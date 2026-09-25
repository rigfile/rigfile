package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// slimRigYAML is the demo rig with almost everything removed.
const slimRigYAML = `apiVersion: rigfile.dev/v1
name: jiaxu/demo
version: 1.1.0
permissions:
  deny: [{read: '~/.ssh/**'}]
`

func TestThingsTheRigDropsAreRemovedAndUserContentStays(t *testing.T) {
	r := newRig(t, nil)
	cd := r.claudeDir()
	put(t, cd, "CLAUDE.md", "# mine\nkeep me\n", 0o644)
	mcp := newFakeMCP()
	env := Env{MCP: mcp}
	p1, _ := r.plan(env)
	st := state.New()
	r.applyPlan(p1, st)
	env.State = st.Targets[StateTarget]

	// the rig now contains far less
	put(t, r.dir, "rigfile.yaml", slimRigYAML, 0o644)
	p2, err := r.plan(env)
	if err != nil {
		t.Fatal(err)
	}
	out := render(p2)
	for _, want := range []string{"- skills", "- agents", "- commands", "no longer in the rig"} {
		_ = want
	}
	removed := map[string]bool{}
	for _, o := range p2.Ops {
		if o.Symbol == engine.Removal {
			removed[o.Category+"/"+o.Key] = true
		}
	}
	for _, want := range []string{"skill/pdf", "agent/reviewer", "command/ship", "mcp/alpaca", "mcp/docs", "hook/guard", "hook/notify script", "permission/ask"} {
		if !removed[want] {
			t.Errorf("expected a removal for %s in:\n%s", want, out)
		}
	}
	if !removed["instruction/CLAUDE.md"] {
		t.Errorf("expected the CLAUDE.md section to be removed:\n%s", out)
	}
	if !strings.Contains(strings.Join(p2.Notes, "\n"), "deny rule Read(**/.env) is no longer in the rig; left in place") {
		t.Fatalf("a deny must never be auto-removed, but the user must be told: %v", p2.Notes)
	}
	mcp.calls = nil
	r.applyPlan(p2, st)

	for _, gone := range []string{"skills/pdf/SKILL.md", "skills/pdf/scripts/run.sh", "agents/reviewer.md", "commands/ship.md", "rigfile/hooks/notify/notify.sh"} {
		if _, err := os.Stat(filepath.Join(cd, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", gone)
		}
	}
	md, _ := os.ReadFile(filepath.Join(cd, "CLAUDE.md"))
	if string(md) != "# mine\nkeep me\n" {
		t.Fatalf("CLAUDE.md must return to the user's own text:\n%q", md)
	}
	sj, _ := os.ReadFile(filepath.Join(cd, "settings.json"))
	var m map[string]any
	if err := json.Unmarshal(sj, &m); err != nil {
		t.Fatalf("settings.json invalid after removals: %v\n%s", err, sj)
	}
	if strings.Contains(string(sj), "rigfile") {
		t.Fatalf("our hook entries must be gone:\n%s", sj)
	}
	deny := m["permissions"].(map[string]any)["deny"].([]any)
	if len(deny) != 2 { // ~/.ssh/** (still wanted) and **/.env (left in place)
		t.Fatalf("deny = %v", deny)
	}
	if ask, _ := m["permissions"].(map[string]any)["ask"].([]any); len(ask) != 0 {
		t.Fatalf("ask rule should be removed: %v", ask)
	}
	if strings.Join(mcp.calls, ",") != "remove alpaca,remove docs" && strings.Join(mcp.calls, ",") != "remove docs,remove alpaca" {
		t.Fatalf("mcp calls: %v", mcp.calls)
	}
	// ownership is pruned to what the rig still has
	for _, it := range st.Targets[StateTarget].Items {
		if it.Category != "permission" {
			t.Errorf("only the surviving permission should be owned, got %+v", it)
		}
	}
	// and the machine now matches the state
	if ds := state.Check(st.Targets[StateTarget].Items, nil); !state.Clean(ds) {
		t.Fatalf("%+v", ds)
	}
}

func TestHandEditedOrphansAreLeftInPlaceAndDisowned(t *testing.T) {
	r := newRig(t, nil)
	cd := r.claudeDir()
	env := Env{MCP: newFakeMCP()}
	p1, _ := r.plan(env)
	st := state.New()
	r.applyPlan(p1, st)
	env.State = st.Targets[StateTarget]

	put(t, cd, "agents/reviewer.md", "I tuned this agent myself", 0o644)
	put(t, cd, "skills/pdf/notes.md", "my notes", 0o644)
	md, _ := os.ReadFile(filepath.Join(cd, "CLAUDE.md"))
	_ = os.WriteFile(filepath.Join(cd, "CLAUDE.md"), []byte(strings.Replace(string(md), "- be terse", "- be terse!!", 1)), 0o644)
	put(t, r.dir, "rigfile.yaml", slimRigYAML, 0o644)

	p2, err := r.plan(env)
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(p2.Notes, "\n")
	for _, want := range []string{`agent "reviewer" is no longer in the rig but was edited by hand`, `skill "pdf" is no longer in the rig but was edited by hand`, `section "coding-style" in ~/.claude/CLAUDE.md is no longer in the rig but was edited by hand`} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing note %q in:\n%s", want, notes)
		}
	}
	r.applyPlan(p2, st)
	if b, _ := os.ReadFile(filepath.Join(cd, "agents/reviewer.md")); string(b) != "I tuned this agent myself" {
		t.Fatal("a hand-edited orphan must be left alone")
	}
	if _, err := os.Stat(filepath.Join(cd, "skills/pdf/notes.md")); err != nil {
		t.Fatal("hand-edited skill removed")
	}
	if b, _ := os.ReadFile(filepath.Join(cd, "CLAUDE.md")); !strings.Contains(string(b), "be terse!!") {
		t.Fatal("hand-edited section removed")
	}
	for _, it := range st.Targets[StateTarget].Items {
		if it.Category == "agent" || it.Category == "skill" || it.Category == "instruction" {
			t.Fatalf("left-in-place items are no longer Rigfile's: %+v", it)
		}
	}
}

func TestRefusedChangesKeepPreviousOwnership(t *testing.T) {
	r := newRig(t, nil)
	env := Env{MCP: newFakeMCP()}
	p1, _ := r.plan(env)
	st := state.New()
	r.applyPlan(p1, st)
	env.State = st.Targets[StateTarget]
	before := len(env.State.Items)

	// the rig changes the guard hook to an unknown built-in and the docs server to something unsupported
	y := strings.Replace(rigYAML, "builtin:guard", "builtin:does-not-exist", 1)
	y = strings.Replace(y, "auth: oauth", "auth: bearer\n    bearer_token: secret://a/b", 1) + "  a/b: {description: t}\n"
	put(t, r.dir, "rigfile.yaml", y, 0o644)
	p2, err := r.plan(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Conflicts()) != 2 {
		t.Fatalf("expected the two refused changes:\n%s", render(p2))
	}
	for _, o := range p2.Ops {
		if o.Symbol == engine.Removal && (o.Key == "guard" || o.Key == "docs") {
			t.Fatalf("a refused change must NOT remove the earlier installation: %+v", o)
		}
	}
	calls := len(env.MCP.(*fakeMCP).calls)
	r.applyPlan(p2, st)
	if len(env.MCP.(*fakeMCP).calls) != calls {
		t.Fatal("no MCP calls expected")
	}
	sj, _ := os.ReadFile(filepath.Join(r.claudeDir(), "settings.json"))
	if !strings.Contains(string(sj), `"guard"`) {
		t.Fatalf("earlier guard hook must remain installed:\n%s", sj)
	}
	if got := len(st.Targets[StateTarget].Items); got != before {
		t.Fatalf("ownership must be retained for refused items: %d -> %d", before, got)
	}
}

func TestEnginePlanApplyReplacesOwnership(t *testing.T) {
	ts := &state.TargetState{Items: []state.Item{{Category: "a", Key: "gone", Kind: state.KindFile}}}
	keepPrev := state.Item{Category: "b", Key: "kept", Kind: state.KindFile, Hash: "old"}
	p := &engine.Plan{Ops: []engine.Op{
		{Category: "c", Key: "new", Symbol: engine.New, Items: []state.Item{{Category: "c", Key: "new", Kind: state.KindFile}}, Do: func(*engine.Exec) error { return nil }},
		{Category: "b", Key: "kept", Symbol: engine.Conflict, Keep: []state.Item{keepPrev}, Items: []state.Item{{Category: "b", Key: "kept", Hash: "WANTED-BUT-REFUSED"}}},
	}}
	if err := p.Apply(&engine.Exec{}, ts); err != nil {
		t.Fatal(err)
	}
	if len(ts.Items) != 2 || ts.Items[0].Key != "new" || ts.Items[1].Hash != "old" {
		t.Fatalf("items = %+v (want new + the previous record of the refused item; 'gone' dropped)", ts.Items)
	}
	if !p.Identities()[state.Identity(keepPrev)] {
		t.Fatal("kept items must count as planned")
	}
	// a failing op leaves ownership untouched
	ts2 := &state.TargetState{Items: []state.Item{{Category: "x", Key: "y"}}}
	bad := &engine.Plan{Ops: []engine.Op{{Category: "c", Key: "k", Symbol: engine.New, Do: func(*engine.Exec) error { return os.ErrPermission }}}}
	if err := bad.Apply(&engine.Exec{}, ts2); err == nil || len(ts2.Items) != 1 {
		t.Fatalf("failed apply must not modify state: %v %+v", err, ts2.Items)
	}
}
