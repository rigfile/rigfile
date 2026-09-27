package zed

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
	env := Env{Plat: pi, ConfigDir: filepath.Join(r.Home, ".config", "zed")}
	if st != nil {
		env.State = st.Targets[StateTarget]
	}
	if mod != nil {
		mod(&env)
	}
	return Build(env, r.Projection(StateTarget, pi))
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
			adaptertest.Golden(t, "zed", r.Tree(".config/zed"))
		})
	}
}

func TestNothingCoveredIsReportedNotSilentlyDropped(t *testing.T) {
	r := adaptertest.New(t, nil)
	p, _ := build(t, r, "linux", nil, nil)
	notes := strings.Join(p.Notes, "\n")
	for _, want := range []string{
		"instruction \"coding-style\" not installed for Zed",
		"1 skill(s) not installed for Zed", "1 subagent(s)", "1 command(s)", "1 hook(s)", "3 permission rule(s)",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing %q in:\n%s", want, notes)
		}
	}
}

func TestMcpSettingsPreservesCommentsAndKeepsSecretsOut(t *testing.T) {
	r := adaptertest.New(t, nil)
	// a real Zed settings.json: JSONC, with comments the way Zed ships them by default
	r.Put(r.Home, ".config/zed/settings.json", "{\n  // a comment Zed put here\n  \"theme\": \"One Dark\",\n  \"context_servers\": {\n    \"mine\": {\"command\": \"x\"}\n  }\n}\n", 0o644)
	st := state.New()
	p, _ := build(t, r, "windows", st, nil)
	r.Apply(p, st, StateTarget)
	got := r.Read(".config/zed/settings.json")
	if !strings.Contains(got, "// a comment Zed put here") {
		t.Fatalf("a comment outside the edited member must survive: %s", got)
	}
	if !strings.Contains(got, `"mine"`) || strings.Contains(got, "secret://") || !strings.Contains(got, "ALPACA_API_KEY=alpaca/api_key") {
		t.Fatalf("%s", got)
	}
	if ds := state.Check(st.Targets[StateTarget].Items, nil); !state.Clean(ds) {
		t.Fatalf("%+v", ds)
	}
	if p2, _ := build(t, r, "windows", st, nil); p2.Changes() != 0 {
		t.Fatal("second plan must change nothing")
	}
}

func TestRemoteServerIsWrittenAsUrlAndHeaders(t *testing.T) {
	r := adaptertest.New(t, nil)
	p, _ := build(t, r, "macos", nil, nil)
	r.Apply(p, state.New(), StateTarget)
	got := r.Read(".config/zed/settings.json")
	if !json.Valid([]byte(got)) {
		t.Fatalf("not valid JSON: %s", got)
	}
	if !strings.Contains(got, `"docs"`) || !strings.Contains(got, `"url":"https://mcp.example.test/mcp"`) {
		t.Fatalf("the remote server must be written (Zed's docs, unlike Devin's, are unambiguous about url/headers): %s", got)
	}
}

func TestRoundTripCaptureOfWhatWasApplied(t *testing.T) {
	r := adaptertest.New(t, nil)
	st := state.New()
	p, _ := build(t, r, "linux", st, nil)
	r.Apply(p, st, StateTarget)
	res, err := Capture(capture.Options{Dir: filepath.Join(r.Home, ".config", "zed"), Home: r.Home, IncludeManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(res.Manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Manifest)
	}
	adaptertest.AssertCanonicalMCP(t, m, true) // Zed captures remote servers too
}

func TestCaptureToleratesCommentsAndTrailingCommas(t *testing.T) {
	r := adaptertest.New(t, nil)
	r.Put(r.Home, ".config/zed/settings.json", "{\n  // top-level comment\n  \"context_servers\": {\n    \"mine\": {\"command\": \"x\", \"args\": [\"a\",],},\n  },\n}\n", 0o644)
	res, err := Capture(capture.Options{Dir: filepath.Join(r.Home, ".config", "zed"), Home: r.Home, IncludeManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(res.Manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Manifest)
	}
	if s, ok := m.MCPServers["mine"]; !ok || s.Command != "x" {
		t.Fatalf("a commented, trailing-comma settings.json must still capture: %+v", m.MCPServers)
	}
}
