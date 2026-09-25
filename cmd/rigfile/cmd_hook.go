package main

import (
	"fmt"

	"github.com/digitaldreamer3462/rigfile/internal/hook"
)

// cmdHook runs a built-in agent hook. Claude Code passes the event JSON on stdin. `hook run <name>` is
// what the adapter writes into settings.json; `hook pre-tool-use` is kept as an alias for `run guard`.
func cmdHook(args []string, e env) int {
	name := ""
	switch {
	case len(args) == 2 && args[0] == "run":
		name = args[1]
	case len(args) == 1 && args[0] == "pre-tool-use":
		name = "guard"
	default:
		fmt.Fprintln(e.err, "usage: rigfile hook run <name>")
		return 2
	}
	if !hook.KnownBuiltin(name) {
		fmt.Fprintf(e.err, "rigfile hook: unknown built-in %q\n", name)
		return 2
	}
	in, err := hook.ParseInput(e.in)
	if err != nil {
		// Fail closed: exit 2 makes Claude Code block the tool call and show stderr to the model.
		fmt.Fprintln(e.err, "rigfile hook: could not read hook input; blocking:", err)
		return 2
	}
	switch name {
	case "guard":
		if in.HookEventName != "" && in.HookEventName != "PreToolUse" {
			return 0 // not our event: no opinion
		}
		out, err := hook.PreToolUse(in).Output()
		if err != nil {
			fmt.Fprintln(e.err, "rigfile hook:", err)
			return 2
		}
		if out != nil {
			fmt.Fprintln(e.out, string(out))
		}
	}
	return 0
}
