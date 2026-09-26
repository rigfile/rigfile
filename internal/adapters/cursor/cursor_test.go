package cursor

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/adaptertest"
	"github.com/digitaldreamer3462/rigfile/internal/capture"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

func build(t *testing.T, r *adaptertest.Rig, goos string, st *state.State, mod func(*Env)) (*engine.Plan, error) {
	t.Helper()
	pi := r.Plat(goos)
	env := Env{Plat: pi, CursorDir: filepath.Join(r.Home, ".cursor")}
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
			adaptertest.Golden(t, "cursor", r.Tree(".cursor"))
		})
	}
}

func TestUserRulesArePrintedNotWrittenAndProjectRulesAreFiles(t *testing.T) {
	r := adaptertest.New(t, nil)
	p, _ := build(t, r, "linux", nil, nil)
	notes := strings.Join(p.Notes, "\n")
	if !strings.Contains(notes, "Customize → Rules") || !strings.Contains(notes, "| # Style") || !strings.Contains(notes, "| - be terse") {
		t.Fatalf("user-scope instructions must be printed for pasting:\n%s", notes)
	}
	for _, want := range []string{"1 skill(s) not installed for Cursor", "1 subagent(s)", "1 command(s)", "1 hook(s)", "3 permission rule(s)"} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing %q", want)
		}
	}
	r.Apply(p, state.New(), StateTarget)
	if r.Read(".cursor/rules") != "" {
		t.Fatal("no rules file may be written without a project")
	}

	// a project-scope instruction becomes an always-applied .mdc rule
	r2 := adaptertest.New(t, map[string]string{"rigfile.yaml": strings.Replace(adaptertest.RigYAML, "{id: coding-style, file: instructions/style.md}", "{id: coding-style, file: instructions/style.md, scope: project}", 1)})
	proj := t.TempDir()
	p2, err := build(t, r2, "macos", nil, func(e *Env) { e.ProjectDir = proj })
	if err != nil {
		t.Fatal(err)
	}
	st := state.New()
	r2.Apply(p2, st, StateTarget)
	r2.Home = proj
	mdc := r2.Read(".cursor/rules/coding-style.mdc")
	if !strings.HasPrefix(mdc, "---\ndescription: Rigfile: coding-style\nalwaysApply: true\n---\n") || !strings.Contains(mdc, "be terse") {
		t.Fatalf("%s", mdc)
	}
}

func TestMcpJsonKeepsUserServersAndSecretsOutOfTheFile(t *testing.T) {
	r := adaptertest.New(t, nil)
	r.Put(r.Home, ".cursor/mcp.json", "{\n  \"mcpServers\": {\n    \"mine\": {\"command\": \"x\"}\n  }\n}\n", 0o644)
	st := state.New()
	p, _ := build(t, r, "windows", st, nil)
	r.Apply(p, st, StateTarget)
	got := r.Read(".cursor/mcp.json")
	if !json.Valid([]byte(got)) || !strings.Contains(got, `"mine"`) || strings.Contains(got, "secret://") || !strings.Contains(got, `"type":"stdio"`) || !strings.Contains(got, "ALPACA_API_KEY=alpaca/api_key") {
		t.Fatalf("%s", got)
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
	res, err := Capture(capture.Options{Dir: filepath.Join(r.Home, ".cursor"), Home: r.Home, IncludeManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(res.Manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Manifest)
	}
	adaptertest.AssertCanonicalMCP(t, m, true)
	if !strings.Contains(strings.Join(reportMsgs(res), "\n"), "User Rules inside the app") {
		t.Fatal("the capture must say user rules cannot be read")
	}
}

func reportMsgs(res *capture.Result) []string {
	var out []string
	for _, f := range res.Report {
		out = append(out, f.Msg)
	}
	return out
}
