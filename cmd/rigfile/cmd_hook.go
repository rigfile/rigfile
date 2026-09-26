package main

import (
	"fmt"

	"github.com/digitaldreamer3462/rigfile/internal/githook"
	"github.com/digitaldreamer3462/rigfile/internal/hook"
)

// cmdGitHook runs the git-side checks (plan §8.1b-c). The files git executes are one-line shims that call
// `rigfile hook <name> "$@"`; all logic lives here.
func cmdGitHook(args []string, e env) int {
	g := githook.Git{}
	switch args[0] {
	case "pre-commit":
		return githook.PreCommit(g, githook.DefaultScanner, e.err)
	case "pre-push":
		remote := ""
		if len(args) > 1 {
			remote = args[1]
		}
		return githook.PrePush(g, githook.DefaultScanner, remote, e.in, e.err)
	default: // reference-transaction <state>
		if len(args) < 2 {
			fmt.Fprintln(e.err, "usage: rigfile hook reference-transaction <state>")
			return 2
		}
		return githook.ReferenceTransaction(g, githook.DefaultScanner, args[1], e.in, e.err)
	}
}

// cmdHook runs a built-in agent hook. Claude Code passes the event JSON on stdin. `hook run <name>` is
// what the adapter writes into settings.json; `hook pre-tool-use` is kept as an alias for `run guard`.
func cmdHook(args []string, e env) int {
	if len(args) > 0 {
		switch args[0] {
		case "pre-commit", "pre-push", "reference-transaction":
			return cmdGitHook(args, e)
		}
	}
	name := ""
	switch {
	case len(args) == 2 && args[0] == "run":
		name = args[1]
	case len(args) == 1 && args[0] == "pre-tool-use":
		name = "guard"
	default:
		fmt.Fprintln(e.err, "usage: rigfile hook run <name> | pre-commit | pre-push <remote> | reference-transaction <state>")
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
