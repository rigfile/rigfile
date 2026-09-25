// Command rigfile is the Rigfile CLI. Stage 1 SPIKE: a vertical slice, not the full command set.
//
//	rigfile validate <rigfile.yaml>
//	rigfile plan  claude --rig <rigfile.yaml> --settings <settings.json>
//	rigfile apply claude --rig <rigfile.yaml> --settings <settings.json> [--yes]
//	rigfile hook pre-tool-use            (reads Claude Code's hook JSON on stdin)
//	rigfile secrets set|rm|status <ref>
//	rigfile exec [--secret ENV=ref]... [--env K=V]... -- <command> [args...]
//
// --settings is required in the spike so nothing touches a real ~/.claude by accident.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/execshim"
	"github.com/digitaldreamer3462/rigfile/internal/hook"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/secrets"
)

var version = "0.0.0-spike"

// env bundles process I/O so tests can drive run() without touching the real terminal or HOME.
type env struct {
	in         io.Reader
	out, err   io.Writer
	stateDir   string // "" = platform default
	getenv     func(string) string
	keyringOff bool // tests: force the encrypted-file fallback
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
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(e.out, "rigfile", version)
		return 0
	case "validate":
		return cmdValidate(args[1:], e)
	case "plan", "apply":
		return cmdPlanApply(args[0], args[1:], e)
	case "hook":
		return cmdHook(args[1:], e)
	case "secrets":
		return cmdSecrets(args[1:], e)
	case "exec":
		return cmdExec(args[1:], e)
	case "help", "-h", "--help":
		usage(e.out)
		return 0
	}
	fmt.Fprintf(e.err, "rigfile: unknown command %q\n\n", args[0])
	usage(e.err)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: rigfile <command>

  validate <rigfile.yaml>                                   check a manifest against the schema
  plan  claude --rig F --settings S                         show what would change in Claude Code settings
  apply claude --rig F --settings S [--yes]                 apply it (backup first)
  hook pre-tool-use                                         Claude Code PreToolUse guard (JSON on stdin)
  secrets set|rm|status <ref>                               manage secrets (values are never printed)
  exec [--secret ENV=ref]... [--env K=V]... -- cmd args     run cmd with secrets in ITS environment only
`)
}

func platformInfo(e env) (*platform.Info, error) {
	return platform.New(platform.Options{Getenv: e.getenv})
}

func stateDir(e env, pi *platform.Info) (string, error) {
	if e.stateDir != "" {
		return e.stateDir, nil
	}
	return pi.StateDir()
}

// --- validate / plan / apply ------------------------------------------------------------------

func cmdValidate(args []string, e env) int {
	if len(args) != 1 {
		fmt.Fprintln(e.err, "usage: rigfile validate <rigfile.yaml>")
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	v, err := manifest.NewValidator()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if err := v.ValidateYAML(data); err != nil {
		fmt.Fprintln(e.err, err)
		return 1
	}
	fmt.Fprintf(e.out, "%s: valid\n", args[0])
	return 0
}

func cmdPlanApply(verb string, args []string, e env) int {
	if len(args) == 0 || args[0] != "claude" {
		fmt.Fprintf(e.err, "usage: rigfile %s claude --rig <rigfile.yaml> --settings <settings.json>\n", verb)
		return 2
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(e.err)
	rig := fs.String("rig", "", "path to rigfile.yaml")
	settings := fs.String("settings", "", "path to Claude Code settings.json (required in the spike)")
	yes := fs.Bool("yes", false, "apply without asking")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *rig == "" || *settings == "" {
		fmt.Fprintln(e.err, "rigfile: --rig and --settings are required")
		return 2
	}

	rigData, err := os.ReadFile(*rig)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	v, err := manifest.NewValidator()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if err := v.ValidateYAML(rigData); err != nil {
		fmt.Fprintln(e.err, err)
		return 1
	}
	perms, err := manifest.ParsePermissions(rigData)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	cur, err := os.ReadFile(*settings)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	plan, err := claudecode.PlanPermissions(pi, cur, perms)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	printPlan(e.out, *settings, plan)
	if verb == "plan" || plan.Empty() {
		return 0
	}

	if !*yes && !confirm(e, fmt.Sprintf("Apply %d change(s) to %s?", len(plan.Adds), *settings)) {
		fmt.Fprintln(e.out, "aborted; nothing changed")
		return 1
	}
	next, err := plan.Apply(cur)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	sd, err := stateDir(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	w := &apply.Writer{BackupRoot: filepath.Join(sd, "backups")}
	res, err := w.WriteFile(*settings, next)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if res.BackupPath != "" {
		fmt.Fprintf(e.out, "backup: %s\n", res.BackupPath)
	}
	runID, err := w.Commit("apply claude permissions from " + *rig)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintf(e.out, "applied %d change(s) to %s (run %s)\n", len(plan.Adds), res.Path, runID)
	return 0
}

func printPlan(w io.Writer, settings string, p claudecode.Plan) {
	fmt.Fprintf(w, "Claude Code  %s\nPERMISSIONS\n", settings)
	for _, c := range p.Adds {
		fmt.Fprintf(w, "  + %-5s %s\n", c.List, c.Rule)
	}
	for _, c := range p.Present {
		fmt.Fprintf(w, "  = %-5s %s   (already present)\n", c.List, c.Rule)
	}
	for _, d := range p.Dropped {
		fmt.Fprintf(w, "  ! %-5s %s   %s\n", d.List, d.Rule, d.Reason)
	}
	if p.Empty() {
		fmt.Fprintln(w, "no changes")
	} else {
		fmt.Fprintf(w, "%d change(s)\n", len(p.Adds))
	}
}

func confirm(e env, prompt string) bool {
	fmt.Fprintf(e.out, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(e.in).ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes"
}

// --- hook -------------------------------------------------------------------------------------

func cmdHook(args []string, e env) int {
	if len(args) != 1 {
		fmt.Fprintln(e.err, "usage: rigfile hook pre-tool-use")
		return 2
	}
	switch args[0] {
	case "pre-tool-use":
		in, err := hook.ParseInput(e.in)
		if err != nil {
			// Fail closed: exit 2 makes Claude Code block the tool call and show stderr to the model.
			fmt.Fprintln(e.err, "rigfile hook: could not read hook input; blocking:", err)
			return 2
		}
		out, err := hook.PreToolUse(in).Output()
		if err != nil {
			fmt.Fprintln(e.err, "rigfile hook:", err)
			return 2
		}
		if out != nil {
			fmt.Fprintln(e.out, string(out))
		}
		return 0
	}
	fmt.Fprintf(e.err, "rigfile hook: unsupported event %q\n", args[0])
	return 2
}

// --- secrets / exec ---------------------------------------------------------------------------

func isTTY(r io.Reader) (int, bool) {
	if f, ok := r.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return int(f.Fd()), true
	}
	return 0, false
}

// passphrase asks for the file-store passphrase: a hidden TTY prompt, or the contents of the file named
// by RIGFILE_PASSPHRASE_FILE (which must be private). Never argv, never a plain env var value.
func passphrase(e env) func() (string, error) {
	return func() (string, error) {
		if p := e.getenv("RIGFILE_PASSPHRASE_FILE"); p != "" {
			if err := platform.IsPrivateFile(p); err != nil {
				return "", fmt.Errorf("RIGFILE_PASSPHRASE_FILE: %w", err)
			}
			b, err := os.ReadFile(p)
			return strings.TrimRight(string(b), "\r\n"), err
		}
		if fd, ok := isTTY(e.in); ok {
			fmt.Fprint(e.err, "Passphrase for the Rigfile secrets file: ")
			b, err := term.ReadPassword(fd)
			fmt.Fprintln(e.err)
			return string(b), err
		}
		return "", errors.New("the encrypted secrets file needs a passphrase: run in a terminal, or set RIGFILE_PASSPHRASE_FILE to a 0600 file")
	}
}

func openStore(e env, pi *platform.Info) (secrets.Store, error) {
	sd, err := stateDir(e, pi)
	if err != nil {
		return nil, err
	}
	o := secrets.OpenOptions{FilePath: filepath.Join(sd, "secrets.age"), Passphrase: passphrase(e)}
	if e.keyringOff {
		o.KeyringProbe = func() error { return errors.New("keychain disabled") }
		o.WorkFactor = 10
	}
	s, err := secrets.Open(o)
	if err != nil {
		return nil, err
	}
	if s.Kind() != "keychain" {
		fmt.Fprintln(e.err, "warning: no OS keychain available; using the encrypted-file backend (weaker; doctor will flag this)")
	}
	return s, nil
}

func cmdSecrets(args []string, e env) int {
	if len(args) < 1 {
		fmt.Fprintln(e.err, "usage: rigfile secrets set|rm|status <ref>")
		return 2
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	verb, refs := args[0], args[1:]
	if (verb == "set" || verb == "rm") && len(refs) != 1 {
		fmt.Fprintf(e.err, "usage: rigfile secrets %s <ref>\n", verb)
		return 2
	}
	st, err := openStore(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	switch verb {
	case "set":
		val, err := readSecretValue(e, refs[0])
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		if err := st.Set(refs[0], val); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		fmt.Fprintf(e.out, "stored %s in the %s\n", refs[0], st.Kind())
	case "rm":
		if err := st.Delete(refs[0]); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		fmt.Fprintf(e.out, "removed %s\n", refs[0])
	case "status":
		fmt.Fprintf(e.out, "backend: %s\n", st.Kind())
		for _, r := range refs {
			_, err := st.Get(r)
			switch {
			case err == nil:
				fmt.Fprintf(e.out, "  %s: set\n", r)
			case secrets.IsNotFound(err):
				fmt.Fprintf(e.out, "  %s: not set\n", r)
			default:
				fmt.Fprintf(e.out, "  %s: error: %v\n", r, err)
				return 1
			}
		}
	default:
		fmt.Fprintf(e.err, "rigfile secrets: unknown subcommand %q\n", verb)
		return 2
	}
	return 0
}

// readSecretValue reads the value from a hidden TTY prompt or, when stdin is a pipe, from stdin.
// There is deliberately no --value flag: argv ends up in shell history and process listings.
func readSecretValue(e env, ref string) ([]byte, error) {
	if err := secrets.ValidateRef(ref); err != nil {
		return nil, err
	}
	var v []byte
	if fd, ok := isTTY(e.in); ok {
		fmt.Fprintf(e.err, "Value for %s (input hidden): ", ref)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(e.err)
		if err != nil {
			return nil, err
		}
		v = b
	} else {
		b, err := io.ReadAll(io.LimitReader(e.in, 64<<10))
		if err != nil {
			return nil, err
		}
		v = []byte(strings.TrimRight(string(b), "\r\n"))
	}
	if len(v) == 0 {
		return nil, errors.New("empty value; nothing stored")
	}
	return v, nil
}

type kvFlags []string

func (k *kvFlags) String() string     { return strings.Join(*k, ",") }
func (k *kvFlags) Set(s string) error { *k = append(*k, s); return nil }

func cmdExec(args []string, e env) int {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(e.err)
	var secretFlags, envFlags kvFlags
	fs.Var(&secretFlags, "secret", "ENV=ref: set ENV from a stored secret (repeatable)")
	fs.Var(&envFlags, "env", "K=V: set a literal, non-secret variable (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		fmt.Fprintln(e.err, "usage: rigfile exec [--secret ENV=ref]... [--env K=V]... -- <command> [args...]")
		return 2
	}
	sec, lit := map[string]string{}, map[string]string{}
	for _, kv := range secretFlags {
		k, r, ok := strings.Cut(kv, "=")
		if !ok || k == "" || r == "" {
			fmt.Fprintf(e.err, "rigfile: --secret wants ENV=ref, got %q\n", kv)
			return 2
		}
		sec[k] = r
	}
	for _, kv := range envFlags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			fmt.Fprintf(e.err, "rigfile: --env wants K=V, got %q\n", kv)
			return 2
		}
		lit[k] = v
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	var st secrets.Store
	if len(sec) > 0 {
		if st, err = openStore(e, pi); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
	}
	code, err := execshim.Run(context.Background(), execshim.Spec{
		Command: cmd, Secrets: sec, Literal: lit, Store: st, Plat: pi,
		Getenv: e.getenv, Stdin: e.in, Stdout: e.out, Stderr: e.err,
	})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
	}
	return code
}
