package vscodecopilot

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/adaptertest"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// projectRig is the canonical rig with its instruction marked project scope.
func projectRig(t *testing.T) *adaptertest.Rig {
	return adaptertest.New(t, map[string]string{"rigfile.yaml": strings.Replace(adaptertest.RigYAML, "{id: coding-style, file: instructions/style.md}", "{id: coding-style, file: instructions/style.md, scope: project}", 1)})
}

func build(t *testing.T, r *adaptertest.Rig, goos string, st *state.State, project bool) (*engine.Plan, error) {
	t.Helper()
	pi := r.Plat(goos)
	env := Env{Plat: pi}
	if project {
		env.ProjectDir = filepath.Join(r.Home, "proj")
	}
	if st != nil {
		env.State = st.Targets[StateTarget]
	}
	return Build(env, r.Projection(StateTarget, pi))
}

func TestGoldenFilesAreIdenticalOnEveryOS(t *testing.T) {
	for _, goos := range adaptertest.OSes {
		t.Run(goos, func(t *testing.T) {
			r := projectRig(t)
			p, err := build(t, r, goos, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			r.Apply(p, state.New(), StateTarget)
			adaptertest.Golden(t, "vscode-copilot", r.Tree("proj"))
		})
	}
}

func TestNothingIsWrittenWithoutAProject(t *testing.T) {
	r := projectRig(t)
	p, err := build(t, r, "linux", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Changes() != 0 {
		t.Fatalf("no project, no changes: %d", p.Changes())
	}
	if n := strings.Join(p.Notes, "\n"); !strings.Contains(n, "--project <dir>") {
		t.Fatalf("%s", n)
	}
}

func TestPersonalInstructionsAreNeverWrittenIntoACommittedFile(t *testing.T) {
	r := adaptertest.New(t, nil) // the instruction is user scope
	p, _ := build(t, r, "macos", nil, true)
	notes := strings.Join(p.Notes, "\n")
	if !strings.Contains(notes, `instruction "coding-style" is user-scope: not written`) || !strings.Contains(notes, "scope: project") {
		t.Fatalf("%s", notes)
	}
	r.Apply(p, state.New(), StateTarget)
	if r.Read("proj/.github/copilot-instructions.md") != "" {
		t.Fatal("a user-scope instruction leaked into the project file")
	}
	// the unsupported categories are reported, and the commit-me warning is on the screen
	for _, want := range []string{"1 skill(s) not written for VS Code Copilot", "1 subagent(s)", "1 command(s)", "1 hook(s)", "3 permission rule(s)", "meant to be committed", "targets: [claude-code]"} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing %q in\n%s", want, notes)
		}
	}
}

func TestMcpJsonKeepsTheTeamsServersAndNoSecretValues(t *testing.T) {
	r := projectRig(t)
	r.Put(r.Home, "proj/.vscode/mcp.json", "{\n  \"servers\": {\n    \"theirs\": {\"type\": \"stdio\", \"command\": \"x\"}\n  },\n  \"inputs\": []\n}\n", 0o644)
	st := state.New()
	p, err := build(t, r, "windows", st, true)
	if err != nil {
		t.Fatal(err)
	}
	r.Apply(p, st, StateTarget)
	doc := r.Read("proj/.vscode/mcp.json")
	if !strings.Contains(doc, `"theirs"`) || !strings.Contains(doc, `"inputs": []`) || !json.Valid([]byte(doc)) {
		t.Fatalf("the file's own content must survive:\n%s", doc)
	}
	flat := strings.ReplaceAll(doc, " ", "")
	for _, want := range []string{`"alpaca"`, `"type":"stdio"`, `"command":"rigfile"`, "ALPACA_API_KEY=alpaca/api_key", `"docs"`, `"type":"http"`, `"url":"https://mcp.example.test/mcp"`} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q in\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "secret://") {
		t.Fatalf("a secret reference leaked into a committed file:\n%s", doc)
	}
	// re-applying changes nothing
	if p2, _ := build(t, r, "windows", st, true); p2.Changes() != 0 {
		t.Fatalf("idempotent: %d", p2.Changes())
	}
}

func TestAFileWithCommentsIsEditedAndItsCommentsKept(t *testing.T) {
	r := projectRig(t)
	orig := "{\n  // the team's own server list\n  \"servers\": {\n    \"theirs\": {\"type\": \"stdio\", \"command\": \"x\"} // needed by CI\n  }\n}\n"
	r.Put(r.Home, "proj/.vscode/mcp.json", orig, 0o644)
	st := state.New()
	p, err := build(t, r, "linux", st, true)
	if err != nil {
		t.Fatal(err)
	}
	r.Apply(p, st, StateTarget)
	doc := r.Read("proj/.vscode/mcp.json")
	for _, want := range []string{"// the team's own server list", "// needed by CI", `"theirs"`, `"alpaca"`, `"docs"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q in\n%s", want, doc)
		}
	}
	if p2, _ := build(t, r, "linux", st, true); p2.Changes() != 0 {
		t.Fatalf("idempotent: %d", p2.Changes())
	}
}

func TestAFileThatIsNotJSONIsLeftAloneWithANote(t *testing.T) {
	r := projectRig(t)
	r.Put(r.Home, "proj/.vscode/mcp.json", "{ servers: nope", 0o644)
	p, err := build(t, r, "linux", nil, true)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if n := strings.Join(p.Notes, "\n"); !strings.Contains(n, "is not valid JSON") || !strings.Contains(n, "alpaca, docs") {
		t.Fatalf("%s", n)
	}
	r.Apply(p, state.New(), StateTarget)
	if r.Read("proj/.vscode/mcp.json") != "{ servers: nope" {
		t.Fatal("untouched")
	}
}

func TestProjectInstructionsBecomeAMarkedSectionAndSurviveOtherText(t *testing.T) {
	r := projectRig(t)
	r.Put(r.Home, "proj/.github/copilot-instructions.md", "# Team rules\nUse tabs.\n", 0o644)
	st := state.New()
	p, _ := build(t, r, "linux", st, true)
	r.Apply(p, st, StateTarget)
	got := r.Read("proj/.github/copilot-instructions.md")
	if !strings.Contains(got, "# Team rules\nUse tabs.") || !strings.Contains(got, "be terse") || !strings.Contains(got, "coding-style") {
		t.Fatalf("%s", got)
	}
	if p2, _ := build(t, r, "linux", st, true); p2.Changes() != 0 {
		t.Fatal("idempotent")
	}
}
