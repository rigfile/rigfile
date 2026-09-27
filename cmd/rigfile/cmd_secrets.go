package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/digitaldreamer3462/rigfile/internal/execshim"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/secrets"
	"github.com/digitaldreamer3462/rigfile/internal/session"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

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

// keyringDisabled is true when the OS keychain must not be used: in tests, and for scripts and CI that set
// RIGFILE_SECRETS_BACKEND=file (the encrypted file backend, which needs RIGFILE_PASSPHRASE_FILE). Any script that runs the
// real CLI on a developer machine must set it, so it never reaches the developer's own keychain.
func keyringDisabled(e env) bool {
	return e.keyringOff || e.getenv("RIGFILE_SECRETS_BACKEND") == "file"
}

func openStore(e env, pi *platform.Info) (secrets.Store, error) {
	sd, err := stateDirFor(e, pi)
	if err != nil {
		return nil, err
	}
	o := secrets.OpenOptions{FilePath: filepath.Join(sd, "secrets.age"), Passphrase: passphrase(e)}
	if keyringDisabled(e) {
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

func secretRefs(needs []state.Need) []string {
	var out []string
	for _, n := range needs {
		if n.Kind == "secret" {
			out = append(out, n.Ref)
		}
	}
	return out
}

// secretStatuses reads set/not-set WITHOUT prompting: only the OS keychain is consulted. With the
// encrypted-file backend the status is unknown (it would need a passphrase), so nothing is returned.
func secretStatuses(e env, pi *platform.Info, refs []string) map[string]string {
	if keyringDisabled(e) || len(refs) == 0 || secrets.KeyringAvailable() != nil {
		return nil
	}
	out := map[string]string{}
	st := secrets.KeyringStore{}
	for _, r := range refs {
		if _, err := st.Get(r); err == nil {
			out[r] = "set"
		} else if secrets.IsNotFound(err) {
			out[r] = "not set"
		}
	}
	return out
}

func cmdSecrets(args []string, e env) int {
	if len(args) < 1 {
		fmt.Fprintln(e.err, "usage: rigfile secrets set|rm|status|list [<ref>...]")
		return 2
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	verb, refs := args[0], args[1:]
	if verb == "list" {
		return secretsList(e, pi)
	}
	if (verb == "set" || verb == "rm") && len(refs) != 1 {
		fmt.Fprintf(e.err, "usage: rigfile secrets %s <ref>\n", verb)
		return 2
	}
	if verb != "set" && verb != "rm" && verb != "status" {
		fmt.Fprintf(e.err, "rigfile secrets: unknown subcommand %q\n", verb)
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
	}
	return 0
}

// secretsList shows what the applied rig needs and whether each secret is set.
func secretsList(e env, pi *platform.Info) int {
	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	s, err := state.Load(sd)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	ts := primaryApplied(s)
	if ts == nil || len(ts.Needs) == 0 {
		fmt.Fprintln(e.out, "no secrets or logins are required by the applied rig")
		return 0
	}
	status := secretStatuses(e, pi, secretRefs(ts.Needs))
	missing := 0
	for _, n := range ts.Needs {
		if n.Kind != "secret" {
			fmt.Fprintf(e.out, "login   %s   %s\n", n.Ref, n.Description)
			continue
		}
		st := status[n.Ref]
		if st == "" {
			st = "unknown (encrypted-file backend; use `rigfile secrets status " + n.Ref + "`)"
		}
		if st == "not set" {
			missing++
		}
		fmt.Fprintf(e.out, "secret  %-28s %s   %s\n", n.Ref, st, n.Description)
	}
	if missing > 0 {
		fmt.Fprintf(e.out, "%d secret(s) not set: rigfile secrets set <ref>\n", missing)
		return 1
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
	var secretFlags, envFlags, allowFlags, bindFlags kvFlags
	var server string
	fs.Var(&secretFlags, "secret", "ENV=ref: set ENV from a stored secret (repeatable)")
	fs.Var(&envFlags, "env", "K=V: set a literal, non-secret variable (repeatable)")
	fs.Var(&allowFlags, "allow", "host[,host]: the server's network.allow (Level 2, repeatable)")
	fs.Var(&bindFlags, "bind", "ref=host[,host]: where a secret may be sent (informational: the broker uses the policy approved by `rigfile apply`)")
	fs.StringVar(&server, "server", "", "the server's name (Level 2: audit log and broker exclusions)")
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
	var allow []string
	for _, a := range allowFlags {
		allow = append(allow, strings.Split(a, ",")...)
	}
	binds := map[string][]string{}
	for _, b := range bindFlags {
		ref, hosts, ok := strings.Cut(b, "=")
		if !ok || ref == "" || hosts == "" {
			fmt.Fprintf(e.err, "rigfile: --bind wants ref=host[,host], got %q\n", b)
			return 2
		}
		binds[ref] = append(binds[ref], strings.Split(hosts, ",")...)
	}
	if server == "" {
		server = filepath.Base(cmd[0])
	}
	if rd, derr := rigdDir(e); derr == nil {
		l2, notice, lerr := startLevel2(rd, server, allow, sec)
		if lerr != nil {
			fmt.Fprintln(e.err, "rigfile:", lerr)
			return 1
		}
		if notice != "" {
			fmt.Fprintln(e.err, notice)
		}
		if l2 != nil {
			defer l2.end()
			for k, v := range l2.env {
				lit[k] = v
			}
			sec = map[string]string{} // the real values never reach this process
		}
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

// isInteractive reports whether we can prompt the user.
func isInteractive(e env) bool {
	if e.interactive {
		return true
	}
	_, ok := isTTY(e.in)
	return ok
}

// readHidden reads one secret value from the terminal without echo.
func readHidden(e env, prompt string) ([]byte, error) {
	if e.hidden != nil {
		return e.hidden(prompt)
	}
	fd, ok := isTTY(e.in)
	if !ok {
		return nil, errors.New("not a terminal")
	}
	fmt.Fprint(e.err, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(e.err)
	return b, err
}

// promptMissingSecrets is the batched "secrets needed" step at the end of apply: after everything else
// succeeded, offer to set every declared secret that is still missing. Interactive only; an empty value
// skips that secret. It never prints values.
func promptMissingSecrets(e env, p *session.Prepared) {
	if !isInteractive(e) {
		return
	}
	var refs []state.Need
	for _, n := range p.Needs() {
		if n.Kind == "secret" {
			refs = append(refs, n)
		}
	}
	if len(refs) == 0 {
		return
	}
	st, err := openStore(e, p.Plat)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile: cannot check secrets:", err)
		return
	}
	var missing []state.Need
	for _, n := range refs {
		if _, err := st.Get(n.Ref); secrets.IsNotFound(err) {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return
	}
	names := make([]string, len(missing))
	for i, n := range missing {
		names[i] = n.Ref
	}
	if !confirm(e, fmt.Sprintf("\n%d secret(s) are not set yet: %s\nSet them now? (input is hidden; leave empty to skip one)  [y]es  [n]o ", len(missing), strings.Join(names, ", "))) {
		fmt.Fprintln(e.out, "skipped; set them later with `rigfile secrets set <ref>`")
		return
	}
	for _, n := range missing {
		if n.ObtainURL != "" {
			fmt.Fprintf(e.out, "%s: get it at %s\n", n.Ref, n.ObtainURL)
		}
		v, err := readHidden(e, "Value for "+n.Ref+": ")
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return
		}
		if len(v) == 0 {
			fmt.Fprintf(e.out, "skipped %s\n", n.Ref)
			continue
		}
		if err := st.Set(n.Ref, v); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return
		}
		fmt.Fprintf(e.out, "stored %s in the %s\n", n.Ref, st.Kind())
	}
}
