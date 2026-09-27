package gemini

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/adapters/adaptertest"
	"github.com/rigfile/rigfile/internal/capture"
	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/state"
)

func build(t *testing.T, r *adaptertest.Rig, goos string, st *state.State, mod func(*Env)) (*engine.Plan, error) {
	t.Helper()
	pi := r.Plat(goos)
	env := Env{Plat: pi, GeminiDir: filepath.Join(r.Home, ".gemini")}
	if st != nil {
		env.State = st.Targets[StateTarget]
	}
	if mod != nil {
		mod(&env)
	}
	return Build(env, r.Projection(StateTarget, pi))
}

func render(p *engine.Plan) string {
	var b strings.Builder
	p.Render(&b)
	return b.String()
}

func TestGoldenFilesAreIdenticalOnEveryOS(t *testing.T) {
	for _, goos := range adaptertest.OSes {
		t.Run(goos, func(t *testing.T) {
			r := adaptertest.New(t, nil)
			p, err := build(t, r, goos, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Apply(p, state.New(), StateTarget)
			adaptertest.Golden(t, "gemini", r.Tree(".gemini"))
		})
	}
}

func TestCommandsBecomeTomlAndSecretsStayOutOfSettings(t *testing.T) {
	r := adaptertest.New(t, map[string]string{"commands/ship.md": "---\ndescription: Ship it\n---\nRun tests for $ARGUMENTS, then push.\n"})
	p, _ := build(t, r, "linux", nil, nil)
	r.Apply(p, state.New(), StateTarget)
	cmd := r.Read(".gemini/commands/ship.toml")
	if !strings.Contains(cmd, `description = 'Ship it'`) || !strings.Contains(cmd, "{{args}}") || strings.Contains(cmd, "$ARGUMENTS") {
		t.Fatalf("%s", cmd)
	}
	settings := r.Read(".gemini/settings.json")
	if !json.Valid([]byte(settings)) || strings.Contains(settings, "secret://") || !strings.Contains(settings, "ALPACA_API_KEY=alpaca/api_key") || !strings.Contains(settings, `"httpUrl"`) {
		t.Fatalf("%s", settings)
	}
}

// TestUntrustedFolderNoteOnlyWhenThereAreServers is a regression test for a real finding (live, 2026-09-27,
// gemini-cli 0.61.0): Gemini CLI silently disables even user-scope MCP servers in a directory it does not
// trust ("gemini mcp list" showed one Rigfile wrote as "Disabled"). Rigfile cannot fix the vendor's own
// trust gate by writing a different file, so it surfaces the caveat instead -- only when it actually wrote a
// server (nothing to warn about otherwise).
func TestUntrustedFolderNoteOnlyWhenThereAreServers(t *testing.T) {
	r := adaptertest.New(t, nil) // the default fixture rig has an mcp server
	p, _ := build(t, r, "linux", nil, nil)
	if !strings.Contains(render(p), "does not trust the folder") {
		t.Fatalf("missing the untrusted-folder note:\n%s", render(p))
	}

	noMCP := adaptertest.New(t, map[string]string{"rigfile.yaml": strings.Replace(adaptertest.RigYAML, `mcp_servers:
  alpaca:
    command: npx
    args: ['-y', 'alpaca-mcp@1.4.2']
    env: {ALPACA_API_KEY: 'secret://alpaca/api_key', ALPACA_PAPER: 'true'}
  docs:
    transport: http
    url: https://mcp.example.test/mcp
    auth: oauth
`, "", 1)})
	p2, err := build(t, noMCP, "linux", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(render(p2), "does not trust the folder") {
		t.Fatalf("the note must not appear when nothing was written:\n%s", render(p2))
	}
}

func TestUserSettingsSurviveConflictsAreRefusedAndDroppedServersAreRemoved(t *testing.T) {
	r := adaptertest.New(t, nil)
	user := "{\n  \"theme\": \"dark\",\n  \"context\": {\n    \"fileName\": [\"AGENTS.md\"]\n  },\n  \"mcpServers\": {\n    \"alpaca\": {\"command\": \"mine\"}\n  }\n}\n"
	r.Put(r.Home, ".gemini/settings.json", user, 0o644)
	st := state.New()
	p, err := build(t, r, "macos", st, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.Notes, "\n") + render(p)
	if !strings.Contains(joined, `! "alpaca" already exists and is not managed by Rigfile`) || !strings.Contains(joined, "does not include GEMINI.md") {
		t.Fatalf("%s", joined)
	}
	r.Apply(p, st, StateTarget)
	got := r.Read(".gemini/settings.json")
	if !json.Valid([]byte(got)) || !strings.Contains(got, `"theme": "dark"`) || !strings.Contains(got, `"command": "mine"`) || !strings.Contains(got, `"docs"`) {
		t.Fatalf("the user's own server and settings stay; the free one is added:\n%s", got)
	}
	if ds := state.Check(st.Targets[StateTarget].Items, nil); !state.Clean(ds) {
		t.Fatalf("%+v", ds)
	}
	// second plan: nothing to do (the refused server is still refused, not re-written)
	p2, _ := build(t, r, "macos", st, nil)
	if p2.Changes() != 0 {
		t.Fatalf("%s", render(p2))
	}
	// the rig drops the docs server and changes nothing else: it is removed, the user's remain
	r2 := adaptertest.New(t, map[string]string{"rigfile.yaml": strings.Replace(adaptertest.RigYAML, "  docs:\n    transport: http\n    url: https://mcp.example.test/mcp\n    auth: oauth\n", "", 1)})
	r2.Home = r.Home
	p3, _ := build(t, r2, "macos", st, nil)
	r2.Apply(p3, st, StateTarget)
	got = r.Read(".gemini/settings.json")
	if strings.Contains(got, `"docs"`) || !strings.Contains(got, `"command": "mine"`) || !json.Valid([]byte(got)) {
		t.Fatalf("%s", got)
	}
	// the server comes back with the full rig; a hand edit of an owned server is a conflict, --overwrite repairs it
	pb, _ := build(t, r, "macos", st, nil)
	r.Apply(pb, st, StateTarget)
	r.Put(r.Home, ".gemini/settings.json", strings.Replace(r.Read(".gemini/settings.json"), "mcp.example.test", "evil.example.test", 1), 0o644)
	r3 := adaptertest.New(t, map[string]string{"rigfile.yaml": strings.Replace(adaptertest.RigYAML, "https://mcp.example.test/mcp", "https://mcp2.example.test/mcp", 1)})
	r3.Home = r.Home
	pc, _ := build(t, r3, "macos", st, nil)
	if !strings.Contains(render(pc), `! "docs" was edited by hand since Rigfile wrote it`) {
		t.Fatalf("%s", render(pc))
	}
	po, _ := build(t, r3, "macos", st, func(e *Env) { e.Overwrite = true })
	r3.Apply(po, st, StateTarget)
	if got := r.Read(".gemini/settings.json"); !strings.Contains(got, "mcp2.example.test") || strings.Contains(got, "evil.example.test") || strings.Contains(got, `"mine"`) || !json.Valid([]byte(got)) {
		t.Fatalf("--overwrite replaces the edited server AND the user's same-named one (that is what the flag means):\n%s", got)
	}
}

func TestUnsupportedItemsAreReportedAndGeminiCliHomeIsHonoured(t *testing.T) {
	r := adaptertest.New(t, nil)
	p, _ := build(t, r, "linux", nil, nil)
	notes := strings.Join(p.Notes, "\n")
	for _, want := range []string{"1 skill(s) not installed for Gemini CLI", "1 subagent(s)", "1 hook(s)", "3 permission rule(s)"} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing %q in\n%s", want, notes)
		}
	}
	pi := r.Plat("linux")
	if d, _ := GeminiDirFor(pi, func(k string) string {
		if k == "GEMINI_CLI_HOME" {
			return "/x/gh"
		}
		return ""
	}); d != filepath.Join("/x/gh", ".gemini") {
		t.Fatalf("%s", d)
	}
}

func TestRoundTripCaptureOfWhatWasApplied(t *testing.T) {
	r := adaptertest.New(t, map[string]string{"commands/ship.md": "---\ndescription: Ship it\n---\nRun tests for $ARGUMENTS, then push.\n"})
	st := state.New()
	p, _ := build(t, r, "macos", st, nil)
	r.Apply(p, st, StateTarget)
	res, err := Capture(capture.Options{Dir: filepath.Join(r.Home, ".gemini"), Home: r.Home, IncludeManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(res.Manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Manifest)
	}
	adaptertest.AssertCanonicalMCP(t, m, true)
	if got := adaptertest.ReadRig(res.Files, "commands/ship.md"); got != "---\ndescription: Ship it\n---\nRun tests for $ARGUMENTS, then push.\n" {
		t.Fatalf("command: %q", got)
	}
	if got := adaptertest.ReadRig(res.Files, "instructions/coding-style.md"); got != "# Style\n- be terse\n" {
		t.Fatalf("%q", got)
	}
	// fixpoint: applying the captured rig again writes the same native files
	dir := t.TempDir()
	rig2 := &adaptertest.Rig{T: t, Dir: dir, Home: t.TempDir()}
	rig2.Put(dir, "rigfile.yaml", string(res.Manifest), 0o644)
	for rel, c := range res.Files {
		rig2.Put(dir, rel, string(c), 0o644)
	}
	pi := rig2.Plat("macos")
	p2, err := Build(Env{Plat: pi, GeminiDir: filepath.Join(rig2.Home, ".gemini")}, rig2.Projection(StateTarget, pi))
	if err != nil {
		t.Fatal(err)
	}
	rig2.Apply(p2, state.New(), StateTarget)
	if a, b := r.Read(".gemini/settings.json"), rig2.Read(".gemini/settings.json"); a != b {
		t.Fatalf("apply(capture(apply(x))) differs from apply(x):\n%s\n---\n%s", a, b)
	}
	if a, b := r.Read(".gemini/commands/ship.toml"), rig2.Read(".gemini/commands/ship.toml"); a != b {
		t.Fatalf("%s\n---\n%s", a, b)
	}
}
