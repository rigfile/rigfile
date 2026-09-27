package state

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/hashing"
	"github.com/rigfile/rigfile/internal/splice"
)

func TestLoadSaveRoundTripAndPermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil || len(s.Targets) != 0 {
		t.Fatalf("missing file must be an empty state: %+v %v", s, err)
	}
	tg := s.Target("claude-code")
	tg.Rig = RigRef{Name: "x/rig", Version: "1.0.0", Hash: "abc"}
	tg.Upsert(Item{Category: "skill", Key: "b", Kind: KindTree, Path: "/p/b", Hash: "h1"})
	tg.Upsert(Item{Category: "agent", Key: "a", Kind: KindFile, Path: "/p/a.md", Hash: "h2"})
	tg.Upsert(Item{Category: "skill", Key: "b", Kind: KindTree, Path: "/p/b", Hash: "h3"}) // same identity: replaced
	if err := s.Save(dir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("state save is stubbed on Windows until Stage 3")
		}
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	items := back.Targets["claude-code"].Items
	if len(items) != 2 || items[0].Category != "agent" || items[1].Hash != "h3" {
		t.Fatalf("items = %+v (want sorted, deduplicated by identity)", items)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(dir, FileName)); st.Mode().Perm() != 0o600 {
			t.Fatalf("state.json mode = %v", st.Mode().Perm())
		}
	}
}

func TestLoadRejectsCorruptAndFutureVersions(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o600)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("%v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version": 99}`), 0o600)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("%v", err)
	}
}

func TestUpsertDistinguishesRegionsAndJSONValues(t *testing.T) {
	tg := &TargetState{}
	tg.Upsert(Item{Category: "permission", Key: "deny", Kind: KindJSONList, Path: "/s.json", Detail: map[string]string{"list": "permissions.deny", "value": "A"}})
	tg.Upsert(Item{Category: "permission", Key: "deny", Kind: KindJSONList, Path: "/s.json", Detail: map[string]string{"list": "permissions.deny", "value": "B"}})
	tg.Upsert(Item{Category: "instruction", Key: "a", Kind: KindRegion, Path: "/c.md", Detail: map[string]string{"region": "r1"}})
	tg.Upsert(Item{Category: "instruction", Key: "a", Kind: KindRegion, Path: "/c.md", Detail: map[string]string{"region": "r2"}})
	if len(tg.Items) != 4 {
		t.Fatalf("items = %d, want 4 distinct", len(tg.Items))
	}
	tg.Remove("permission", "deny")
	if len(tg.Items) != 2 {
		t.Fatalf("after Remove: %d", len(tg.Items))
	}
}

func TestDriftFileAndTree(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "agent.md")
	_ = os.WriteFile(f, []byte("v1"), 0o644)
	sk := filepath.Join(dir, "skill")
	_ = os.MkdirAll(sk, 0o755)
	_ = os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("s"), 0o644)
	fh, _ := hashing.File(f)
	th, _ := hashing.Tree(sk)
	items := []Item{
		{Category: "agent", Key: "a", Kind: KindFile, Path: f, Hash: fh},
		{Category: "skill", Key: "s", Kind: KindTree, Path: sk, Hash: th},
	}
	if ds := Check(items, nil); !Clean(ds) {
		t.Fatalf("fresh state must be clean: %+v", ds)
	}
	_ = os.WriteFile(f, []byte("hand edit"), 0o644)
	_ = os.WriteFile(filepath.Join(sk, "extra.md"), []byte("x"), 0o644)
	ds := Check(items, nil)
	if ds[0].Status != Modified || ds[1].Status != Modified || Clean(ds) {
		t.Fatalf("%+v", ds)
	}
	_ = os.Remove(f)
	_ = os.RemoveAll(sk)
	ds = Check(items, nil)
	if ds[0].Status != Missing || ds[1].Status != Missing {
		t.Fatalf("%+v", ds)
	}
}

func TestDriftRegionAndJSONList(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "CLAUDE.md")
	doc, _, _ := splice.Upsert([]byte("# mine\n"), splice.HTML, "rig#sec", []byte("- rule\n"), splice.Options{})
	_ = os.WriteFile(md, doc, 0o644)
	reg, _, _ := splice.Find(doc, splice.HTML, "rig#sec")
	js := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(js, []byte(`{"permissions":{"deny":["Read(x)"]}}`), 0o644)
	items := []Item{
		{Category: "instruction", Key: "sec", Kind: KindRegion, Path: md, Hash: reg.Hash, Detail: map[string]string{"region": "rig#sec", "style": "html"}},
		{Category: "permission", Key: "deny", Kind: KindJSONList, Path: js, Detail: map[string]string{"list": "permissions.deny", "value": "Read(x)"}},
	}
	if ds := Check(items, nil); !Clean(ds) {
		t.Fatalf("%+v", ds)
	}
	// hand-edit inside the managed section
	edited := strings.Replace(string(doc), "- rule", "- rule (edited)", 1)
	_ = os.WriteFile(md, []byte(edited), 0o644)
	_ = os.WriteFile(js, []byte(`{"permissions":{"deny":[]}}`), 0o644)
	ds := Check(items, nil)
	if ds[0].Status != Modified || !strings.Contains(ds[0].Detail, "by hand") || ds[1].Status != Missing {
		t.Fatalf("%+v", ds)
	}
	// whole section removed / whole file gone
	_ = os.WriteFile(md, []byte("# mine\n"), 0o644)
	_ = os.Remove(js)
	ds = Check(items, nil)
	if ds[0].Status != Missing || ds[1].Status != Missing {
		t.Fatalf("%+v", ds)
	}
}

func TestUnknownKindsAndProbes(t *testing.T) {
	it := Item{Category: "mcp", Key: "alpaca", Kind: KindMCP, Detail: map[string]string{"name": "alpaca"}}
	if ds := Check([]Item{it}, nil); ds[0].Status != Unknown {
		t.Fatalf("no probe must be Unknown, never silently OK: %+v", ds)
	}
	probe := map[string]Probe{KindMCP: func(i Item) (Status, string) { return Missing, "not registered: " + i.Detail["name"] }}
	if ds := Check([]Item{it}, probe); ds[0].Status != Missing || !strings.Contains(ds[0].Detail, "alpaca") {
		t.Fatalf("%+v", ds)
	}
	if ds := Check([]Item{{Kind: "weird"}}, nil); ds[0].Status != Unknown {
		t.Fatal("unknown kind must be Unknown")
	}
}

func TestDriftJSONRawEntries(t *testing.T) {
	dir := t.TempDir()
	js := filepath.Join(dir, "settings.json")
	entry := `{"matcher":"Bash","hooks":[{"type":"command","command":"rigfile"}]}`
	_ = os.WriteFile(js, []byte("{\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\n        \"matcher\": \"Bash\",\n        \"hooks\": [{\"type\": \"command\", \"command\": \"rigfile\"}]\n      }\n    ]\n  }\n}\n"), 0o644)
	it := Item{Category: "hook", Key: "guard", Kind: KindJSONRaw, Path: js, Detail: map[string]string{"list": "hooks.PreToolUse", "raw": entry}}
	if ds := Check([]Item{it}, nil); !Clean(ds) {
		t.Fatalf("pretty-printed entry must match its compacted form: %+v", ds)
	}
	_ = os.WriteFile(js, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"EDITED"}]}]}}`), 0o644)
	if ds := Check([]Item{it}, nil); ds[0].Status != Missing {
		t.Fatalf("an edited hook must be reported: %+v", ds)
	}
}
