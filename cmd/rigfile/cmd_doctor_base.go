package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/basecheck"
	"github.com/digitaldreamer3462/rigfile/internal/gitmod"
	"github.com/digitaldreamer3462/rigfile/internal/jsonedit"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// doctorBase runs the base-secure integrity checks (plan §8.4) and reports whether anything drifted.
func doctorBase(e env, pi *platform.Info, st *state.State, add func(checkLevel, string, string, ...any)) (drifted bool) {
	claudeDir, err := claudecode.ClaudeDirFor(pi, e.getenv)
	if err != nil {
		add(lvFail, "base-secure", "%v", err)
		return true
	}
	bin, _ := e.look("rigfile")
	res, err := basecheck.Check(basecheck.Options{Plat: pi, Getenv: e.getenv, ClaudeDir: claudeDir, State: st, Rigfile: bin, Sandbox: st.Prefs.Sandbox})
	if err != nil {
		add(lvFail, "base-secure", "cannot verify: %v", err)
		return true
	}
	report := func(name string, applied bool, lines []string, okMsg string) {
		switch {
		case !applied:
			add(lvWarn, name, "not applied on this machine yet: run `rigfile apply <rig-dir>`")
		case len(lines) == 0:
			add(lvOK, name, "%s", okMsg)
		default:
			drifted = true
			shown := lines
			if len(shown) > 6 {
				shown = append(shown[:6:6], fmt.Sprintf("... and %d more line(s)", len(lines)-6))
			}
			add(lvFail, name, "drift: %s. Fix: `rigfile doctor --fix` (re-applies your rig; you review the plan)", strings.Join(shown, " | "))
		}
	}
	report("base-secure", res.ClaudeApplied, res.Claude, "deny/ask rules, guard/write-guard/redact hooks, settings and the security-baseline instructions are in place")
	if st.Targets[gitmod.Target] != nil || res.GitApplied {
		report("base-secure git", res.GitApplied, res.Git, "hooks directory, core.hooksPath block and global gitignore are in place")
	}

	// hooks can be switched off by the user or by a project: say so
	settings, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if v, ok := jsonedit.ReadValueRaw(settings, []string{"disableAllHooks"}); ok && v == "true" {
		drifted = true
		add(lvFail, "hooks enabled", "`disableAllHooks` is true in %s: none of base-secure's hooks run (the deny rules still apply)", filepath.Join(claudeDir, "settings.json"))
	}
	if st.Prefs.Sandbox {
		if v, ok := jsonedit.ReadValueRaw(settings, []string{"sandbox", "enabled"}); !ok || v != "true" {
			add(lvFail, "sandbox", "you opted in (--sandbox) but sandbox.enabled is not true in settings.json")
			drifted = true
		} else if pi.OS == platform.Linux && (!have(e, "bwrap") || !have(e, "socat")) {
			add(lvWarn, "sandbox", "enabled in settings, but bubblewrap/socat are missing, so Claude Code runs commands UNSANDBOXED: sudo apt-get install bubblewrap socat (or dnf)")
		} else {
			add(lvOK, "sandbox", "enabled (dependencies present)")
		}
	}

	// git: the hooks the machine really uses
	if gts := st.Targets[gitmod.Target]; gts != nil && len(gts.Items) > 0 {
		paths, perr := gitmod.Locate(gitmod.Options{Plat: pi, Getenv: e.getenv})
		if perr == nil {
			if rbin, _, ok := basecheck.RecordedRigfile(paths.HooksDir); ok {
				if _, err := os.Stat(rbin); err != nil {
					if p, lerr := e.look("rigfile"); lerr != nil {
						add(lvFail, "git hooks binary", "the hooks call %s, which is gone, and `rigfile` is not on PATH: commits are NOT being scanned (they print a warning). Run `rigfile apply`", rbin)
						drifted = true
					} else {
						add(lvWarn, "git hooks binary", "the hooks call %s (missing); they fall back to %s. Run `rigfile apply` to refresh them", rbin, p)
					}
				}
			}
			if eff := effectiveHooksPath(e); eff != "" && filepath.Clean(eff) != filepath.Clean(paths.HooksDir) {
				add(lvWarn, "git in this dir", "the repository here overrides core.hooksPath (%s): Rigfile's hooks do NOT run in it", eff)
			}
		}
	}

	// the scanner itself
	if rs, err := scan.DefaultRuleset(); err != nil {
		add(lvFail, "scanner", "%v", err)
		drifted = true
	} else {
		add(lvOK, "scanner", "%d rules (gitleaks %s + Rigfile additions)", rs.NumRules(), scan.RulesVersion())
	}
	return drifted
}

func have(e env, cmd string) bool { _, err := e.look(cmd); return err == nil }
