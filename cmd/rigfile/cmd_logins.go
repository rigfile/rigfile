package main

import (
	"context"
	"flag"
	"fmt"
	"os/exec"

	"github.com/rigfile/rigfile/internal/login"
	"github.com/rigfile/rigfile/internal/secrets"
	"github.com/rigfile/rigfile/internal/state"
)

// cmdLogins walks through the logins the applied rig declared, one after another (docs/sharing.md §8). It reads the
// list from state.json, so it works after `apply` or `pull` without needing the rig directory.
func cmdLogins(args []string, e env) int {
	fs := flag.NewFlagSet("logins", flag.ContinueOnError)
	fs.SetOutput(e.err)
	only := fs.String("provider", "", "sign in to this provider only")
	method := fs.String("method", "", "override the method: api-key, vendor-cli or oauth (needs --provider)")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 0 || (*method != "" && *only == "") {
		fmt.Fprintln(e.err, "usage: rigfile logins [--provider name [--method api-key|vendor-cli|oauth]]")
		return 2
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	st, err := state.Load(sd)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	var reqs []login.Request
	seen := map[string]bool{}
	for _, at := range appliedTargets(st) {
		for _, n := range at.TS.Needs {
			if n.Kind != "login" || seen[n.Ref] || (*only != "" && n.Ref != *only) {
				continue
			}
			seen[n.Ref] = true
			reqs = append(reqs, login.Request{Provider: n.Ref, Method: login.Method(n.Method), Reason: n.Description})
		}
	}
	if *only != "" && len(reqs) == 0 {
		reqs = append(reqs, login.Request{Provider: *only})
	}
	if *method != "" {
		reqs[0].Method = login.Method(*method)
	}
	if len(reqs) == 0 {
		fmt.Fprintln(e.out, "the applied rig needs no logins")
		return 0
	}
	var store secrets.Store
	if s, err := openStore(e, pi); err == nil {
		store = s
	} else {
		fmt.Fprintln(e.err, "note: no secret store is available:", err)
	}
	env := login.Env{
		In: e.in, Out: e.out, Getenv: e.getenv, Plat: pi, Store: store, LookPath: e.look,
		Hidden: func(prompt string) (string, error) {
			b, err := readHidden(e, prompt)
			return string(b), err
		},
		Line: func(prompt string) (string, error) {
			fmt.Fprint(e.out, prompt)
			return readLine(e.in), nil
		},
		RunCmd: func(ctx context.Context, argv []string) error {
			if e.runCmd != nil {
				return e.runCmd(ctx, argv)
			}
			return login.SystemRun(ctx, argv)
		},
		Client: &login.Client{Show: func(f string, a ...any) { fmt.Fprintf(e.out, f, a...) }, Open: openBrowser(e)},
	}
	results := login.RunAll(context.Background(), env, reqs)
	failed := 0
	fmt.Fprintln(e.out)
	for _, r := range results {
		mark := "ok "
		if !r.OK {
			mark, failed = "x  ", failed+1
		}
		fmt.Fprintf(e.out, "%s %-14s %s\n", mark, r.Provider, r.Note)
	}
	if failed > 0 {
		fmt.Fprintf(e.out, "\n%d login(s) not done; run `rigfile logins` again when ready.\n", failed)
		return 1
	}
	return 0
}

// openBrowser returns the function that opens a URL in the user's browser, or nil when there is no known way to
// (the flow then prints the address instead). A test can replace it through env.openURL.
func openBrowser(e env) func(string) error {
	if e.openURL != nil {
		return e.openURL
	}
	return func(u string) error {
		pi, err := platformInfo(e)
		if err != nil {
			return err
		}
		if login.Headless(pi, e.getenv) {
			return fmt.Errorf("no browser")
		}
		var name string
		var args []string
		switch pi.OS {
		case "macos":
			name, args = "open", []string{u}
		case "windows":
			name, args = "rundll32", []string{"url.dll,FileProtocolHandler", u}
		default:
			name, args = "xdg-open", []string{u}
		}
		if _, err := exec.LookPath(name); err != nil {
			return err
		}
		return exec.Command(name, args...).Start()
	}
}
