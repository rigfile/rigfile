package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/hashing"
	"github.com/rigfile/rigfile/internal/merge"
	"github.com/rigfile/rigfile/internal/state"
	"github.com/rigfile/rigfile/internal/targets"
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
	stTxt := string(mustRead(t, filepath.Join(m.stateDir(), "state.json")))
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

func TestCodexIsConfiguredWhenDetectedAndUndoneByRollback(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)

	// no ~/.codex and no `codex` on PATH: reported, not configured
	r := m.run("", "plan", rig, "--no-git")
	if !strings.Contains(r.out, "NOT CONFIGURED  codex: not detected") {
		t.Fatalf("%+v", r)
	}
	// ~/.codex exists: Codex gets its own section
	_ = os.MkdirAll(filepath.Join(m.home, ".codex"), 0o755)
	r = m.run("", "plan", rig, "--no-git")
	for _, want := range []string{"Targets: claude-code, codex", "AGENTS.md", ".agents/skills/pdf", "config.toml", "not installed for Codex"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	cfg := string(mustRead(t, filepath.Join(m.home, ".codex", "config.toml")))
	if !strings.Contains(cfg, "[mcp_servers.alpaca]") || strings.Contains(cfg, "secret://") {
		t.Fatalf("%s", cfg)
	}
	if r := m.run("", "diff"); r.code != 0 || !strings.Contains(r.out, "Codex") || !strings.Contains(r.out, "no drift") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "rollback", "--force"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".agents", "skills", "pdf")); !os.IsNotExist(err) {
		t.Fatal("rollback must remove the Codex skill too")
	}
	// CODEX_HOME moves the config directory
	m.env["CODEX_HOME"] = filepath.Join(m.home, "elsewhere")
	_ = os.MkdirAll(m.env["CODEX_HOME"], 0o755)
	if r := m.run("", "apply", rig, "--yes", "--no-git", "--overwrite"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.env["CODEX_HOME"], "config.toml")); err != nil {
		t.Fatal("CODEX_HOME was ignored")
	}
}

func TestGeminiCliIsConfiguredWhenDetected(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	_ = os.MkdirAll(filepath.Join(m.home, ".gemini"), 0o755)
	r := m.run("", "plan", rig, "--no-git")
	for _, want := range []string{"Gemini CLI", "GEMINI.md", "settings.json", "commands/ship"} {
		if want == "commands/ship" {
			continue // the shared rig has no command; the adapter tests cover commands
		}
		if !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if s := string(mustRead(t, filepath.Join(m.home, ".gemini", "settings.json"))); !strings.Contains(s, `"alpaca"`) || strings.Contains(s, "secret://") {
		t.Fatalf("%s", s)
	}
	if r := m.run("", "diff"); r.code != 0 || !strings.Contains(r.out, "Gemini CLI") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "rollback", "--force"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".gemini", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("rollback removes the file the run created")
	}
}

func TestEveryDetectedTargetSaysHowMuchOfBaseSecureItEnforces(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	for _, d := range []string{".codex", ".gemini", ".cursor"} {
		_ = os.MkdirAll(filepath.Join(m.home, d), 0o755)
	}
	r := m.run("", "plan", rig, "--no-git")
	for _, want := range []string{"base-secure on Codex CLI: PARTLY enforced", "base-secure on Gemini CLI: instructions only, NOT enforced", "base-secure on Cursor: instructions only, NOT enforced", "Targets: claude-code, codex, cursor, gemini-cli"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	// Codex gets the real mapping from the embedded base layer: defaults, and rules for git push / --force / env
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	rules := string(mustRead(t, filepath.Join(m.home, ".codex", "rules", "rigfile-base-secure.rules")))
	for _, want := range []string{`pattern = ["git", "push", "--force"]`, `decision = "forbidden"`, `pattern = ["git", "push"]`, `decision = "prompt"`, `pattern = ["env"]`} {
		if !strings.Contains(rules, want) {
			t.Fatalf("Codex rules missing %q:\n%s", want, rules)
		}
	}
	cfg := string(mustRead(t, filepath.Join(m.home, ".codex", "config.toml")))
	if !strings.HasPrefix(cfg, "# rigfile:begin base-secure-settings") {
		t.Fatalf("%s", cfg)
	}
	// the security baseline reaches every instructions target
	for _, f := range []string{".codex/AGENTS.md", ".gemini/GEMINI.md"} {
		if !strings.Contains(string(mustRead(t, filepath.Join(m.home, f))), "Security baseline (managed by rigfile/base-secure") {
			t.Fatalf("%s lacks the security baseline", f)
		}
	}
	// a normal (no --no-git) doctor still passes with several targets applied
	if r := m.run("", "diff"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestInitFromCodexCapturesHandWrittenSetupAndSkipsSecrets(t *testing.T) {
	m := newMachine(t)
	cdir := filepath.Join(m.home, ".codex")
	_ = os.MkdirAll(cdir, 0o755)
	tok := "sk-ant-" + strings.Repeat("TEST", 6)
	_ = os.WriteFile(filepath.Join(cdir, "AGENTS.md"), []byte("# Mine\n- prefer small diffs\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cdir, "config.toml"), []byte("[mcp_servers.tools]\ncommand = \"npx\"\nargs = [\"-y\", \"tools-mcp@1.0.0\"]\n\n[mcp_servers.tools.env]\nTOOLS_API_KEY = \""+tok+"\"\n"), 0o600)
	out := filepath.Join(t.TempDir(), "rig")
	r := m.run("", "init", "--from", "codex", "--out", out, "--name", "me/codex-rig")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	yml := string(mustRead(t, filepath.Join(out, "rigfile.yaml")))
	if strings.Contains(yml, tok) || strings.Contains(r.out, tok) || !strings.Contains(yml, "secret://tools/tools_api_key") {
		t.Fatalf("the secret value must never be captured:\n%s", yml)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(out, "instructions", "agents-md.md"))), "prefer small diffs") {
		t.Fatal("AGENTS.md was not captured")
	}
	if r := m.run("", "init", "--from", "nope", "--out", filepath.Join(t.TempDir(), "x")); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestVSCodeCopilotIsProjectScopedAndNeverAutoSelectedWithoutAProject(t *testing.T) {
	m := newMachine(t)
	rig := plainRig(t, "instructions:\n  - {id: team, file: instructions/style.md, scope: project}\nmcp_servers:\n  docs:\n    transport: http\n    url: https://mcp.example.test/mcp\n")
	put(t, rig, "instructions/style.md", "be terse\n", 0o644)
	// no project: not selected, and the reason says how to select it
	r := m.run("", "plan", rig, "--no-git")
	if r.code != 0 || !strings.Contains(r.out, "vscode-copilot: not detected (project-scoped: pass --project <dir>") {
		t.Fatalf("%+v", r)
	}
	// forced without a project: only notes, nothing to write
	if r := m.run("", "plan", rig, "--no-git", "--target", "vscode-copilot"); r.code != 0 || !strings.Contains(r.out, "configured per PROJECT") {
		t.Fatalf("%+v", r)
	}
	// with a project it writes the two committed files there, and nothing under the home directory
	proj := t.TempDir()
	if r := m.run("", "apply", rig, "--yes", "--no-git", "--target", "vscode-copilot", "--project", proj); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	mcp := string(mustRead(t, filepath.Join(proj, ".vscode", "mcp.json")))
	ins := string(mustRead(t, filepath.Join(proj, ".github", "copilot-instructions.md")))
	if !strings.Contains(mcp, `"servers"`) || !strings.Contains(mcp, "mcp.example.test") || !strings.Contains(ins, "be terse") {
		t.Fatalf("%s\n%s", mcp, ins)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".vscode")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written outside the project")
	}
	// a project that already has .vscode selects the target by itself
	r = m.run("", "plan", rig, "--no-git", "--project", proj)
	if !strings.Contains(r.out, "GitHub Copilot in VS Code") {
		t.Fatalf("%s", r.out)
	}
}
