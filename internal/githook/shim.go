package githook

import "strings"

// Names of the git hooks Rigfile installs.
var HookNames = []string{"pre-commit", "pre-push", "reference-transaction"}

// ShimScript is the file git executes for hook `name`. It is deliberately tiny and does no scanning: the
// logic is in the Go binary (identical on every OS). bin is the absolute path of the rigfile executable.
//
// The reference-transaction shim filters in `sh` before starting the binary: git calls that hook five times
// for one `git commit` (preparing/prepared/committed/aborted...), and starting a Go process each time
// costs ~6 ms. Only the "prepared" state of a branch or tag update can be blocked, so everything else exits
// in the shell.
func ShimScript(name, bin string) string {
	q := "'" + strings.ReplaceAll(bin, "'", `'\''`) + "'"
	if name == "reference-transaction" {
		return "#!/bin/sh\n" +
			"# managed by rigfile (base-secure): backstop that `git commit --no-verify` cannot skip\n" +
			"[ \"$1\" = prepared ] || exit 0\n" +
			"input=\n" +
			"while IFS= read -r line; do input=\"$input$line\n\"; done\n" +
			"case \"$input\" in *\" refs/heads/\"*|*\" refs/tags/\"*) ;; *) exit 0 ;; esac\n" +
			"printf '%s\\n' \"$input\" | " + q + " hook reference-transaction \"$1\"\n" +
			"exit $?\n"
	}
	return "#!/bin/sh\n# managed by rigfile (base-secure)\nexec " + q + " hook " + name + " \"$@\"\n"
}
