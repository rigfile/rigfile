// Command rigfile is the Rigfile CLI (Stage 1: local rigs, Claude Code target, macOS + Linux).
//
//	rigfile init     [--out DIR]             capture your current Claude Code setup into a new rig
//	rigfile validate <rig-dir|rigfile.yaml>
//	rigfile plan     [<rig-dir>] [flags]     show what would change; change nothing
//	rigfile apply    [<rig-dir>] [flags]     apply it (review screen, backup first)
//	rigfile diff                             drift between the machine and what was applied
//	rigfile rollback [<run-id>] [--list]     undo a run
//	rigfile lock     [<rig-dir>]             write/refresh rigfile.lock
//	rigfile doctor                           health check
//	rigfile secrets  set|rm|status|list      manage secrets (values are never printed)
//	rigfile exec     ... -- <cmd>            run a command with secrets in ITS environment only
//	rigfile hook     run <name>              built-in agent hooks (called by Claude Code)
//
// Exit codes: 0 ok, 1 error or problems, 2 usage, 3 applied but some items were refused (conflicts).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/tools"
)

var version = "0.1.0-stage1"

// env bundles process I/O and machine access so tests can drive run() with a temp HOME.
type env struct {
	in          io.Reader
	out, err    io.Writer
	getenv      func(string) string
	stateDir    string                              // "" = platform default
	mcp         claudecode.MCPClient                // nil = the real `claude` CLI
	keyringOff  bool                                // tests: force the encrypted-file secret backend
	lookPath    func(string) (string, error)        // nil = exec.LookPath
	tools       tools.Host                          // nil = run real package managers
	interactive bool                                // tests: behave as if stdin/stdout were a terminal
	hidden      func(prompt string) ([]byte, error) // tests: replaces the hidden-input prompt
}

func (e env) look(name string) (string, error) {
	if e.lookPath != nil {
		return e.lookPath(name)
	}
	return exec.LookPath(name)
}

func main() {
	e := env{in: os.Stdin, out: os.Stdout, err: os.Stderr, getenv: os.Getenv}
	os.Exit(run(os.Args[1:], e))
}

func run(args []string, e env) int {
	if len(args) == 0 {
		usage(e.err)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(e.out, "rigfile", version)
		return 0
	case "init":
		return cmdInit(rest, e)
	case "validate":
		return cmdValidate(rest, e)
	case "plan", "apply":
		return cmdPlanApply(args[0], rest, e)
	case "diff":
		return cmdDiff(rest, e)
	case "rollback":
		return cmdRollback(rest, e)
	case "lock":
		return cmdLock(rest, e)
	case "doctor":
		return cmdDoctor(rest, e)
	case "secrets":
		return cmdSecrets(rest, e)
	case "exec":
		return cmdExec(rest, e)
	case "hook":
		return cmdHook(rest, e)
	case "help", "-h", "--help":
		usage(e.out)
		return 0
	}
	fmt.Fprintf(e.err, "rigfile: unknown command %q\n\n", args[0])
	usage(e.err)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: rigfile <command> [args]

  init [--out DIR] [--name o/n]      capture your current Claude Code setup into a new rig (read-only)
  validate <rig-dir|file>            check a rig against the schema and its own consistency rules
  plan  [<rig-dir>]                  show what would change (nothing is written)
  apply [<rig-dir>]                  apply the rig to Claude Code (review, confirm, backup first)
  diff                               show drift since the last apply
  rollback [<run-id>] [--list]       undo a run (newest by default)
  lock [<rig-dir>]                   write or refresh rigfile.lock
  doctor                             health check
  secrets set|rm|status|list         manage secrets (values are never printed)
  exec [--secret ENV=ref]... -- cmd  run cmd with secrets injected into ITS environment only
  hook run <name>                    built-in agent hook (used by Claude Code)

flags for plan/apply: --layers DIR  --project DIR  --claude-dir DIR  --overwrite
flags for apply:      --yes  --update-lock
`)
}

func platformInfo(e env) (*platform.Info, error) {
	return platform.New(platform.Options{Getenv: e.getenv})
}

func stateDirFor(e env, pi *platform.Info) (string, error) {
	if e.stateDir != "" {
		return e.stateDir, nil
	}
	return pi.StateDir()
}

func mcpClient(e env) claudecode.MCPClient {
	if e.mcp != nil {
		return e.mcp
	}
	return claudecode.CLIClient{}
}

// parseInterspersed parses flags and collects positional arguments in any order
// (`rigfile plan ./rig --yes` and `rigfile plan --yes ./rig` both work).
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
