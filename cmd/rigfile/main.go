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
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rigfile/rigfile/internal/adapters/claudecode"
	"github.com/rigfile/rigfile/internal/models"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/rigd"
	"github.com/rigfile/rigfile/internal/sandbox"
	"github.com/rigfile/rigfile/internal/sigverify"
	"github.com/rigfile/rigfile/internal/source"
	"github.com/rigfile/rigfile/internal/tools"
)

var version = "0.1.0-stage1"

// env bundles process I/O and machine access so tests can drive run() with a temp HOME.
type env struct {
	in           io.Reader
	out, err     io.Writer
	getenv       func(string) string
	stateDir     string                                                  // "" = platform default
	mcp          claudecode.MCPClient                                    // nil = the real `claude` CLI
	keyringOff   bool                                                    // tests: force the encrypted-file secret backend
	lookPath     func(string) (string, error)                            // nil = exec.LookPath
	tools        tools.Host                                              // nil = run real package managers
	interactive  bool                                                    // tests: behave as if stdin/stdout were a terminal
	hidden       func(prompt string) ([]byte, error)                     // tests: replaces the hidden-input prompt
	sources      *source.Client                                          // nil = the real services, cached under the state directory
	sleep        func(time.Duration)                                     // tests: replaces the device-flow polling delay
	pollEvery    time.Duration                                           // tests: how often publish checks the registry scan
	runCmd       func(ctx context.Context, argv []string) error          // tests: replaces running a vendor login command
	openURL      func(string) error                                      // tests: replaces opening the browser
	verifySig    func(bundle, tarball []byte) (*sigverify.Result, error) // tests: replaces Sigstore verification
	brokerAct    rigd.Activator                                          // tests: replaces launchctl / systemctl / schtasks
	modelDeps    func(models.Deps) models.Deps                           // tests: replaces how model setup reaches the machine
	runAgent     func(path string, args, env, drop []string) int         // tests: replaces launching codex or claude
	detectModels func() []models.Detected                                // tests: replaces looking for a running Ollama
	hardware     *platform.Hardware                                      // tests: replaces hardware detection
	exe          string                                                  // tests: the path installed into service files ("" = this binary)
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
	case "pull":
		return cmdPull("pull", args[1:], e)
	case "update":
		return cmdPull("update", args[1:], e)
	case "sync":
		return cmdSync(args[1:], e)
	case "models":
		return cmdModels(args[1:], e)
	case "ui":
		return cmdUI(args[1:], e)
	case "org":
		return cmdOrg(args[1:], e)
	case "collection":
		return cmdCollection(args[1:], e)
	case "fork":
		return cmdFork(args[1:], e)
	case "changes":
		return cmdChanges(args[1:], e)
	case "self-update":
		return cmdSelfUpdate(args[1:], e)
	case "verify-signature":
		return cmdVerifySignature(args[1:], e)
	case "login":
		return cmdLogin(args[1:], e)
	case "logout":
		return cmdLogout(args[1:], e)
	case "whoami":
		return cmdWhoami(args[1:], e)
	case "logins":
		return cmdLogins(args[1:], e)
	case "publish":
		return cmdPublish(args[1:], e)
	case "diff":
		return cmdDiff(rest, e)
	case "rollback":
		return cmdRollback(rest, e)
	case "lock":
		return cmdLock(rest, e)
	case "doctor":
		return cmdDoctor(rest, e)
	case "broker":
		return cmdBroker(rest, e)
	case "secrets":
		return cmdSecrets(rest, e)
	case "exec":
		return cmdExec(rest, e)
	case sandbox.LandlockExecSubcommand: // hidden: internal/sandbox re-execs this binary through itself (Linux Level 2 confinement)
		return sandbox.LandlockExecMain(rest)
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
  pull <source> [--plan-only]        fetch a rig from github.com/o/r[@ref][//dir] (or gitlab.com, https/ssh git URL), review, apply
  update [--plan-only]               re-resolve the source of the last pulled rig and show what changed
  login | logout | whoami            sign in to a Rigfile registry (device flow; token kept in your keychain)
  publish [--to-git DIR] [--to-registry [--public]]   scrub your setup (or a rig dir); write a repo and/or publish to the registry
  logins [--provider name]           walk through the logins the applied rig needs
  sync init|join|approve|finish|track|status|push|pull   end-to-end encrypted sync of your own private files between your machines
  models list|pull|status|serve|url|run|rm   local models: choose per machine, verified download, service, agents
  ui [<rig-dir>] [--no-open]           the plan and your checklist in a browser page on this computer
  org create|list|members|add|rm       organisations: a namespace several people publish under
  collection create|add|rm|delete|show|list   curated lists of rigs on the registry
  fork <source> --name owner/name    start your own rig from someone else's (a copy, or --extend to build on it)
  changes <before> <after> [--diff]  what a new version of a rig adds, removes and changes
  self-update [--check]              install the latest release after verifying its signature and checksum
  verify-signature <file> [--pubkey k] check a minisign signature
  diff                               show drift since the last apply
  rollback [<run-id>] [--list]       undo a run (newest by default)
  lock [<rig-dir>]                   write or refresh rigfile.lock
  doctor                             health check
  secrets set|rm|status|list         manage secrets (values are never printed)
  broker run|status|enable|exclude   Level 2: the secret broker (docs/rigd.md)
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
