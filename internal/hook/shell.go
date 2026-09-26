package hook

import (
	"path"
	"regexp"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/scan"
)

// Shell command rules (RIGFILE_PLAN.md §8.2, docs/targets/claude-code.md §12.3). Claude Code's own Bash
// permission rules do not match `/bin/rm`, `sh -c '...'`, `git -C . push`, `git -c ... push`; this guard
// sees the whole command text, so it unwraps those forms before applying the rules. It is still a
// tokenizer, not a shell: variable indirection, aliases, `python -c`, encoded payloads and other
// obfuscation evade it, and docs/red-team.md lists what is known to. Malformed input fails closed elsewhere.

const maxShellDepth = 4

func checkShell(cmd string) Decision { return checkShellDepth(cmd, 0) }

func checkShellDepth(cmd string, depth int) Decision {
	if depth > maxShellDepth {
		return Decision{}
	}
	if pipeToShell.MatchString(cmd) {
		return Decision{Ask, "This pipes a download straight into a shell. Download the script, review it, then run it.", "no-pipe-to-shell"}
	}
	var best Decision
	consider := func(d Decision) {
		if d.Verdict == Deny || (d.Verdict == Ask && best.Verdict == NoOpinion) {
			if best.Verdict != Deny {
				best = d
			}
		}
	}
	for _, sub := range splitCommands(cmd) {
		w := fields(sub)
		w, sudo := unwrap(w)
		if len(w) == 0 {
			continue
		}
		prog := path.Base(w[0])
		args := w[1:]
		if d := checkProgram(prog, args, depth); d.Verdict != NoOpinion {
			consider(d)
			if d.Verdict == Deny {
				return d
			}
		}
		if sudo {
			consider(Decision{Ask, "sudo runs a command with administrator rights. Ask the user to run it, or explain why it is needed.", "ask-sudo"})
		}
	}
	return best
}

func checkProgram(prog string, args []string, depth int) Decision {
	switch prog {
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		if inner := shellDashC(args); inner != "" {
			return checkShellDepth(inner, depth+1)
		}
	case "eval":
		return checkShellDepth(strings.Join(args, " "), depth+1)
	case "git":
		return checkGit(args)
	case "rm":
		return checkRm(args)
	case "chmod", "chown":
		if recursive(args) && (contains(args, "777") || contains(args, "a+rwx") || contains(args, "o+w")) {
			return Decision{Ask, "Recursively opening permissions is risky. Use narrower permissions.", "ask-chmod"}
		}
	case "docker", "podman":
		if len(args) > 0 && args[0] == "run" && contains(args, "--privileged") {
			return Decision{Ask, "A privileged container has full access to the host.", "ask-privileged-container"}
		}
	case "npm", "yarn", "pnpm", "bun":
		if len(args) > 0 && args[0] == "publish" {
			return publishAsk()
		}
	case "twine":
		if len(args) > 0 && args[0] == "upload" {
			return publishAsk()
		}
	case "cargo":
		if len(args) > 0 && args[0] == "publish" {
			return publishAsk()
		}
	case "gem":
		if len(args) > 0 && args[0] == "push" {
			return publishAsk()
		}
	case "env", "printenv", "set":
		// `env`/`printenv`/`set` with nothing to run dump the whole environment, secrets included.
		if len(args) == 0 {
			return dumpEnv()
		}
		if prog == "printenv" && len(args) > 0 && strings.HasPrefix(args[0], "-") {
			return dumpEnv()
		}
	case "export", "declare", "typeset":
		if contains(args, "-p") || contains(args, "-x") || contains(args, "-px") || contains(args, "-xp") {
			return dumpEnv()
		}
	case "compgen":
		if contains(args, "-e") {
			return dumpEnv()
		}
	case "cat", "less", "more", "head", "tail", "bat", "sed", "awk", "grep", "egrep", "fgrep", "rg", "xxd", "hexdump", "strings", "base64", "od", "nl",
		"tac", "cp", "mv", "scp", "rsync", "tar", "zip", "7z", "vim", "vi", "nano", "emacs", "code", "open", "source", ".":
		for _, a := range args {
			if sensitivePathArg(a) {
				return Decision{Deny, "That path holds credentials (" + scan.Base(a) + "). Do not read or copy it; ask the user to supply what you need through a secret reference.", "no-read-credentials"}
			}
		}
	}
	return Decision{}
}

func publishAsk() Decision {
	return Decision{Ask, "Publishing a package is irreversible and public. Confirm with the user first.", "ask-publish"}
}

func dumpEnv() Decision {
	return Decision{Deny, "Printing the whole environment exposes secrets. Read only the specific, non-secret variable you need.", "no-env-dump"}
}

// --- git ---------------------------------------------------------------------------------------------

func checkGit(args []string) Decision {
	if hooksPath.MatchString(strings.Join(args, " ")) {
		return Decision{Deny, "Changing or overriding core.hooksPath disables the repository's safety hooks and is not allowed.", "no-git-bypass"}
	}
	sub, rest := gitSubcommand(args)
	switch sub {
	case "commit", "merge", "rebase", "am", "cherry-pick", "push":
		if noVerifyFlag(sub, rest) {
			return Decision{Deny, "Bypassing git hooks (--no-verify) is not allowed: hooks protect against committing secrets. Fix what the hook reported instead.", "no-git-bypass"}
		}
	}
	switch sub {
	case "push":
		if force, protected := forcePush(rest); force {
			if protected {
				return Decision{Deny, "Force-pushing to main/master rewrites shared history and is not allowed.", "no-force-push-main"}
			}
			return Decision{Ask, "Force-pushing rewrites remote history. Confirm with the user first.", "ask-force-push"}
		}
		return Decision{Ask, "Pushing publishes commits. Confirm with the user first.", "ask-git-push"}
	case "add":
		for _, a := range rest {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if scan.IsSensitiveFilename(a) {
				return Decision{Deny, scan.Base(a) + " looks like a credential file; do not stage it. Add it to .gitignore and use a secret reference.", "no-stage-secrets"}
			}
		}
	}
	return Decision{}
}

// forcePush reports whether the push forces, and whether it targets main/master (or, with no explicit
// target, cannot be shown not to).
func forcePush(rest []string) (force, protected bool) {
	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--force" || a == "-f" || a == "--force-with-lease" || strings.HasPrefix(a, "--force-with-lease=") || a == "--force-if-includes":
			force = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f") && !strings.Contains(a, "="):
			force = true // clustered short flags such as -fu
		case a == "--repo" || a == "--receive-pack" || a == "--exec" || a == "-o" || a == "--push-option":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			positional = append(positional, a)
		}
	}
	if !force {
		for _, p := range positional {
			if strings.HasPrefix(p, "+") { // +refspec forces
				force = true
			}
		}
	}
	if !force {
		return false, false
	}
	for _, p := range positional {
		for _, part := range strings.FieldsFunc(strings.TrimPrefix(p, "+"), func(r rune) bool { return r == ':' || r == '/' }) {
			if part == "main" || part == "master" {
				protected = true
			}
		}
		if p == "--all" || p == "--mirror" {
			protected = true
		}
	}
	return force, protected
}

// --- rm / chmod ----------------------------------------------------------------------------------------

func checkRm(args []string) Decision {
	rec, force := false, false
	var targets []string
	for _, a := range args {
		switch {
		case a == "--recursive" || a == "-r" || a == "-R":
			rec = true
		case a == "--force" || a == "-f":
			force = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--"):
			if strings.ContainsAny(a, "rR") {
				rec = true
			}
			if strings.Contains(a, "f") {
				force = true
			}
		case strings.HasPrefix(a, "--"):
		default:
			targets = append(targets, a)
		}
	}
	if !(rec && force) {
		return Decision{}
	}
	for _, t := range targets {
		if strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~") || strings.Contains(t, "$") || strings.Contains(t, "..") || t == "." || t == "*" {
			return Decision{Ask, "Recursive forced deletion outside the current project (" + t + "). Confirm the exact path with the user first.", "ask-rm-rf"}
		}
	}
	return Decision{}
}

func recursive(args []string) bool {
	for _, a := range args {
		if a == "-R" || a == "--recursive" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "Rr")) {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// --- unwrapping -------------------------------------------------------------------------------------------

// wrapperValueFlags lists, per wrapper program, the short/long options that take a value as the next token.
var wrapperValueFlags = map[string][]string{
	"sudo":    {"-u", "-g", "-h", "-p", "-C", "-D", "-R", "-T", "-U", "--user", "--group", "--host", "--prompt"},
	"env":     {"-u", "-C", "-S", "--unset", "--chdir"},
	"timeout": {"-k", "-s", "--kill-after", "--signal"},
	"nice":    {"-n", "--adjustment"},
	"xargs":   {"-n", "-I", "-P", "-d", "-L", "-E", "-s", "-a", "--max-args", "--replace", "--max-procs", "--delimiter", "--max-lines", "--arg-file"},
	"stdbuf":  {"-i", "-o", "-e"},
	"ionice":  {"-c", "-n", "-p"},
	"nohup":   nil, "time": nil, "command": nil, "builtin": nil, "exec": nil, "setsid": nil, "doas": {"-u", "-C"},
}

var assignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// unwrap strips leading VAR=value assignments and wrapper programs (sudo, env, timeout, nice, nohup, xargs,
// command, exec, ...) so the real program is w[0]. sudo reports whether a privilege wrapper was seen.
func unwrap(w []string) (out []string, sudo bool) {
	last := ""
	defer func() {
		if len(out) == 0 && last == "env" {
			out = []string{"env"} // `env` (or `env FOO=x`) with no command prints the whole environment
		}
	}()
	for len(w) > 0 {
		for len(w) > 0 && assignRe.MatchString(w[0]) {
			w = w[1:]
		}
		if len(w) == 0 {
			break
		}
		prog := path.Base(w[0])
		valueFlags, isWrapper := wrapperValueFlags[prog]
		if !isWrapper {
			break
		}
		last = prog
		if prog == "sudo" || prog == "doas" {
			sudo = true
		}
		w = w[1:]
		for len(w) > 0 && strings.HasPrefix(w[0], "-") && w[0] != "-" {
			flag := w[0]
			w = w[1:]
			if flag == "--" {
				break
			}
			for _, vf := range valueFlags {
				if flag == vf && len(w) > 0 {
					w = w[1:]
					break
				}
			}
		}
		if prog == "timeout" && len(w) > 0 { // the duration
			w = w[1:]
		}
	}
	return w, sudo
}

// shellDashC returns the command string of `sh -c '...'` / `bash -lc '...'`.
func shellDashC(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-c" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.HasSuffix(a, "c") && len(a) <= 4) {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return "" // a script path: not our business
		}
	}
	return ""
}

// --- credential paths -------------------------------------------------------------------------------------

var credentialFragments = []string{
	"/.ssh/", "/.aws/", "/.gnupg/", "/.kube/", "/.config/gcloud/", "/.azure/", "/.docker/config.json", "/.claude/.credentials.json", "/.claude.json",
	"/.rigfile/", "/.config/rigfile/", "/.local/share/keyrings/", "/library/keychains/", "/etc/shadow", "/etc/sudoers", "/proc/", "/.netrc", "/.npmrc", "/.pypirc",
}

// sensitivePathArg reports whether a command argument names a credential file or directory. Options such
// as --file=PATH are looked into.
func sensitivePathArg(a string) bool {
	if i := strings.Index(a, "="); i > 0 && strings.HasPrefix(a, "-") {
		a = a[i+1:]
	}
	if a == "" || strings.HasPrefix(a, "-") {
		return false
	}
	p := strings.NewReplacer("${HOME}", "~", "$HOME", "~").Replace(strings.ReplaceAll(a, `\`, "/"))
	if scan.IsSensitiveFilename(p) {
		return true
	}
	norm := "/" + strings.TrimPrefix(strings.ToLower(p), "~/")
	if !strings.HasSuffix(norm, "/") {
		norm += "/"
	}
	for _, f := range credentialFragments {
		if strings.Contains(norm, f) || strings.Contains(strings.TrimSuffix(norm, "/"), f) {
			if f == "/proc/" && !strings.Contains(norm, "/environ") {
				continue // only /proc/*/environ leaks secrets
			}
			return true
		}
	}
	return false
}
