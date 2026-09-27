package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/rigfile/rigfile/internal/localui"
	"github.com/rigfile/rigfile/internal/secrets"
)

// cmdUI implements `ui [<rig-dir>]`: the plan and the checklist of what the rig still needs, in a browser page on this
// computer only (docs/local-ui.md).
func cmdUI(args []string, e env) int {
	var f rigFlags
	fs := rigFlagSet("ui", e, &f, true)
	noOpen := fs.Bool("no-open", false, "print the address instead of opening the browser")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 1 {
		fmt.Fprintln(e.err, "usage: rigfile ui [<rig-dir>] [flags]")
		return 2
	}
	rigDir := "."
	if len(pos) == 1 {
		rigDir = pos[0]
	}
	p, code := prepare(e, rigDir, f)
	if p == nil {
		return code
	}
	title := p.Header()
	if i := strings.IndexByte(title, '\n'); i > 0 {
		title = title[:i]
	}
	pi := p.Plat
	p.Close()

	// each action runs the same code as the command line, into a buffer, with no prompts
	capture := func(verb string, yes bool) (string, error) {
		var out, errb bytes.Buffer
		e2 := e
		e2.out, e2.err, e2.in = &out, &errb, strings.NewReader("")
		ff := f
		ff.yes = yes
		rc := planApply(verb, rigDir, ff, e2)
		text := out.String()
		if errb.Len() > 0 {
			text += "\n" + errb.String()
		}
		if rc != 0 {
			return text, fmt.Errorf("rigfile %s finished with code %d", verb, rc)
		}
		return text, nil
	}
	var storeOnce sync.Once
	var store secrets.Store
	var storeErr error
	openOnce := func() (secrets.Store, error) {
		storeOnce.Do(func() { store, storeErr = openStore(e, pi) })
		return store, storeErr
	}
	srv := &localui.Server{B: localui.Backend{
		Title: title,
		Plan:  func() (string, error) { return capture("plan", false) },
		Needs: func() ([]localui.Need, error) {
			pp, code := prepare(quiet(e), rigDir, f)
			if pp == nil {
				return nil, fmt.Errorf("prepare failed (%d)", code)
			}
			defer pp.Close()
			var out []localui.Need
			for _, n := range pp.Needs() {
				need := localui.Need{Kind: n.Kind, Ref: n.Ref, Description: n.Description, ObtainURL: n.ObtainURL, Method: n.Method}
				if n.Kind == "secret" {
					if st, err := openOnce(); err == nil {
						if _, gerr := st.Get(n.Ref); gerr == nil {
							need.Status = "set"
						} else if secrets.IsNotFound(gerr) {
							need.Status = "not set"
						}
					}
				}
				out = append(out, need)
			}
			return out, nil
		},
		SetSecret: func(ref string, v []byte) error {
			st, err := openOnce()
			if err != nil {
				return errors.New("the secret store is not available: " + err.Error())
			}
			return st.Set(ref, v)
		},
		Apply: func() (string, error) { return capture("apply", true) },
	}}
	url, err := srv.Start()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintf(e.out, "Rigfile is at %s\n(Only this computer can reach it. Press Ctrl-C, or use the Stop button, to close it.)\n", url)
	if !*noOpen {
		if open := openBrowser(e); open != nil {
			if err := open(url); err != nil {
				fmt.Fprintln(e.out, "Open that address in your browser.")
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.Wait(ctx)
	fmt.Fprintln(e.out, "Stopped.")
	return 0
}

// quiet is an env whose output is discarded (a background refresh of the checklist must not print).
func quiet(e env) env {
	e.out, e.err, e.in = io.Discard, io.Discard, strings.NewReader("")
	return e
}
