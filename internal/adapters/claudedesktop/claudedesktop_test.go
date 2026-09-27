package claudedesktop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/adapters/adaptertest"
	"github.com/rigfile/rigfile/internal/capture"
	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/state"
)

func build(t *testing.T, r *adaptertest.Rig, goos string, st *state.State) (*engine.Plan, string) {
	t.Helper()
	pi := r.Plat(goos)
	dir, err := DesktopDirFor(pi)
	if err != nil {
		t.Fatal(err)
	}
	env := Env{Plat: pi, DesktopDir: dir}
	if st != nil {
		env.State = st.Targets[StateTarget]
	}
	p, err := Build(env, r.Projection(StateTarget, pi))
	if err != nil {
		t.Fatal(err)
	}
	return p, dir
}

func TestPerOSPathsAndGolden(t *testing.T) {
	for goos, rel := range map[string]string{"macos": "Library/Application Support/Claude", "windows": "AppData/Roaming/Claude"} {
		t.Run(goos, func(t *testing.T) {
			r := adaptertest.New(t, nil)
			p, dir := build(t, r, goos, nil)
			if !strings.HasSuffix(strings.ReplaceAll(dir, `\`, "/"), rel) {
				t.Fatalf("%s: %s", goos, dir)
			}
			r.Apply(p, state.New(), StateTarget)
			adaptertest.Golden(t, "claudedesktop-"+goos, r.Tree(rel))
			notes := strings.Join(p.Notes, "\n")
			for _, want := range []string{"remote MCP server \"docs\" is not written", "only supports MCP servers", "restart Claude Desktop"} {
				if !strings.Contains(notes, want) {
					t.Errorf("missing %q in\n%s", want, notes)
				}
			}
		})
	}
}

func TestNotAvailableOnLinux(t *testing.T) {
	r := adaptertest.New(t, nil)
	pi := r.Plat("linux")
	if ok, why := Available(pi); ok || !strings.Contains(why, "no Linux build") {
		t.Fatalf("%v %s", ok, why)
	}
	p, err := Build(Env{Plat: pi, DesktopDir: r.Home}, r.Projection(StateTarget, pi))
	if err != nil || len(p.Ops) != 0 || len(p.Notes) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestStdioServersOnlyAndUserConfigSurvives(t *testing.T) {
	r := adaptertest.New(t, nil)
	pi := r.Plat("macos")
	dir, _ := DesktopDirFor(pi)
	r.Put(dir, ConfigFile, "{\n  \"globalShortcut\": \"Cmd+Space\",\n  \"mcpServers\": {\n    \"fs\": {\"command\": \"npx\", \"args\": [\"-y\", \"fs\"]}\n  }\n}\n", 0o644)
	st := state.New()
	p, _ := build(t, r, "macos", st)
	r.Apply(p, st, StateTarget)
	got := r.Read("Library/Application Support/Claude/" + ConfigFile)
	if !json.Valid([]byte(got)) || !strings.Contains(got, `"globalShortcut": "Cmd+Space"`) || !strings.Contains(got, `"fs"`) || !strings.Contains(got, `"alpaca"`) || strings.Contains(got, "mcp.example.test") || strings.Contains(got, "secret://") {
		t.Fatalf("%s", got)
	}
}

func TestRoundTripCaptureOfWhatWasApplied(t *testing.T) {
	r := adaptertest.New(t, nil)
	st := state.New()
	p, dir := build(t, r, "windows", st)
	r.Apply(p, st, StateTarget)
	res, err := Capture(capture.Options{Dir: dir, Home: r.Home, IncludeManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(res.Manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Manifest)
	}
	adaptertest.AssertCanonicalMCP(t, m, false) // remote connectors live in the app, not in this file
	if _, ok := m.MCPServers["docs"]; ok {
		t.Fatal("a remote server was never written, so it cannot be captured")
	}
}
