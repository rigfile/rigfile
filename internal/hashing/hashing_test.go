package hashing

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func write(t *testing.T, dir, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestBytesAndFile(t *testing.T) {
	// sha256("abc")
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if Bytes([]byte("abc")) != abc {
		t.Fatal("wrong sha256")
	}
	d := t.TempDir()
	write(t, d, "f", "abc", 0o644)
	if got, err := File(filepath.Join(d, "f")); err != nil || got != abc {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := File(filepath.Join(d, "missing")); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestTreeIsDeterministicAndSensitive(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	// same content, created in different orders
	write(t, a, "SKILL.md", "x", 0o644)
	write(t, a, "scripts/run.sh", "echo", 0o755)
	write(t, a, "refs/a.md", "a", 0o644)
	write(t, b, "refs/a.md", "a", 0o644)
	write(t, b, "scripts/run.sh", "echo", 0o755)
	write(t, b, "SKILL.md", "x", 0o644)
	ha, err := Tree(a)
	if err != nil {
		t.Fatal(err)
	}
	if hb, _ := Tree(b); ha != hb {
		t.Fatal("identical trees must hash identically regardless of creation order or location")
	}
	write(t, b, "refs/a.md", "changed", 0o644)
	if hb, _ := Tree(b); hb == ha {
		t.Fatal("content change must change the hash")
	}
	write(t, b, "refs/a.md", "a", 0o644)
	write(t, b, "extra.txt", "", 0o644)
	if hb, _ := Tree(b); hb == ha {
		t.Fatal("an added (even empty) file must change the hash")
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(filepath.Join(b, "extra.txt"))
		_ = os.Chmod(filepath.Join(b, "scripts/run.sh"), 0o644)
		if hb, _ := Tree(b); hb == ha {
			t.Fatal("losing the executable bit must change the hash")
		}
	}
}

func TestTreeRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	d := t.TempDir()
	write(t, d, "a", "x", 0o644)
	if err := os.Symlink("/etc/hosts", filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Tree(d); err == nil {
		t.Fatal("a symlink inside a tree must be rejected")
	}
}

func TestEmptyTree(t *testing.T) {
	h, err := Tree(t.TempDir())
	if err != nil || h != Bytes(nil) {
		t.Fatalf("%s %v", h, err)
	}
}
