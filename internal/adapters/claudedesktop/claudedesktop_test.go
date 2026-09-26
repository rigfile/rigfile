package claudedesktop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/adaptertest"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/state"
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
