package codex

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/rigfile/rigfile/internal/adapters/adaptertest"
	"github.com/rigfile/rigfile/internal/capture"
	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/state"
)

func build(t *testing.T, r *adaptertest.Rig, goos string, st *state.State, mod func(*Env)) (*engine.Plan, error) {
	t.Helper()
	pi := r.Plat(goos)
	env := Env{Plat: pi, CodexDir: filepath.Join(r.Home, ".codex")}
	if st != nil {
		env.State = st.Targets[StateTarget]
	}
	if mod != nil {
		mod(&env)
	}
	return Build(env, r.Projection(StateTarget, pi))
}

func TestGoldenFilesAreIdenticalOnEveryOS(t *testing.T) {
	var first map[string]string
	for _, goos := range adaptertest.OSes {
		t.Run(goos, func(t *testing.T) {
			r := adaptertest.New(t, nil)
			p, err := build(t, r, goos, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			st := state.New()
			r.Apply(p, st, StateTarget)
			got := r.Tree(".codex", ".agents")
			adaptertest.Golden(t, "codex", got)
			// with base-secure's mapping on: sandbox/approval defaults at the top plus command rules
			r2 := adaptertest.New(t, nil)
			p2, err := build(t, r2, goos, nil, func(e *Env) { e.BaseSecure = true })
			if err != nil {
				t.Fatal(err)
			}
			r2.Apply(p2, state.New(), StateTarget)
			adaptertest.Golden(t, "codex-basesecure", r2.Tree(".codex", ".agents"))
			if first == nil {
				first = got
			} else if len(first) != len(got) {
				t.Fatalf("OS-dependent output")
			}
		})
	}
}

func TestSecretsNeverReachTheConfigFile(t *testing.T) {
	r := adaptertest.New(t, nil)
	p, _ := build(t, r, "linux", nil, nil)
	r.Apply(p, state.New(), StateTarget)
	cfg := r.Read(".codex/config.toml")
	if !strings.Contains(cfg, `command = 'rigfile'`) && !strings.Contains(cfg, `command = "rigfile"`) {
		t.Fatalf("stdio servers go through the exec shim:\n%s", cfg)
	}
	if !strings.Contains(cfg, "ALPACA_API_KEY=alpaca/api_key") || strings.Contains(cfg, "secret://") {
		t.Fatalf("secret refs are passed as --secret ENV=ref, never as values:\n%s", cfg)
	}
	if strings.Contains(cfg, "mcp.example.test") == false {
		t.Fatalf("the remote (oauth) server without secrets is written:\n%s", cfg)
	}
}

func TestUserTomlIsPreservedAndConflictsAreRefused(t *testing.T) {
	r := adaptertest.New(t, nil)
	user := "# my codex config\nmodel = \"gpt-x\"   # keep me\n\n[mcp_servers.alpaca]\ncommand = \"mine\"\n"
	r.Put(r.Home, ".codex/config.toml", user, 0o644)
	p, err := build(t, r, "linux", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	op := findOp(p, "mcp", "config.toml")
	if op.Symbol != engine.Update {
		t.Fatalf("docs server is still added: %+v", op)
	}
	joined := strings.Join(op.Detail, "\n")
	if !strings.Contains(joined, `! "alpaca" clashes with something you already defined`) || !strings.Contains(joined, `+ "docs"`) {
		t.Fatalf("a server the user already defined is refused, the rest is added:\n%s", joined)
	}
	r.Apply(p, state.New(), StateTarget)
	got := r.Read(".codex/config.toml")
	if !strings.HasPrefix(got, user) {
		t.Fatalf("the user's own text must be untouched and first:\n%s", got)
	}
	if strings.Count(got, "[mcp_servers.alpaca]") != 1 {
		t.Fatalf("no duplicate table may be written:\n%s", got)
	}
}

func TestIdempotentDriftAndOrphanRemoval(t *testing.T) {
	r := adaptertest.New(t, nil)
	st := state.New()
	p, _ := build(t, r, "macos", nil, nil)
	r.Apply(p, st, StateTarget)
	if ds := state.Check(st.Targets[StateTarget].Items, nil); !state.Clean(ds) {
		t.Fatalf("%+v", ds)
	}
	p2, _ := build(t, r, "macos", st, nil)
	if n := p2.Changes(); n != 0 {
		t.Fatalf("a second plan must change nothing, got %d:\n%s", n, render(p2))
	}
	// the rig drops a skill, an agent and a server: they are removed, user content stays
	r2 := adaptertest.New(t, map[string]string{
		"skills/pdf/SKILL.md": "\x00delete", "skills/pdf/scripts/run.sh": "\x00delete", "agents/reviewer.md": "\x00delete",
		"rigfile.yaml": strings.Replace(strings.Replace(strings.Replace(adaptertest.RigYAML, "skills:\n  - {path: skills/pdf}\n", "", 1), "agents:\n  - {path: agents/reviewer.md}\n", "", 1), "  docs:\n    transport: http\n    url: https://mcp.example.test/mcp\n    auth: oauth\n", "", 1),
	})
	r2.Home = r.Home
	p3, err := build(t, r2, "macos", st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p3.Changes() < 3 {
		t.Fatalf("expected removals:\n%s", render(p3))
	}
	r2.Apply(p3, st, StateTarget)
	if r.Read(".agents/skills/pdf/SKILL.md") != "" || r.Read(".codex/agents/reviewer.toml") != "" {
		t.Fatal("dropped items must be removed")
	}
	if strings.Contains(r.Read(".codex/config.toml"), "mcp.example.test") {
		t.Fatal("the dropped server must be removed from config.toml")
	}
}

func TestNotesForWhatCodexCannotTake(t *testing.T) {
	r := adaptertest.New(t, nil)
	r.Put(r.Home, ".codex/AGENTS.override.md", "mine\n", 0o644)
	p, _ := build(t, r, "linux", nil, nil)
	notes := strings.Join(p.Notes, "\n")
	for _, want := range []string{"hook(s) not installed for Codex", "2 file read/edit deny rule(s) NOT enforced on Codex", "AGENTS.override.md", "Codex has no per-agent tool allowlist"} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing note %q in:\n%s", want, notes)
		}
	}
}

func findOp(p *engine.Plan, category, key string) engine.Op {
	for _, o := range p.Ops {
		if o.Category == category && o.Key == key {
			return o
		}
	}
	return engine.Op{}
}

func render(p *engine.Plan) string {
	var b strings.Builder
	p.Render(&b)
	return b.String()
}

func TestBaseSecureMappingWritesDefaultsOnTopAndRulesButNeverOverridesTheUser(t *testing.T) {
	r := adaptertest.New(t, nil)
	user := "# mine\nmodel = \"gpt-x\"\n\n[profiles.fast]\nmodel = \"m\"\n"
	r.Put(r.Home, ".codex/config.toml", user, 0o644)
	st := state.New()
	p, err := build(t, r, "linux", st, func(e *Env) { e.BaseSecure = true })
	if err != nil {
		t.Fatal(err)
	}
	r.Apply(p, st, StateTarget)
	cfg := r.Read(".codex/config.toml")
	if !strings.HasPrefix(cfg, "# rigfile:begin base-secure-settings") || !strings.Contains(cfg, `approval_policy = "on-request"`) || !strings.Contains(cfg, `sandbox_mode = "workspace-write"`) || !strings.Contains(cfg, user) {
		t.Fatalf("defaults go in a region at the top, above the user's tables:\n%s", cfg)
	}
	var v map[string]any
	if err := toml.Unmarshal([]byte(cfg), &v); err != nil || v["approval_policy"] != "on-request" || v["model"] != "gpt-x" {
		t.Fatalf("valid TOML with the keys at the top level: %v %v", err, v)
	}
	rules := r.Read(".codex/rules/rigfile-base-secure.rules")
	if !strings.Contains(rules, `pattern = ["git", "push"]`) || !strings.Contains(rules, `decision = "prompt"`) {
		t.Fatalf("the rig's ask rule becomes a prompt rule:\n%s", rules)
	}
	if p2, _ := build(t, r, "linux", st, func(e *Env) { e.BaseSecure = true }); p2.Changes() != 0 {
		t.Fatalf("second plan must change nothing:\n%s", render(p2))
	}
	// the user's own settings win
	r2 := adaptertest.New(t, nil)
	r2.Put(r2.Home, ".codex/config.toml", "approval_policy = \"never\"\n", 0o644)
	p3, _ := build(t, r2, "linux", nil, func(e *Env) { e.BaseSecure = true })
	r2.Apply(p3, state.New(), StateTarget)
	got := r2.Read(".codex/config.toml")
	if strings.Contains(got, `approval_policy = "on-request"`) || !strings.Contains(got, `sandbox_mode = "workspace-write"`) || !strings.Contains(strings.Join(p3.Notes, "\n"), "left as it is") {
		t.Fatalf("%s\n%v", got, p3.Notes)
	}
}

func TestArgvPrefix(t *testing.T) {
	for in, want := range map[string]string{"git push*": "git push", "rm -rf*": "rm -rf", "env": "env", "sudo*": "sudo", "git push --force*": "git push --force"} {
		got, ok := argvPrefix(in)
		if !ok || strings.Join(got, " ") != want {
			t.Errorf("%q -> %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"git*--no-verify*", "curl*| sh*", "*", ""} {
		if _, ok := argvPrefix(in); ok {
			t.Errorf("%q must not be expressible", in)
		}
	}
}

func TestRoundTripCaptureOfWhatWasApplied(t *testing.T) {
	r := adaptertest.New(t, nil)
	st := state.New()
	p, err := build(t, r, "linux", st, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Apply(p, st, StateTarget)
	res, err := Capture(capture.Options{Dir: filepath.Join(r.Home, ".codex"), Home: r.Home, IncludeManaged: true, Name: "x/captured"}, filepath.Join(r.Home, ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(res.Manifest)
	if err != nil {
		t.Fatalf("captured manifest invalid: %v\n%s", err, res.Manifest)
	}
	adaptertest.AssertCanonicalMCP(t, m, true)
	if got := adaptertest.ReadRig(res.Files, "instructions/coding-style.md"); got != "# Style\n- be terse\n" {
		t.Fatalf("instruction text: %q", got)
	}
	if got := adaptertest.ReadRig(res.Files, "skills/pdf/scripts/run.sh"); got != adaptertest.Files["skills/pdf/scripts/run.sh"] {
		t.Fatalf("skill tree: %q", got)
	}
	if got := adaptertest.ReadRig(res.Files, "skills/ship/SKILL.md"); !strings.Contains(got, "Run the tests, then push.") {
		t.Fatalf("the command became a skill and is captured as one: %q", got)
	}
	ag := adaptertest.ReadRig(res.Files, "agents/reviewer.md")
	if !strings.Contains(ag, "description: Reviews diffs") || !strings.Contains(ag, "Be strict.") || strings.Contains(ag, "tools:") {
		t.Fatalf("agent (tools are a documented loss): %q", ag)
	}
	// without IncludeManaged, marked regions Rigfile wrote are left out (whole files such as skills carry no marker)
	res2, _ := Capture(capture.Options{Dir: filepath.Join(r.Home, ".codex"), Home: r.Home}, filepath.Join(r.Home, ".agents", "skills"))
	m2, _ := manifest.Parse(res2.Manifest)
	if len(m2.MCPServers) != 0 || len(res2.Files["instructions/coding-style.md"]) != 0 {
		t.Fatalf("managed content must not be swallowed by a plain init:\n%s", res2.Manifest)
	}
}
