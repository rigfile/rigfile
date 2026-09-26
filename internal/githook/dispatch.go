package githook

import (
	"fmt"
	"strings"
)

// HookNames are every hook git can run (githooks(5)). With `core.hooksPath` pointing at Rigfile's directory
// git looks ONLY there, so each name needs a file: otherwise the user's own hooks (and the repository's)
// would silently stop running. Every file is a two-line shim to the dispatcher.
var HookNames = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg",
	"post-commit", "pre-rebase", "post-checkout", "post-merge", "pre-push", "pre-receive", "update", "proc-receive", "post-receive",
	"post-update", "reference-transaction", "push-to-checkout", "pre-auto-gc", "post-rewrite", "sendemail-validate", "fsmonitor-watchman",
	"p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit", "post-index-change",
}

// DispatcherName is the one real script in the hooks directory.
const DispatcherName = "_rigfile-dispatch"

// PreviousFile records the hooks directory that was configured before Rigfile (chained to).
const PreviousFile = ".previous-hooks-path"

// ShimScript is the file git executes for hook `name`: it hands over to the dispatcher next to it.
func ShimScript(name string) string {
	return "#!/bin/sh\n# managed by rigfile (base-secure)\nexec \"${0%/*}/" + DispatcherName + "\" " + name + " \"$@\"\n"
}

// DispatchScript is the dispatcher. It deliberately does no scanning: the logic is in the Go binary
// (identical on every OS). bin is the absolute path of the rigfile executable; if it has moved the script
// falls back to `rigfile` on PATH, and if that is missing too it WARNS LOUDLY and lets the operation
// through (a missing binary must not brick git in every repository; `rigfile doctor` reports it).
//
// Order for every hook: (1) Rigfile's own check, blocking hooks only; (2) the hooks directory the machine
// had configured before Rigfile (recorded in PreviousFile); (3) the repository's own .git/hooks, which git
// stops running once core.hooksPath is set. The first failure stops the chain.
//
// Speed: git runs reference-transaction five times per `git commit`, so the script filters in `sh` and
// starts Rigfile only for the "prepared" state of a branch or tag update. Hooks that carry stdin are read
// once with the shell builtin `read` so each consumer sees the same input.
func DispatchScript(bin string, backstop bool) string {
	q := "'" + strings.ReplaceAll(bin, "'", `'\''`) + "'"
	b := 0
	if backstop {
		b = 1
	}
	return fmt.Sprintf(`#!/bin/sh
# managed by rigfile (base-secure). `+"`rigfile apply`"+` rewrites this file and `+"`rigfile doctor`"+` checks it; do not edit.
name=$1; shift
here=${0%%/*}
RIGFILE=%s
BACKSTOP=%d

input=
case $name in
  pre-push|reference-transaction|post-rewrite|pre-receive|post-receive|proc-receive)
    while IFS= read -r line; do input="$input$line
"; done ;;
esac

rf=
if [ -x "$RIGFILE" ]; then rf=$RIGFILE; else rf=$(command -v rigfile 2>/dev/null); fi

own() {
  if [ -z "$rf" ]; then
    echo "rigfile: WARNING: rigfile was not found, so the $name check was skipped (run: rigfile doctor)" >&2
    return 0
  fi
  case $name in
    pre-commit) "$rf" hook pre-commit ;;
    commit-msg) "$rf" hook commit-msg "$@" ;;
    pre-push) printf '%%s' "$input" | "$rf" hook pre-push "$@" ;;
    reference-transaction)
      [ "$BACKSTOP" = 1 ] || return 0
      [ "$1" = prepared ] || return 0
      case $input in *" refs/heads/"*|*" refs/tags/"*) ;; *) return 0 ;; esac
      printf '%%s' "$input" | "$rf" hook reference-transaction "$1" ;;
  esac
}

case $name in
  pre-commit|commit-msg|pre-push|reference-transaction) own "$@" || exit $? ;;
esac

chain() {
  h=$1; shift
  [ -f "$h" ] && [ -x "$h" ] || return 0
  if [ -n "$input" ]; then printf '%%s' "$input" | "$h" "$@"; else "$h" "$@"; fi
}

prev=
[ -f "$here/%s" ] && IFS= read -r prev < "$here/%s"
if [ -n "$prev" ] && [ "$prev" != "$here" ]; then chain "$prev/$name" "$@" || exit $?; fi

gd=${GIT_DIR:-}
if [ -z "$gd" ]; then
  if [ -d .git ]; then gd=.git
  elif [ -f .git ]; then IFS= read -r l < .git; gd=${l#gitdir: }
  else gd=$(git rev-parse --git-common-dir 2>/dev/null); fi
fi
if [ -f "$gd/commondir" ]; then IFS= read -r c < "$gd/commondir"; case $c in /*) gd=$c ;; *) gd="$gd/$c" ;; esac; fi
case $gd in */worktrees/*) gd=${gd%%/worktrees/*} ;; esac
if [ -n "$gd" ]; then chain "$gd/hooks/$name" "$@" || exit $?; fi
exit 0
`, q, b, PreviousFile, PreviousFile)
}
