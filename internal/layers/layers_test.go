package layers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
)

// writeRig writes <root>/<owner>/<name>/rigfile.yaml and returns its dir.
func writeRig(t *testing.T, root, name, version, extra string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "apiVersion: rigfile.dev/v1\nname: " + name + "\nversion: " + version + "\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "rigfile.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func load(t *testing.T, dir string) *manifest.Loaded {
	t.Helper()
	l, err := manifest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func names(r *Result) string {
	var n []string
	for _, l := range r.Layers {
		n = append(n, l.Name)
	}
	return strings.Join(n, " > ")
}

func TestLinearisation(t *testing.T) {
	root := t.TempDir()
	writeRig(t, root, "x/d", "1.0.0", "")
	writeRig(t, root, "x/b", "1.0.0", "from: [x/d]\n")
	writeRig(t, root, "x/c", "1.0.0", "from: [x/d]\n") // diamond: b and c both inherit d
	top := writeRig(t, root, "x/top", "1.0.0", "from: [x/b, x/c]\n")
	r, err := Resolve(load(t, top), DirSource{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(r); got != "x/d > x/b > x/c > x/top" {
		t.Fatalf("order = %s (d must appear once, at its earliest position)", got)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "base-secure") {
		t.Fatalf("missing base-secure must be reported: %v", r.Warnings)
	}
}

func TestBaseSecureIsAlwaysLowestAndLocked(t *testing.T) {
	root := t.TempDir()
	writeRig(t, root, "rigfile/base-secure", "1.2.0", "")
	writeRig(t, root, "x/mid", "1.0.0", "")
	// base-secure listed LAST: position in the list is ignored (§2.1).
	top := writeRig(t, root, "x/top", "1.0.0", "from: [x/mid, 'rigfile/base-secure@^1']\n")
	r, err := Resolve(load(t, top), DirSource{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(r); got != "rigfile/base-secure > x/mid > x/top" {
		t.Fatalf("order = %s", got)
	}
	if !r.Layers[0].Locked || r.Layers[1].Locked || r.Layers[2].Locked {
		t.Fatalf("only base-secure may be locked: %+v", r.Layers)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", r.Warnings)
	}
	// unlisted: still applied first
	top2 := writeRig(t, root, "x/top2", "1.0.0", "")
	r, _ = Resolve(load(t, top2), DirSource{Root: root})
	if names(r) != "rigfile/base-secure > x/top2" {
		t.Fatalf("implicit base: %s", names(r))
	}
	// a version pin that the available base does not satisfy is an error
	top3 := writeRig(t, root, "x/top3", "1.0.0", "from: ['rigfile/base-secure@^2']\n")
	if _, err := Resolve(load(t, top3), DirSource{Root: root}); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("got %v", err)
	}
}

func TestCyclesAndDepth(t *testing.T) {
	root := t.TempDir()
	writeRig(t, root, "x/a", "1.0.0", "from: [x/b]\n")
	writeRig(t, root, "x/b", "1.0.0", "from: [x/a]\n")
	if _, err := Resolve(load(t, filepath.Join(root, "x", "a")), DirSource{Root: root}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: %v", err)
	}
	// a chain deeper than MaxDepth
	for i := 0; i <= MaxDepth+1; i++ {
		from := ""
		if i <= MaxDepth {
			from = "from: [x/l" + string(rune('a'+i+1)) + "]\n"
		}
		writeRig(t, root, "x/l"+string(rune('a'+i)), "1.0.0", from)
	}
	if _, err := Resolve(load(t, filepath.Join(root, "x", "la")), DirSource{Root: root}); err == nil || !strings.Contains(err.Error(), "deeper than") {
		t.Fatalf("depth: %v", err)
	}
}

func TestMissingLayerAndNameMismatchAndRange(t *testing.T) {
	root := t.TempDir()
	writeRig(t, root, "x/lib", "2.0.0", "")
	top := writeRig(t, root, "x/top", "1.0.0", "from: ['x/lib@^1']\n")
	if _, err := Resolve(load(t, top), DirSource{Root: root}); err == nil || !strings.Contains(err.Error(), "requires x/lib@^1 but found version 2.0.0") {
		t.Fatalf("range: %v", err)
	}
	top2 := writeRig(t, root, "x/top2", "1.0.0", "from: [x/ghost]\n")
	if _, err := Resolve(load(t, top2), DirSource{Root: root}); err == nil || !strings.Contains(err.Error(), "x/ghost is not available") {
		t.Fatalf("missing: %v", err)
	}
	// a directory that claims a different name than requested
	writeRig(t, root, "x/real", "1.0.0", "")
	_ = os.MkdirAll(filepath.Join(root, "x", "alias"), 0o755)
	b, _ := os.ReadFile(filepath.Join(root, "x", "real", "rigfile.yaml"))
	_ = os.WriteFile(filepath.Join(root, "x", "alias", "rigfile.yaml"), b, 0o644)
	top3 := writeRig(t, root, "x/top3", "1.0.0", "from: [x/alias]\n")
	if _, err := Resolve(load(t, top3), DirSource{Root: root}); err == nil || !strings.Contains(err.Error(), "declares name") {
		t.Fatalf("mismatch: %v", err)
	}
	// no root at all: layers are simply unavailable (top-only rigs still work)
	solo := writeRig(t, root, "x/solo", "1.0.0", "")
	r, err := Resolve(load(t, solo), DirSource{})
	if err != nil || names(r) != "x/solo" {
		t.Fatalf("solo: %v %v", err, r)
	}
}

func TestParseRef(t *testing.T) {
	ok := map[string]Ref{"a/b": {"a/b", ""}, "a/b@^1": {"a/b", "^1"}, "a/b@1.2.3": {"a/b", "1.2.3"}}
	for in, want := range ok {
		if got, err := ParseRef(in); err != nil || got != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "ab", "a/b/c", "/b", "a/"} {
		if _, err := ParseRef(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestSatisfies(t *testing.T) {
	yes := [][2]string{{"1.2.3", ""}, {"1.2.3", "1.2.3"}, {"1.2.9", "1.2"}, {"1.9.0", "^1"}, {"1.2.0", "^1.2"}, {"1.9.9", "^1.2"}, {"0.2.5", "^0.2.3"},
		{"1.2.9", "~1.2"}, {"1.9.0", "~1"}, {"1.0.0-beta", "1.0.0-beta"}}
	no := [][2]string{{"2.0.0", "^1"}, {"1.1.9", "^1.2"}, {"0.3.0", "^0.2.3"}, {"0.2.2", "^0.2.3"}, {"1.3.0", "~1.2"}, {"2.0.0", "~1"}, {"1.2.4", "1.2.3"},
		{"1.0.0-beta", "^1"}, {"x", "^1"}, {"1.0.0", "^x"}, {"1.0.0", "^1.2.3.4"}}
	for _, c := range yes {
		if !Satisfies(c[0], c[1]) {
			t.Errorf("%s should satisfy %q", c[0], c[1])
		}
	}
	for _, c := range no {
		if Satisfies(c[0], c[1]) {
			t.Errorf("%s should NOT satisfy %q", c[0], c[1])
		}
	}
}

// End to end: resolve then merge, with base-secure locking a hook.
func TestResolveThenMergeEnforcesLocks(t *testing.T) {
	root := t.TempDir()
	writeRig(t, root, "rigfile/base-secure", "1.0.0", "hooks:\n  - {id: guard, event: pre_tool_use, run: 'builtin:guard'}\n")
	top := writeRig(t, root, "x/top", "1.0.0", "hooks:\n  - {id: guard, event: stop, run: 'builtin:mine'}\n")
	r, err := Resolve(load(t, top), DirSource{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	_, err = merge.Merge(r.Layers, "claude-code")
	if err == nil || !strings.Contains(err.Error(), "locked by rigfile/base-secure") {
		t.Fatalf("got %v", err)
	}
}
