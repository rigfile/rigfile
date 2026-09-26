package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/state"
	"github.com/digitaldreamer3462/rigfile/internal/targets"
)

// fakeTarget proves the framework with a second, trivial adapter: it is "installed" when ~/.fake exists and
// writes one file there.
func fakeTarget() targets.Target {
	return targets.Target{
		Name: "windsurf", Title: "Fake Tool",
		Detect: func(c targets.Ctx) targets.Detection {
			h, _ := c.Plat.Home()
			if _, err := os.Stat(filepath.Join(h, ".fake")); err == nil {
				return targets.Detection{Installed: true, Why: "~/.fake exists"}
			}
			return targets.Detection{Why: "no ~/.fake"}
		},
		Plan: func(c targets.Ctx, proj *merge.Projection) (*engine.Plan, error) {
			h, _ := c.Plat.Home()
			path := filepath.Join(h, ".fake", "conf")
			body := []byte("rig configured for " + proj.Target + "\n")
			op := engine.Op{Category: "setting", Key: "conf", Symbol: engine.New, Summary: "~/.fake/conf",
				Items: []state.Item{{Category: "setting", Key: "conf", Kind: state.KindFile, Path: path, Hash: hashing.Bytes(body)}},
				Do:    func(x *engine.Exec) error { _, err := x.W.WriteFile(path, body); return err }}
			if cur, err := os.ReadFile(path); err == nil && string(cur) == string(body) {
				op.Symbol, op.Summary, op.Do = engine.Unchanged, op.Summary+"   (up to date)", nil
			}
			return &engine.Plan{Target: "Fake Tool", Ops: []engine.Op{op}}, nil
		},
	}
}

func withFake(t *testing.T) {
	restore := targets.Reset(append(targets.All(), fakeTarget())...)
	t.Cleanup(restore)
}

func TestSeveralTargetsShareOnePlanScreenStateLockAndRollback(t *testing.T) {
	withFake(t)
	m := newMachine(t)
	rig := plainRig(t, "")

	// not detected: reported, not configured
	r := m.run("", "plan", rig, "--no-git")
	if r.code != 0 || !strings.Contains(r.out, "NOT CONFIGURED  windsurf: not detected") || strings.Contains(r.out, "Fake Tool") {
		t.Fatalf("%+v", r)
	}
	// detected: a second section on the same screen
	_ = os.MkdirAll(filepath.Join(m.home, ".fake"), 0o755)
	r = m.run("", "plan", rig, "--no-git")
	if !strings.Contains(r.out, "Targets: claude-code, windsurf") || !strings.Contains(r.out, "Fake Tool") || !strings.Contains(r.out, "~/.fake/conf") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 || !strings.Contains(r.out, "applied") {
		t.Fatalf("%+v", r)
	}
	if got := string(mustRead(t, filepath.Join(m.home, ".fake", "conf"))); got != "rig configured for windsurf\n" {
		t.Fatalf("each target is merged and projected for ITSELF: %q", got)
	}
	// one lockfile holds a merged hash per target
	lockTxt := string(mustRead(t, filepath.Join(rig, "rigfile.lock")))
	if !strings.Contains(lockTxt, "claude-code") || !strings.Contains(lockTxt, "windsurf") {
		t.Fatalf("lock must pin every target:\n%s", lockTxt)
	}
	// state, diff and doctor see both targets
	stTxt := string(mustRead(t, filepath.Join(m.home, ".rigfile", "state.json")))
	if !strings.Contains(stTxt, `"windsurf"`) || !strings.Contains(stTxt, `"claude-code"`) {
		t.Fatalf("%s", stTxt)
	}
	if r := m.run("", "diff"); r.code != 0 || !strings.Contains(r.out, "Fake Tool") || !strings.Contains(r.out, "Claude Code") || !strings.Contains(r.out, "no drift") {
		t.Fatalf("%+v", r)
	}
	_ = os.WriteFile(filepath.Join(m.home, ".fake", "conf"), []byte("edited\n"), 0o644)
	if r := m.run("", "diff"); r.code != 1 || !strings.Contains(r.out, "✘") {
		t.Fatalf("drift in the second target must show: %+v", r)
	}
	if r := m.run("", "doctor"); r.code != 1 || !strings.Contains(r.out, "✘ drift") {
		t.Fatalf("%+v", r)
	}
	// one rollback undoes both
	if r := m.run("", "rollback", "--force"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".fake", "conf")); !os.IsNotExist(err) {
		t.Fatal("rollback must undo every target of the run")
	}
}

func TestTargetSelectionRules(t *testing.T) {
	withFake(t)
	m := newMachine(t)

	// --target forces a target that is not detected
	rig := plainRig(t, "")
	r := m.run("", "plan", rig, "--no-git", "--target", "windsurf")
	if r.code != 0 || !strings.Contains(r.out, "Fake Tool") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "plan", rig, "--no-git", "--target", "nope"); r.code != 1 || !strings.Contains(r.err, `unknown target "nope"`) {
		t.Fatalf("%+v", r)
	}
	// the rig can name a target (always configured) ...
	rig = plainRig(t, "targets:\n  include: [claude-code, windsurf]\n")
	if r := m.run("", "plan", rig, "--no-git"); !strings.Contains(r.out, "Fake Tool") {
		t.Fatalf("%+v", r)
	}
	// ... restrict the set (include is a whitelist, even for Claude Code) ...
	rig = plainRig(t, "targets:\n  include: [windsurf]\n")
	r = m.run("", "plan", rig, "--no-git")
	if !strings.Contains(r.out, "Targets: windsurf") && !strings.Contains(r.out, "Target: windsurf") || !strings.Contains(r.out, "claude-code: not in the rig's targets.include") {
		t.Fatalf("%+v", r)
	}
	// ... or exclude one even when it is detected
	_ = os.MkdirAll(filepath.Join(m.home, ".fake"), 0o755)
	rig = plainRig(t, "targets:\n  exclude: [windsurf]\n")
	r = m.run("", "plan", rig, "--no-git")
	if strings.Contains(r.out, "Fake Tool") || !strings.Contains(r.out, "windsurf: excluded by the rig") {
		t.Fatalf("%+v", r)
	}
	// nothing left to configure is an error, not an empty plan
	rig = plainRig(t, "targets:\n  include: [claude-code]\n  exclude: [claude-code]\n")
	if r := m.run("", "plan", rig, "--no-git"); r.code != 1 || !strings.Contains(r.err, "no target is selected") {
		t.Fatalf("%+v", r)
	}
}
