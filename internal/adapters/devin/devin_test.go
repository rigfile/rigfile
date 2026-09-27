package devin

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
	env := Env{Plat: pi, ConfigDir: filepath.Join(r.Home, ".config", "devin")}
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
			adaptertest.Golden(t, "devin", r.Tree(".config/devin"))
		})
	}
}

func TestNothingCoveredIsReportedNotSilentlyDropped(t *testing.T) {
	r := adaptertest.New(t, nil)
	p, _ := build(t, r, "linux", nil, nil)
	notes := strings.Join(p.Notes, "\n")
	for _, want := range []string{
		"instruction \"coding-style\" not installed for Devin",
		"MCP server \"docs\" not installed for Devin",
		"1 skill(s) not installed for Devin", "1 subagent(s)", "1 command(s)", "1 hook(s)", "3 permission rule(s)",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing %q in:\n%s", want, notes)
		}
	}
	r.Apply(p, state.New(), StateTarget)
	if r.Read(".config/devin/mcp_config.json") == "" {
		t.Fatal("the alpaca stdio server must still be written")
	}
}

func TestMcpConfigKeepsUserServersAndSecretsOutOfTheFile(t *testing.T) {
	r := adaptertest.New(t, nil)
	r.Put(r.Home, ".config/devin/mcp_config.json", "{\n  \"mcpServers\": {\n    \"mine\": {\"command\": \"x\"}\n  }\n}\n", 0o644)
	st := state.New()
	p, _ := build(t, r, "windows", st, nil)
	r.Apply(p, st, StateTarget)
	got := r.Read(".config/devin/mcp_config.json")
	if !json.Valid([]byte(got)) || !strings.Contains(got, `"mine"`) || strings.Contains(got, "secret://") || !strings.Contains(got, "ALPACA_API_KEY=alpaca/api_key") {
		t.Fatalf("%s", got)
	}
	if strings.Contains(got, `"type"`) {
		t.Fatal("the vendor docs do not document a type field; do not invent one")
	}
	if ds := state.Check(st.Targets[StateTarget].Items, nil); !state.Clean(ds) {
		t.Fatalf("%+v", ds)
	}
	if p2, _ := build(t, r, "windows", st, nil); p2.Changes() != 0 {
		t.Fatal("second plan must change nothing")
	}
}

func TestRoundTripCaptureOfWhatWasApplied(t *testing.T) {
	r := adaptertest.New(t, nil)
	st := state.New()
	p, _ := build(t, r, "linux", st, nil)
	r.Apply(p, st, StateTarget)
	res, err := Capture(capture.Options{Dir: filepath.Join(r.Home, ".config", "devin"), Home: r.Home, IncludeManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(res.Manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Manifest)
	}
	adaptertest.AssertCanonicalMCP(t, m, false) // Devin skips remote servers (field-name ambiguity), so "docs" never round-trips
}
