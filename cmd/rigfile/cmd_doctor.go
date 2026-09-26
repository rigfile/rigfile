package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/gitmod"
	"github.com/digitaldreamer3462/rigfile/internal/secrets"
	"github.com/digitaldreamer3462/rigfile/internal/session"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

type checkLevel string

const (
	lvOK   checkLevel = "ok"
	lvWarn checkLevel = "warn"
	lvFail checkLevel = "fail"
)

type check struct {
	level  checkLevel
	name   string
	detail string
}

func (c check) mark() string {
	switch c.level {
	case lvOK:
		return "✔"
	case lvWarn:
		return "⚠"
	}
	return "✘"
}

// cmdDoctor health-checks everything Rigfile depends on. Exit 1 if anything is red (✘).
func cmdDoctor(args []string, e env) int {
	if len(args) != 0 {
		fmt.Fprintln(e.err, "usage: rigfile doctor")
		return 2
	}
	var cs []check
	add := func(l checkLevel, name, format string, a ...any) {
		cs = append(cs, check{l, name, fmt.Sprintf(format, a...)})
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	wsl := ""
	if pi.WSL {
		wsl = " (WSL)"
	}
	add(lvOK, "platform", "%s/%s%s", pi.OS, pi.Arch, wsl)

	// rigfile must be on PATH: MCP entries and hooks call it by name
	if p, err := e.look("rigfile"); err == nil {
		add(lvOK, "rigfile on PATH", "%s", p)
	} else {
		add(lvFail, "rigfile on PATH", "not found; MCP servers and hooks call `rigfile`, so Claude Code cannot launch them")
	}

	// vendor CLI
	if p, err := e.look("claude"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, verr := exec.CommandContext(ctx, p, "--version").Output()
		cancel()
		if verr == nil {
			add(lvOK, "claude CLI", "%s (%s)", strings.TrimSpace(string(out)), p)
		} else {
			add(lvOK, "claude CLI", "%s", p)
		}
	} else if e.mcp != nil && e.mcp.Available() {
		add(lvOK, "claude CLI", "available")
	} else {
		add(lvWarn, "claude CLI", "not found; Rigfile cannot register or check MCP servers")
	}

	// applied state and drift
	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	st, err := state.Load(sd)
	if err != nil {
		add(lvFail, "state", "%v", err)
		return printChecks(e, cs)
	}
	if gts := st.Targets[gitmod.Target]; gts != nil && len(gts.Items) > 0 {
		var bad []string
		for _, d := range state.Check(gts.Items, nil) {
			if d.Status != state.OK {
				bad = append(bad, fmt.Sprintf("%s (%s)", d.Item.Key, d.Status))
			}
		}
		if len(bad) == 0 {
			add(lvOK, "git protections", "hooks, core.hooksPath and the global gitignore match what was applied")
		} else {
			add(lvFail, "git protections", "differ from what was applied: %s (see `rigfile diff`)", strings.Join(bad, "; "))
		}
	}
	ts := st.Targets[session.Target]
	if ts == nil || len(ts.Items) == 0 {
		add(lvWarn, "applied rig", "nothing applied yet: run `rigfile apply <rig-dir>`")
	} else {
		add(lvOK, "applied rig", "%s@%s   %s", ts.Rig.Name, ts.Rig.Version, ts.AppliedAt)
		ds := state.Check(ts.Items, map[string]state.Probe{state.KindMCP: claudecode.MCPProbe(mcpClient(e))})
		var bad []string
		for _, d := range ds {
			if d.Status != state.OK {
				bad = append(bad, fmt.Sprintf("%s %s (%s)", d.Item.Category, d.Item.Key, d.Status))
			}
		}
		switch {
		case len(bad) == 0:
			add(lvOK, "drift", "%d item(s) match what was applied", len(ds))
		default:
			shown := bad
			if len(shown) > 8 {
				shown = append(shown[:8:8], fmt.Sprintf("... and %d more", len(bad)-8))
			}
			add(lvFail, "drift", "%d of %d item(s) differ: %s (see `rigfile diff`)", len(bad), len(ds), strings.Join(shown, "; "))
		}

		// secrets the rig needs
		refs := secretRefs(ts.Needs)
		if len(refs) > 0 {
			status := secretStatuses(e, pi, refs)
			switch {
			case status == nil:
				add(lvWarn, "secrets", "%d required; status not checked (encrypted-file backend needs a passphrase): rigfile secrets status <ref>", len(refs))
			default:
				var missing []string
				for _, r := range refs {
					if status[r] != "set" {
						missing = append(missing, r)
					}
				}
				if len(missing) == 0 {
					add(lvOK, "secrets", "all %d required secret(s) are set", len(refs))
				} else {
					add(lvFail, "secrets", "not set: %s (run `rigfile secrets set <ref>`)", strings.Join(missing, ", "))
				}
			}
		}
		for _, n := range ts.Needs {
			if n.Kind == "login" {
				add(lvWarn, "login", "%s: complete this sign-in yourself (%s)", n.Ref, n.Description)
			}
		}
	}

	// secret backend quality
	switch {
	case e.keyringOff:
		add(lvWarn, "secret store", "encrypted-file backend (weaker than the OS keychain)")
	case secrets.KeyringAvailable() == nil:
		add(lvOK, "secret store", "OS keychain (%s)", pi.PreferredSecretStore())
	default:
		add(lvWarn, "secret store", "no OS keychain available; the encrypted-file fallback will be used (weaker)")
	}
	return printChecks(e, cs)
}

func printChecks(e env, cs []check) int {
	fail, warn := 0, 0
	for _, c := range cs {
		fmt.Fprintf(e.out, "%s %-16s %s\n", c.mark(), c.name, c.detail)
		switch c.level {
		case lvFail:
			fail++
		case lvWarn:
			warn++
		}
	}
	switch {
	case fail > 0:
		fmt.Fprintf(e.out, "\n%d problem(s), %d warning(s)\n", fail, warn)
		return 1
	case warn > 0:
		fmt.Fprintf(e.out, "\nOK with %d warning(s)\n", warn)
	default:
		fmt.Fprintln(e.out, "\nall good")
	}
	return 0
}
