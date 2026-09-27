package lock

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/layers"
	"github.com/rigfile/rigfile/internal/manifest"
)

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const skill = "---\nname: s\ndescription: d\n---\nbody\n"

// makeRig builds a two-layer rig under root and returns the top rig's dir.
func makeRig(t *testing.T, root string) string {
	t.Helper()
	put(t, root, "layers/x/base/rigfile.yaml", "apiVersion: rigfile.dev/v1\nname: x/base\nversion: 1.0.0\ncommands:\n  - {path: commands/hi.md}\n")
	put(t, root, "layers/x/base/commands/hi.md", "hi")
	put(t, root, "top/rigfile.yaml", `apiVersion: rigfile.dev/v1
name: x/top
version: 2.0.0
from: [x/base]
instructions:
  - {id: style, file: instructions/style.md}
skills:
  - {path: skills/s}
  - {ref: 'skills.sh/a/b@1.0.0'}
agents:
  - {path: agents/rev.md}
hooks:
  - {id: n, event: stop, run: {macos: hooks/n.sh, linux: hooks/n.sh}}
  - {id: b, event: stop, run: 'builtin:x'}
`)
	put(t, root, "top/instructions/style.md", "# style")
	put(t, root, "top/skills/s/SKILL.md", skill)
	put(t, root, "top/agents/rev.md", "---\nname: rev\ndescription: d\n---\n")
	put(t, root, "top/hooks/n.sh", "#!/bin/sh\n")
	return filepath.Join(root, "top")
}

func build(t *testing.T, root string) *Lock {
	t.Helper()
	top, err := manifest.Load(filepath.Join(root, "top"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := layers.Resolve(top, layers.DirSource{Root: filepath.Join(root, "layers")})
	if err != nil {
		t.Fatal(err)
	}
	l, err := Build(res, map[string]string{"claude-code": "deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestBuildIsStableAndMachineIndependent(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	makeRig(t, r1)
	makeRig(t, r2)
	b1, _ := build(t, r1).Marshal()
	b2, _ := build(t, r2).Marshal()
	if !bytes.Equal(b1, b2) {
		t.Fatalf("same rig in two locations must produce identical lockfile bytes:\n%s\n---\n%s", b1, b2)
	}
	if !bytes.HasSuffix(b1, []byte("}\n")) || bytes.Contains(b1, []byte(r1)) {
		t.Fatal("lockfile must end with a newline and contain no absolute paths")
	}
	l := build(t, r1)
	if len(l.Layers) != 2 || l.Layers[0].Name != "x/base" || l.Layers[1].Name != "x/top" {
		t.Fatalf("layers: %+v", l.Layers)
	}
	cats := map[string]int{}
	for _, it := range l.Layers[1].Items {
		cats[it.Category]++
	}
	if cats["instruction"] != 1 || cats["skill"] != 2 || cats["agent"] != 1 || cats["hook"] != 2 {
		t.Fatalf("items (one hook item per OS script; builtin hooks are not listed; external skill refs are pinned by string): %+v", l.Layers[1].Items)
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	root := t.TempDir()
	makeRig(t, root)
	l := build(t, root)
	b, _ := l.Marshal()
	back, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if d := Verify(back, l); len(d) != 0 {
		t.Fatalf("round trip differs: %v", d)
	}
	if _, err := Parse([]byte(`{"lockVersion": 99}`)); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("version: %v", err)
	}
	if _, err := Parse([]byte(`{"lockVersion": 1, "surprise": true}`)); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := Parse([]byte(`nope`)); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

func TestVerifyDetectsEveryKindOfChange(t *testing.T) {
	root := t.TempDir()
	makeRig(t, root)
	pinned := build(t, root)

	cases := []struct {
		name   string
		mutate func()
		want   string
	}{
		{"skill file edited", func() { put(t, root, "top/skills/s/SKILL.md", skill+"more\n") }, `skill "s" (skills/s) changed`},
		{"instruction edited", func() { put(t, root, "top/instructions/style.md", "# changed") }, `instruction "user/style"`},
		{"hook script edited", func() { put(t, root, "top/hooks/n.sh", "#!/bin/sh\necho hi\n") }, "hook"},
		{"base layer file edited", func() { put(t, root, "layers/x/base/commands/hi.md", "HI") }, "layer x/base: command"},
		{"manifest edited", func() {
			b, _ := os.ReadFile(filepath.Join(root, "top/rigfile.yaml"))
			put(t, root, "top/rigfile.yaml", string(b)+"description: hello\n")
		}, "layer x/top: rigfile.yaml changed"},
		{"version bumped", func() {
			b, _ := os.ReadFile(filepath.Join(root, "top/rigfile.yaml"))
			put(t, root, "top/rigfile.yaml", strings.Replace(string(b), "version: 2.0.0", "version: 2.0.1", 1))
		}, "version 2.0.0 -> 2.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root2 := t.TempDir()
			makeRig(t, root2)
			// mutate a fresh copy relative to the pinned lock
			old := root
			root = root2
			defer func() { root = old }()
			c.mutate()
			d := Verify(pinned, build(t, root2))
			if !strings.Contains(strings.Join(d, "\n"), c.want) {
				t.Fatalf("want a difference containing %q, got %v", c.want, d)
			}
			if err := Mismatch(d); err == nil || !strings.Contains(err.Error(), "rigfile lock") {
				t.Fatalf("Mismatch: %v", err)
			}
		})
	}
	if d := Verify(pinned, build(t, root)); len(d) != 0 || Mismatch(d) != nil {
		t.Fatalf("an unchanged rig must verify clean: %v", d)
	}
}

func TestVerifyDetectsMergedModelAndLayerSetChanges(t *testing.T) {
	root := t.TempDir()
	makeRig(t, root)
	a := build(t, root)
	b := build(t, root)
	b.Merged["claude-code"] = "different"
	if d := Verify(a, b); len(d) != 1 || !strings.Contains(d[0], "merged model for claude-code changed") {
		t.Fatalf("%v", d)
	}
	c := build(t, root)
	c.Layers = c.Layers[1:] // base layer dropped
	if d := Verify(a, c); len(d) != 1 || !strings.Contains(d[0], "x/base is in the lockfile but no longer part") {
		t.Fatalf("%v", d)
	}
	if d := Verify(c, a); len(d) != 1 || !strings.Contains(d[0], "x/base is new") {
		t.Fatalf("%v", d)
	}
}

func TestBuildFailsOnEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	makeRig(t, root)
	outside := t.TempDir()
	put(t, outside, "SKILL.md", skill)
	_ = os.RemoveAll(filepath.Join(root, "top/skills/s"))
	if err := os.Symlink(outside, filepath.Join(root, "top/skills/s")); err != nil {
		t.Skip("symlinks unavailable")
	}
	top, err := manifest.Load(filepath.Join(root, "top"))
	if err != nil {
		t.Fatal(err)
	}
	res, _ := layers.Resolve(top, layers.DirSource{Root: filepath.Join(root, "layers")})
	if _, err := Build(res, nil); err == nil || !strings.Contains(err.Error(), "outside the rig") {
		t.Fatalf("got %v", err)
	}
}
