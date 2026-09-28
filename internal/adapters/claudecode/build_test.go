package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/apply"
	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/layers"
	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/merge"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/state"
)

// ---- fixtures --------------------------------------------------------------------------------

type fakeMCP struct {
	avail   bool
	servers map[string]string // name -> json
	calls   []string
}

func newFakeMCP() *fakeMCP { return &fakeMCP{avail: true, servers: map[string]string{}} }

func (f *fakeMCP) Available() bool { return f.avail }
func (f *fakeMCP) Present(n string) (bool, error) {
	_, ok := f.servers[n]
	return ok, nil
}
func (f *fakeMCP) AddJSON(n, doc string) error {
	f.calls = append(f.calls, "add "+n)
	f.servers[n] = doc
	return nil
}
func (f *fakeMCP) Remove(n string) error {
	f.calls = append(f.calls, "remove "+n)
	delete(f.servers, n)
	return nil
}

const skillMD = "---\nname: pdf\ndescription: Work with PDFs\n---\nbody\n"

func put(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

const rigYAML = `apiVersion: rigfile.dev/v1
name: adams/demo
version: 1.0.0
instructions:
  - {id: coding-style, file: instructions/style.md}
skills:
  - {path: skills/pdf}
agents:
  - {path: agents/reviewer.md}
commands:
  - {path: commands/ship.md}
hooks:
  - {id: guard, event: pre_tool_use, match: {tool: bash}, run: 'builtin:guard'}
  - id: notify
    event: stop
    run: {macos: hooks/notify.sh, linux: hooks/notify.sh}
mcp_servers:
  alpaca:
    command: npx
    args: ['-y', 'alpaca-mcp@1.4.2']
    env: {ALPACA_API_KEY: 'secret://alpaca/api_key', ALPACA_PAPER: 'true'}
  docs:
    transport: http
    url: https://mcp.example.test/mcp
    auth: oauth
permissions:
  deny: [{read: '~/.ssh/**'}, {read: '**/.env'}]
  ask: [{bash: 'git push*'}]
secrets:
  alpaca/api_key: {description: key}
`

type rig struct {
	t    *testing.T
	dir  string
	home string
}

func newRig(t *testing.T, files map[string]string) *rig {
	t.Helper()
	dir := t.TempDir()
	base := map[string]string{
		"rigfile.yaml":              rigYAML,
		"instructions/style.md":     "# Style\n- be terse\n",
		"skills/pdf/SKILL.md":       skillMD,
		"skills/pdf/scripts/run.sh": "#!/bin/sh\necho pdf\n",
		"agents/reviewer.md":        "---\nname: reviewer\ndescription: reviews\n---\nreview\n",
		"commands/ship.md":          "ship it\n",
		"hooks/notify.sh":           "#!/bin/sh\necho done\n",
	}
	for k, v := range files {
		if v == "\x00delete" {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	for rel, c := range base {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		put(t, dir, rel, c, mode)
	}
	return &rig{t: t, dir: dir, home: t.TempDir()}
}

func (r *rig) plat(goos string) *platform.Info {
	pi, err := platform.New(platform.Options{GOOS: goos, GOARCH: "arm64", Getenv: func(k string) string {
		if k == "HOME" || k == "USERPROFILE" {
			return r.home
		}
		return ""
	}})
	if err != nil {
		r.t.Fatal(err)
	}
	return pi
}

func (r *rig) claudeDir() string { return filepath.Join(r.home, ".claude") }

// plan resolves, merges, projects and builds.
func (r *rig) plan(env Env) (*engine.Plan, error) {
	r.t.Helper()
	top, err := manifest.Load(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	if ps := manifest.Check(top); manifest.HasErrors(ps) {
		r.t.Fatalf("rig has errors: %v", ps)
	}
	res, err := layers.Resolve(top, layers.DirSource{})
	if err != nil {
		r.t.Fatal(err)
	}
	m, err := merge.Merge(res.Layers, "claude-code")
	if err != nil {
		r.t.Fatal(err)
	}
	if env.Plat == nil {
		env.Plat = r.plat("linux")
	}
	if env.ClaudeDir == "" {
		env.ClaudeDir = r.claudeDir()
	}
	proj := m.Project(string(env.Plat.OS), "claude-code")
	return Build(env, proj)
}

// applyPlan executes a plan with a fresh writer and returns the run id and the new state.
func (r *rig) applyPlan(p *engine.Plan, st *state.State) string {
	r.t.Helper()
	w := &apply.Writer{BackupRoot: filepath.Join(r.home, ".rigfile", "backups")}
	if err := p.Apply(&engine.Exec{W: w}, st.Target(StateTarget)); err != nil {
		r.t.Fatalf("apply: %v", err)
	}
	id, err := w.Commit("test")
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func opBy(p *engine.Plan, category, key string) engine.Op {
	for _, o := range p.Ops {
		if o.Category == category && o.Key == key {
			return o
		}
	}
	return engine.Op{}
}

func render(p *engine.Plan) string {
	var b bytes.Buffer
	p.Render(&b)
	return b.String()
}

// ---- tests -----------------------------------------------------------------------------------

func TestFreshApplyCreatesEverything(t *testing.T) {
	r := newRig(t, nil)
	mcp := newFakeMCP()
	p, err := r.plan(Env{MCP: mcp})
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range []string{"+ "} {
		if !strings.Contains(render(p), sym) {
			t.Fatalf("plan output:\n%s", render(p))
		}
	}
	st := state.New()
	r.applyPlan(p, st)

	cd := r.claudeDir()
	// instructions: marked section in CLAUDE.md
	md, _ := os.ReadFile(filepath.Join(cd, "CLAUDE.md"))
	if !strings.Contains(string(md), "<!-- rigfile:begin coding-style sha256=") || !strings.Contains(string(md), "- be terse") {
		t.Fatalf("CLAUDE.md:\n%s", md)
	}
	// skill tree with executable script
	if b, _ := os.ReadFile(filepath.Join(cd, "skills/pdf/SKILL.md")); string(b) != skillMD {
		t.Fatal("skill not copied")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(cd, "skills/pdf/scripts/run.sh")); st.Mode().Perm()&0o111 == 0 {
			t.Fatal("skill script lost its executable bit")
		}
	}
	for _, f := range []string{"agents/reviewer.md", "commands/ship.md", "rigfile/hooks/notify/notify.sh"} {
		if _, err := os.Stat(filepath.Join(cd, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	// settings.json: valid, has permissions and both hooks
	sj, _ := os.ReadFile(filepath.Join(cd, "settings.json"))
	var m map[string]any
	if err := json.Unmarshal(sj, &m); err != nil {
		t.Fatalf("settings.json invalid: %v\n%s", err, sj)
	}
	perms := m["permissions"].(map[string]any)
	if len(perms["deny"].([]any)) != 2 || len(perms["ask"].([]any)) != 1 {
		t.Fatalf("permissions: %v", perms)
	}
	hooks := m["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)[0].(map[string]any)
	h0 := pre["hooks"].([]any)[0].(map[string]any)
	if pre["matcher"] != "Bash" || h0["command"] != "rigfile" || strings.Join(toStr(h0["args"]), " ") != "hook run guard" {
		t.Fatalf("PreToolUse hook: %v", pre)
	}
	stop := hooks["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if !strings.HasSuffix(filepath.ToSlash(stop["command"].(string)), "rigfile/hooks/notify/notify.sh") || len(stop["args"].([]any)) != 0 {
		t.Fatalf("Stop hook must use exec form with the owned script path: %v", stop)
	}
	// MCP: stdio server wrapped in rigfile exec with secret by reference only
	alp := mcp.servers["alpaca"]
	if !strings.Contains(alp, `"command":"rigfile"`) || !strings.Contains(alp, `"--secret","ALPACA_API_KEY=alpaca/api_key"`) ||
		!strings.Contains(alp, `"--env","ALPACA_PAPER=true"`) || !strings.Contains(alp, `"--","npx","-y","alpaca-mcp@1.4.2"`) {
		t.Fatalf("alpaca entry: %s", alp)
	}
	if strings.Contains(alp, "FAKE") || strings.Contains(alp, "sk-") {
		t.Fatal("no secret value may appear")
	}
	if mcp.servers["docs"] != `{"type":"http","url":"https://mcp.example.test/mcp"}` {
		t.Fatalf("docs entry: %s", mcp.servers["docs"])
	}
	// state owns everything it wrote
	kinds := map[string]int{}
	for _, it := range st.Targets[StateTarget].Items {
		kinds[it.Category+"/"+it.Kind]++
	}
	for _, want := range []string{"instruction/region", "skill/tree", "agent/file", "command/file", "hook/file", "hook/json-raw", "permission/json-list", "mcp/mcp"} {
		if kinds[want] == 0 {
			t.Errorf("state missing %s: %v", want, kinds)
		}
	}
	if kinds["permission/json-list"] != 3 {
		t.Errorf("3 permission rules expected in state, got %d", kinds["permission/json-list"])
	}
}

func toStr(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestSecondPlanIsAllUnchangedAndDriftIsClean(t *testing.T) {
	r := newRig(t, nil)
	mcp := newFakeMCP()
	env := Env{MCP: mcp}
	p1, _ := r.plan(env)
	st := state.New()
	r.applyPlan(p1, st)

	env.State = st.Targets[StateTarget]
	p2, err := r.plan(env)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Changes() != 0 {
		t.Fatalf("a re-plan after apply must have no changes:\n%s", render(p2))
	}
	if !strings.Contains(render(p2), "no changes") {
		t.Fatal(render(p2))
	}
	calls := len(mcp.calls)
	r.applyPlan(p2, st) // applying an empty plan changes nothing and adds no MCP calls
	if len(mcp.calls) != calls {
		t.Fatalf("idempotent apply made extra MCP calls: %v", mcp.calls)
	}
	ds := state.Check(st.Targets[StateTarget].Items, map[string]state.Probe{state.KindMCP: MCPProbe(mcp)})
	if !state.Clean(ds) {
		t.Fatalf("drift after a clean apply: %+v", ds)
	}
}

func TestUserContentIsPreserved(t *testing.T) {
	r := newRig(t, nil)
	cd := r.claudeDir()
	put(t, cd, "CLAUDE.md", "# My own notes\nkeep me\n", 0o644)
	put(t, cd, "settings.json", "{\n  \"model\": \"sonnet\",\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(npm run *)\"\n    ]\n  },\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\n        \"matcher\": \"Write\",\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"mine.sh\"\n          }\n        ]\n      }\n    ]\n  }\n}\n", 0o644)
	put(t, cd, "agents/mine.md", "my agent", 0o644)
	p, err := r.plan(Env{MCP: newFakeMCP()})
	if err != nil {
		t.Fatal(err)
	}
	r.applyPlan(p, state.New())
	md, _ := os.ReadFile(filepath.Join(cd, "CLAUDE.md"))
	if !strings.HasPrefix(string(md), "# My own notes\nkeep me\n") {
		t.Fatalf("user text in CLAUDE.md changed:\n%s", md)
	}
	sj, _ := os.ReadFile(filepath.Join(cd, "settings.json"))
	for _, must := range []string{`"model": "sonnet"`, `"Bash(npm run *)"`, `"mine.sh"`, `"matcher": "Write"`} {
		if !strings.Contains(string(sj), must) {
			t.Fatalf("user setting %s lost:\n%s", must, sj)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(sj, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if n := len(m["hooks"].(map[string]any)["PreToolUse"].([]any)); n != 2 {
		t.Fatalf("user's hook must stay alongside ours, got %d entries", n)
	}
	if b, _ := os.ReadFile(filepath.Join(cd, "agents/mine.md")); string(b) != "my agent" {
		t.Fatal("unrelated agent touched")
	}
}

func TestConflictsAreRefusedAndOverwriteReplaces(t *testing.T) {
	r := newRig(t, nil)
	cd := r.claudeDir()
	put(t, cd, "agents/reviewer.md", "a different agent the user wrote", 0o644)
	put(t, cd, "skills/pdf/SKILL.md", "user's own pdf skill", 0o644)
	mcp := newFakeMCP()
	mcp.servers["alpaca"] = `{"user":"owned"}`

	p, err := r.plan(Env{MCP: mcp})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"agent", "reviewer"}, {"skill", "pdf"}, {"mcp", "alpaca"}} {
		if o := opBy(p, c[0], c[1]); o.Symbol != engine.Conflict || o.Do != nil || len(o.Items) != 0 {
			t.Fatalf("%s %s must be a refused conflict: %+v\n%s", c[0], c[1], o, render(p))
		}
	}
	if len(p.Conflicts()) != 3 || !strings.Contains(render(p), "refused") {
		t.Fatalf("%s", render(p))
	}
	st := state.New()
	r.applyPlan(p, st)
	if b, _ := os.ReadFile(filepath.Join(cd, "agents/reviewer.md")); string(b) != "a different agent the user wrote" {
		t.Fatal("conflicting agent was overwritten")
	}
	if b, _ := os.ReadFile(filepath.Join(cd, "skills/pdf/SKILL.md")); string(b) != "user's own pdf skill" {
		t.Fatal("conflicting skill was overwritten")
	}
	if mcp.servers["alpaca"] != `{"user":"owned"}` {
		t.Fatal("conflicting MCP server was replaced")
	}
	for _, it := range st.Targets[StateTarget].Items {
		if it.Key == "reviewer" || it.Key == "pdf" || (it.Category == "mcp" && it.Key == "alpaca") {
			t.Fatalf("Rigfile must not claim ownership of what it refused: %+v", it)
		}
	}
	// --overwrite replaces them (still backed up by the writer)
	p2, _ := r.plan(Env{MCP: mcp, Overwrite: true})
	if len(p2.Conflicts()) != 0 {
		t.Fatalf("overwrite should clear conflicts:\n%s", render(p2))
	}
	r.applyPlan(p2, state.New())
	if b, _ := os.ReadFile(filepath.Join(cd, "agents/reviewer.md")); !strings.Contains(string(b), "review") {
		t.Fatal("overwrite did not replace the agent")
	}
}

func TestHandEditsToOwnedThingsAreConflictsAndUpdatesFlowThrough(t *testing.T) {
	r := newRig(t, nil)
	env := Env{MCP: newFakeMCP()}
	p, _ := r.plan(env)
	st := state.New()
	r.applyPlan(p, st)
	cd := r.claudeDir()
	env.State = st.Targets[StateTarget]

	// 1) the rig changes: new agent text, skill loses a file and gains another -> updates
	put(t, r.dir, "agents/reviewer.md", "---\nname: reviewer\ndescription: v2\n---\nnew\n", 0o644)
	_ = os.Remove(filepath.Join(r.dir, "skills/pdf/scripts/run.sh"))
	put(t, r.dir, "skills/pdf/notes.md", "n", 0o644)
	p2, err := r.plan(env)
	if err != nil {
		t.Fatal(err)
	}
	if opBy(p2, "agent", "reviewer").Symbol != engine.Update || opBy(p2, "skill", "pdf").Symbol != engine.Update {
		t.Fatalf("changes to the rig must plan as updates:\n%s", render(p2))
	}
	r.applyPlan(p2, st)
	if b, _ := os.ReadFile(filepath.Join(cd, "agents/reviewer.md")); !strings.Contains(string(b), "v2") {
		t.Fatal("agent not updated")
	}
	if _, err := os.Stat(filepath.Join(cd, "skills/pdf/scripts/run.sh")); !os.IsNotExist(err) {
		t.Fatal("a file the new skill version no longer has must be removed (owned tree)")
	}
	if _, err := os.Stat(filepath.Join(cd, "skills/pdf/notes.md")); err != nil {
		t.Fatal("new skill file missing")
	}

	// 2) the user hand-edits things Rigfile owns -> conflicts, nothing overwritten
	env.State = st.Targets[StateTarget]
	put(t, cd, "agents/reviewer.md", "my tweak", 0o644)
	put(t, cd, "skills/pdf/notes.md", "my tweak", 0o644)
	md, _ := os.ReadFile(filepath.Join(cd, "CLAUDE.md"))
	_ = os.WriteFile(filepath.Join(cd, "CLAUDE.md"), []byte(strings.Replace(string(md), "- be terse", "- be terse (my edit)", 1)), 0o644)
	put(t, r.dir, "agents/reviewer.md", "---\nname: reviewer\ndescription: v3\n---\n", 0o644)
	put(t, r.dir, "instructions/style.md", "# Style\n- be verbose\n", 0o644)
	p3, err := r.plan(env)
	if err != nil {
		t.Fatal(err)
	}
	if opBy(p3, "agent", "reviewer").Symbol != engine.Conflict || opBy(p3, "skill", "pdf").Symbol != engine.Conflict {
		t.Fatalf("hand-edited agent must be refused:\n%s", render(p3))
	}
	instr := opBy(p3, "instruction", "CLAUDE.md")
	if instr.Symbol != engine.Conflict || !strings.Contains(strings.Join(instr.Detail, "\n"), "edited by hand") {
		t.Fatalf("hand-edited section must be refused: %+v", instr)
	}
}

func TestMCPCases(t *testing.T) {
	r := newRig(t, nil)
	// claude CLI missing
	p, _ := r.plan(Env{MCP: &fakeMCP{avail: false}})
	if o := opBy(p, "mcp", "alpaca"); o.Symbol != engine.Conflict || !strings.Contains(o.Summary, "not installed") {
		t.Fatalf("%+v", o)
	}
	p, _ = r.plan(Env{})
	if o := opBy(p, "mcp", "docs"); o.Symbol != engine.Conflict {
		t.Fatalf("nil client must be treated as unavailable: %+v", o)
	}
	// definition change: remove + add
	mcp := newFakeMCP()
	env := Env{MCP: mcp}
	p, _ = r.plan(env)
	st := state.New()
	r.applyPlan(p, st)
	env.State = st.Targets[StateTarget]
	b, _ := os.ReadFile(filepath.Join(r.dir, "rigfile.yaml"))
	put(t, r.dir, "rigfile.yaml", strings.Replace(string(b), "alpaca-mcp@1.4.2", "alpaca-mcp@1.5.0", 1), 0o644)
	p, _ = r.plan(env)
	if o := opBy(p, "mcp", "alpaca"); o.Symbol != engine.Update {
		t.Fatalf("%+v\n%s", o, render(p))
	}
	mcp.calls = nil
	r.applyPlan(p, st)
	if strings.Join(mcp.calls, ",") != "remove alpaca,add alpaca" || !strings.Contains(mcp.servers["alpaca"], "alpaca-mcp@1.5.0") {
		t.Fatalf("calls=%v entry=%s", mcp.calls, mcp.servers["alpaca"])
	}
	// something Stage 1 cannot express is reported, not silently dropped
	put(t, r.dir, "rigfile.yaml", strings.Replace(rigYAML, "auth: oauth", "auth: bearer\n    bearer_token: secret://a/b", 1)+"  a/b: {description: t}\n", 0o644)
	p, _ = r.plan(Env{MCP: newFakeMCP()})
	if o := opBy(p, "mcp", "docs"); o.Symbol != engine.Conflict || !strings.Contains(o.Summary, "bearer") {
		t.Fatalf("%+v", o)
	}
	// Runs flag is set on MCP servers (⚠ on the screen)
	r2 := newRig(t, nil)
	p2, _ := r2.plan(Env{MCP: newFakeMCP()})
	if !strings.Contains(render(p2), "⚠ executes code") {
		t.Fatalf("executables must be flagged:\n%s", render(p2))
	}
}

func TestHookProblems(t *testing.T) {
	r := newRig(t, map[string]string{"rigfile.yaml": strings.Replace(rigYAML, "builtin:guard", "builtin:does-not-exist", 1)})
	p, err := r.plan(Env{MCP: newFakeMCP()})
	if err != nil {
		t.Fatal(err)
	}
	if o := opBy(p, "hook", "guard"); o.Symbol != engine.Conflict || !strings.Contains(o.Summary, "unknown built-in") {
		t.Fatalf("%+v", o)
	}
	// a hook with no version for this OS is a note, not an install
	r2 := newRig(t, nil)
	p2, err := r2.plan(Env{MCP: newFakeMCP(), Plat: r2.plat("windows")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p2.Notes, "\n"), `hook "notify" has no command for windows`) {
		t.Fatalf("notes: %v", p2.Notes)
	}
}

func TestPermissionShadowingIsReportedAsANote(t *testing.T) {
	y := strings.Replace(rigYAML, "ask: [{bash: 'git push*'}]", "ask: [{bash: 'git push*'}]\n  allow: [{read: '~/.ssh/config'}]", 1)
	r := newRig(t, map[string]string{"rigfile.yaml": y})
	p, err := r.plan(Env{MCP: newFakeMCP()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "Read(~/.ssh/config) has no effect") {
		t.Fatalf("notes: %v", p.Notes)
	}
}

func TestProjectScopeInstructions(t *testing.T) {
	y := strings.Replace(rigYAML, "  - {id: coding-style, file: instructions/style.md}", "  - {id: coding-style, file: instructions/style.md}\n  - {id: repo-rules, file: instructions/repo.md, scope: project}", 1)
	r := newRig(t, map[string]string{"rigfile.yaml": y, "instructions/repo.md": "# repo rules\n"})
	p, _ := r.plan(Env{MCP: newFakeMCP()})
	if !strings.Contains(strings.Join(p.Notes, "\n"), "--project") {
		t.Fatalf("a project-scope instruction without a project must say why: %v", p.Notes)
	}
	proj := t.TempDir()
	p, _ = r.plan(Env{MCP: newFakeMCP(), ProjectDir: proj})
	r.applyPlan(p, state.New())
	if b, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md")); !strings.Contains(string(b), "# repo rules") {
		t.Fatalf("project CLAUDE.md: %s", b)
	}
}

func TestMalformedMarkersRefuseToPlan(t *testing.T) {
	r := newRig(t, nil)
	put(t, r.claudeDir(), "CLAUDE.md", "<!-- rigfile:begin coding-style sha256=000000000000 -->\nno end marker\n", 0o644)
	_, err := r.plan(Env{MCP: newFakeMCP()})
	if err == nil || !errors.Is(err, errMalformed()) && !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("a file with broken markers must not be edited: %v", err)
	}
}

func errMalformed() error { return errors.New("malformed") }

func TestRollbackRemovesEverythingItCreated(t *testing.T) {
	r := newRig(t, nil)
	p, _ := r.plan(Env{MCP: newFakeMCP()})
	st := state.New()
	id := r.applyPlan(p, st)
	if id == "" {
		t.Fatal("no run recorded")
	}
	out, err := apply.Rollback(filepath.Join(r.home, ".rigfile", "backups"), id, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if o.Action == "skipped" {
			t.Fatalf("nothing should be skipped: %+v", o)
		}
	}
	if _, err := os.Stat(r.claudeDir()); !os.IsNotExist(err) {
		var left []string
		_ = filepath.Walk(r.claudeDir(), func(p string, i os.FileInfo, e error) error {
			if e == nil && !i.IsDir() {
				left = append(left, p)
			}
			return nil
		})
		t.Fatalf("rollback left files behind: %v", left)
	}
}

func TestPlanRenderIsDeterministic(t *testing.T) {
	r := newRig(t, nil)
	a, _ := r.plan(Env{MCP: newFakeMCP()})
	b, _ := r.plan(Env{MCP: newFakeMCP()})
	if render(a) != render(b) {
		t.Fatal("plan rendering must be deterministic")
	}
	out := strings.ReplaceAll(render(a), `\`, "/") // the screen uses the OS separator
	for _, want := range []string{"INSTRUCTIONS", "SKILLS", "AGENTS", "COMMANDS", "MCP SERVERS", "HOOKS", "PERMISSIONS", "~/.claude/skills/pdf"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

func TestBuildNeedsEnv(t *testing.T) {
	if _, err := Build(Env{}, &merge.Projection{}); err == nil {
		t.Fatal("Build without Plat/ClaudeDir must fail")
	}
}

func TestMatcherFor(t *testing.T) {
	cases := []struct {
		m       manifest.HookMatch
		matcher string
		ifRule  string
		err     bool
	}{
		{manifest.HookMatch{}, "", "", false},
		{manifest.HookMatch{Tool: "*"}, "", "", false},
		{manifest.HookMatch{Tool: "bash"}, "Bash", "", false},
		{manifest.HookMatch{Tool: "bash", Command: "git commit*"}, "Bash", "Bash(git commit*)", false},
		{manifest.HookMatch{Tool: "write", Path: "**/*.env"}, "Write", "Edit(**/*.env)", false},
		{manifest.HookMatch{Tool: "mcp"}, "mcp__.*", "", false},
		{manifest.HookMatch{Tool: "mcp:github:create_issue"}, "mcp__github__create_issue", "", false},
		{manifest.HookMatch{Tool: "frobnicate"}, "", "", true},
		{manifest.HookMatch{Tool: "web_fetch", Command: "x"}, "", "", true},
	}
	for _, c := range cases {
		m, i, err := matcherFor(c.m)
		if (err != nil) != c.err || m != c.matcher || i != c.ifRule {
			t.Errorf("%+v -> %q %q %v", c.m, m, i, err)
		}
	}
}

func TestScriptCommandWindows(t *testing.T) {
	win, _ := platform.New(platform.Options{GOOS: "windows", Getenv: func(string) string { return "" }})
	cmd, args := scriptCommand(win, `C:\x\notify.ps1`)
	if cmd != "powershell.exe" || args[len(args)-1] != `C:\x\notify.ps1` || args[0] != "-NoProfile" {
		t.Fatalf("%s %v", cmd, args)
	}
	cmd, args = scriptCommand(win, `C:\x\a.exe`)
	if cmd != `C:\x\a.exe` || len(args) != 0 {
		t.Fatalf("%s %v", cmd, args)
	}
}
