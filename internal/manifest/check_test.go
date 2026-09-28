package manifest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// rig writes files (relative path -> content) into a temp dir and returns it.
func rig(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const skillMD = "---\nname: demo\ndescription: A demo skill\n---\nbody\n"

const base = "apiVersion: rigfile.dev/v1\nname: adams/demo\nversion: 1.0.0\n"

func load(t *testing.T, files map[string]string) *Loaded {
	t.Helper()
	l, err := Load(rig(t, files))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return l
}

func findings(ps []Problem, level Level, sub string) bool {
	for _, p := range ps {
		if p.Level == level && strings.Contains(p.String(), sub) {
			return true
		}
	}
	return false
}

func TestLoadDecodesEverything(t *testing.T) {
	l, err := Load("../../testdata/fixtures")
	if err == nil {
		t.Fatal("the fixtures dir has no rigfile.yaml; want an error")
	}
	dir := t.TempDir()
	b, _ := os.ReadFile("../../testdata/fixtures/plan-example.rigfile.yaml")
	_ = os.WriteFile(filepath.Join(dir, "rigfile.yaml"), b, 0o644)
	l, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := l.M
	if m.Name != "adams/data-science" || len(m.Skills) != 2 || m.Skills[0].Key() != "job-pipeline" || m.Skills[1].Key() != "pdf" {
		t.Fatalf("skills/keys: %+v", m.Skills)
	}
	if m.Agents[0].Key() != "code-reviewer" || m.Commands[0].Key() != "ship" || m.Instructions[0].Key() != "user/coding-style" {
		t.Fatal("keys wrong")
	}
	alp := m.MCPServers["alpaca"]
	if alp.Command != "npx" || alp.Env["ALPACA_API_KEY"] != "secret://alpaca/api_key" || alp.IsRemote() {
		t.Fatalf("alpaca: %+v", alp)
	}
	if !m.MCPServers["github"].IsRemote() || m.MCPServers["github"].Auth != "oauth" {
		t.Fatal("github server wrong")
	}
	h := m.Hooks[0]
	if name, ok := Builtin(h.Run.For("linux")); !ok || name != "block-large-files" || h.Match.Tool != "bash" {
		t.Fatalf("hook 0: %+v", h)
	}
	if m.Hooks[1].Run.For("windows") != "hooks/notify.ps1" || m.Hooks[1].Run.For("macos") != "hooks/notify.sh" {
		t.Fatalf("per-OS run: %+v", m.Hooks[1].Run)
	}
	if len(m.Permissions.Deny) != 3 || m.Permissions.Ask[0].Bash != "git push*" {
		t.Fatalf("permissions: %+v", m.Permissions)
	}
	if m.Secrets["alpaca/api_key"].Hosts[0] != "*.alpaca.markets" || m.Tools.Common[0] != "gh" || m.Tools.Linux.Apt[0] != "build-essential" {
		t.Fatal("secrets/tools wrong")
	}
	if m.Models["local-coder"].Variants[0].Revision == "" || m.Gateways["anthropic-bridge"].Routes["local-coder"] == "" || m.Routing.FallbackOnLimit != "local-coder" {
		t.Fatal("models/gateways/routing wrong")
	}
	if len(l.Hash) != 64 {
		t.Fatal("hash missing")
	}
}

func TestLoadRejectsInvalidAndMissing(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no rigfile.yaml") {
		t.Fatalf("missing: %v", err)
	}
	dir := rig(t, map[string]string{"rigfile.yaml": base + "surprise: 1\n"})
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid: %v", err)
	}
}

func TestCheckMissingFilesAndSkillFormat(t *testing.T) {
	l := load(t, map[string]string{
		"rigfile.yaml": base + `instructions:
  - {id: a, file: instructions/missing.md}
skills:
  - {path: skills/good}
  - {path: skills/nomd}
  - {path: skills/badfm}
  - {path: skills/nofm}
agents:
  - {path: agents/nope.md}
`,
		"skills/good/SKILL.md":  skillMD,
		"skills/nomd/README.md": "x",
		"skills/badfm/SKILL.md": "---\nname: only-name\n---\nx",
		"skills/nofm/SKILL.md":  "no frontmatter",
	})
	ps := Check(l)
	for _, want := range []string{"instructions[0].file: instructions/missing.md does not exist", "no SKILL.md", "needs both name and description", "must start with YAML frontmatter", "agents[0].path: agents/nope.md does not exist"} {
		if !findings(ps, Error, want) {
			t.Errorf("missing finding %q in:\n%v", want, ps)
		}
	}
	if findings(ps, Error, "skills[0]") {
		t.Errorf("the good skill was flagged: %v", ps)
	}
}

func TestCheckSymlinkEscapeIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(skillMD), 0o644)
	dir := rig(t, map[string]string{"rigfile.yaml": base + "skills:\n  - {path: skills/evil}\n"})
	_ = os.MkdirAll(filepath.Join(dir, "skills"), 0o755)
	if err := os.Symlink(outside, filepath.Join(dir, "skills", "evil")); err != nil {
		t.Fatal(err)
	}
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ps := Check(l); !findings(ps, Error, "outside the rig directory") {
		t.Fatalf("a symlink out of the rig must be an error: %v", ps)
	}
}

func TestCheckDuplicatesAndCaseCollisions(t *testing.T) {
	l := load(t, map[string]string{
		"rigfile.yaml": base + `skills:
  - {path: skills/a}
  - {path: other/a}
  - {path: skills/B}
  - {path: skills/b}
commands:
  - {path: commands/a.md}
hooks:
  - {id: h, event: stop, run: builtin:x}
  - {id: h, event: stop, run: builtin:y}
`,
		"skills/a/SKILL.md": skillMD, "other/a/SKILL.md": skillMD, "skills/B/SKILL.md": skillMD, "skills/b/SKILL.md": skillMD,
		"commands/a.md": "x",
	})
	ps := Check(l)
	for _, want := range []string{`skills[1]: duplicate id "a"`, "differs from", "collide on case-insensitive", `hooks[1]: duplicate id "h"`, `same name as a skill`} {
		if !findings(ps, Error, want) && !findings(ps, Warn, want) {
			t.Errorf("missing %q in:\n%v", want, ps)
		}
	}
}

func TestCheckHookScriptsPerOS(t *testing.T) {
	l := load(t, map[string]string{
		"rigfile.yaml": base + `hooks:
  - id: n
    event: stop
    run: {macos: hooks/n.sh, windows: hooks/n.ps1}
  - {id: b, event: stop, run: "builtin:ok"}
`,
		"hooks/n.sh": "#!/bin/sh\n",
	})
	ps := Check(l)
	if !findings(ps, Error, "hooks[0].run(windows): hooks/n.ps1 does not exist") || findings(ps, Error, "run(macos)") || findings(ps, Error, "hooks[1]") {
		t.Fatalf("%v", ps)
	}
}

func TestPinProblem(t *testing.T) {
	cases := []struct {
		cmd  string
		args []string
		bad  bool
	}{
		{"npx", []string{"-y", "pkg@1.2.3"}, false},
		{"npx", []string{"-y", "@scope/pkg@1.2.3"}, false},
		{"npx", []string{"-y", "pkg"}, true},
		{"npx", []string{"-y", "pkg@latest"}, true},
		{"npx", []string{"-y", "@scope/pkg"}, true},
		{"npx", []string{"-y", "pkg@^1.2.0"}, true},
		{"npx.cmd", []string{"pkg@2.0.0"}, false},
		{"uvx", []string{"tool==0.5.1"}, false},
		{"uvx", []string{"tool"}, true},
		{"pipx", []string{"run", "tool==1.0.0"}, false},
		{"pipx", []string{"run", "tool"}, true},
		{"rigfile", []string{"exec", "--secret", "A=b/c", "--", "npx", "-y", "pkg@1.2.3"}, false},
		{"rigfile", []string{"exec", "--", "npx", "-y", "pkg"}, true},
		{"/opt/tools/my-server", nil, true}, // unknown launcher: cannot verify
	}
	for _, c := range cases {
		got := pinProblem(c.cmd, c.args) != ""
		if got != c.bad {
			t.Errorf("%s %v: problem=%v want %v (%q)", c.cmd, c.args, got, c.bad, pinProblem(c.cmd, c.args))
		}
	}
}

func TestCheckSecretsAndCrossReferences(t *testing.T) {
	l := load(t, map[string]string{"rigfile.yaml": base + `mcp_servers:
  s:
    command: npx
    args: ["-y", "pkg@1.0.0"]
    env: {A_KEY: "secret://undeclared/one", B_KEY: "secret://x/bound"}
    network: {allow: ["api.other.com"]}
secrets:
  x/bound: {description: d, hosts: ["*.example.com"]}
routing: {fallback_on_limit: ghost, local_for: ["subagent:nobody"]}
gateways:
  g: {listen: "127.0.0.1:4000", routes: {phantom: "http://127.0.0.1:8080/v1"}}
`})
	ps := Check(l)
	for _, want := range []string{"secret://undeclared/one which is not declared", "bound to [*.example.com]", "routing.fallback_on_limit", "gateways.g.routes", `subagent "nobody"`} {
		if !findings(ps, Error, want) && !findings(ps, Warn, want) {
			t.Errorf("missing %q in:\n%v", want, ps)
		}
	}
	if !HasErrors(ps) {
		t.Fatal("cross-reference failures are errors")
	}
	// errors sort before warnings
	seenWarn := false
	for _, p := range ps {
		if p.Level == Warn {
			seenWarn = true
		} else if seenWarn {
			t.Fatalf("error listed after a warning: %v", ps)
		}
	}
}

func TestCheckCleanRig(t *testing.T) {
	l := load(t, map[string]string{
		"rigfile.yaml":       base + "skills:\n  - {path: skills/ok}\nmcp_servers:\n  s: {command: npx, args: [\"-y\", \"pkg@1.0.0\"]}\n",
		"skills/ok/SKILL.md": skillMD,
	})
	if ps := Check(l); len(ps) != 0 {
		t.Fatalf("expected no findings, got %v", ps)
	}
}

func TestHostCovers(t *testing.T) {
	yes := [][2]string{{"a.com", "a.com"}, {"*.a.com", "x.a.com"}, {"*.a.com", "*.x.a.com"}}
	no := [][2]string{{"a.com", "b.com"}, {"*.a.com", "a.com"}, {"*.a.com", "*.a.com.evil.com"}, {"*.a.com", "nota.com"}, {"x.a.com", "*.a.com"}}
	for _, c := range yes {
		if !HostCovers(c[0], c[1]) {
			t.Errorf("%s should cover %s", c[0], c[1])
		}
	}
	for _, c := range no {
		if c[0] != c[1] && HostCovers(c[0], c[1]) {
			t.Errorf("%s should NOT cover %s", c[0], c[1])
		}
	}
}

func TestWindowsReservedNames(t *testing.T) {
	for name, want := range map[string]bool{"con": true, "NUL": true, "com1": true, "Lpt9": true, "aux.md": true, "trail.": true,
		"com0": false, "console": false, "pdf": false, "com10": false} {
		if got := windowsReserved(name); got != want {
			t.Errorf("windowsReserved(%q) = %v", name, got)
		}
	}
}

func TestPrivatePathsCannotAlsoBeShipped(t *testing.T) {
	l := load(t, map[string]string{
		"rigfile.yaml": base + `instructions:
  - {id: mine, file: memory/notes.md}
  - {id: ok, file: instructions/style.md}
skills:
  - {path: memory/skills/pdf}
private:
  - memory/
`,
		"memory/notes.md":            "personal",
		"instructions/style.md":      "public",
		"memory/skills/pdf/SKILL.md": skillMD,
	})
	ps := Check(l)
	for _, want := range []string{"instructions[0].file: memory/notes.md is listed under `private:`", "skills[0].path: memory/skills/pdf is listed under `private:`"} {
		if !findings(ps, Error, want) {
			t.Errorf("missing %q in\n%v", want, ps)
		}
	}
	if findings(ps, Error, "instructions[1]") {
		t.Errorf("a public instruction was flagged: %v", ps)
	}
	// private paths that nothing ships are fine
	l = load(t, map[string]string{"rigfile.yaml": base + "instructions:\n  - {id: ok, file: instructions/style.md}\nprivate:\n  - memory/\n", "instructions/style.md": "x"})
	for _, p := range Check(l) {
		if p.Level == Error && strings.Contains(p.Msg, "private") {
			t.Fatalf("%v", p)
		}
	}
}
