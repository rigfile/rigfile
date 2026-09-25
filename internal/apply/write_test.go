package apply

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 9, 25, 10, 2, 3, 0, time.UTC) }

func newWriter(t *testing.T) (*Writer, string) {
	t.Helper()
	root := t.TempDir()
	return &Writer{BackupRoot: filepath.Join(root, "backups"), Now: fixedNow}, root
}

func TestNewFileIsCreatedPrivateWithoutBackup(t *testing.T) {
	w, root := newWriter(t)
	p := filepath.Join(root, "home", ".claude", "settings.json")
	res, err := w.WriteFile(p, []byte("{}\n"))
	if err != nil || !res.Changed || !res.Created || res.BackupPath != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("new file mode = %v, want 0600", st.Mode().Perm())
		}
	}
}

func TestExistingFileIsBackedUpThenReplaced(t *testing.T) {
	w, root := newWriter(t)
	p := filepath.Join(root, "settings.json")
	if err := os.WriteFile(p, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := w.WriteFile(p, []byte(`{"new":true}`))
	if err != nil || !res.Changed || res.Created || res.BackupPath == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got, _ := os.ReadFile(p); string(got) != `{"new":true}` {
		t.Fatalf("content = %s", got)
	}
	if b, _ := os.ReadFile(res.BackupPath); string(b) != `{"old":true}` {
		t.Fatalf("backup content = %s", b)
	}
	if !strings.Contains(res.BackupPath, "2026-09-25T10-02-03Z") {
		t.Fatalf("backup not in a timestamped snapshot: %s", res.BackupPath)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o644 {
			t.Fatalf("existing mode must be preserved, got %v", st.Mode().Perm())
		}
		bst, _ := os.Stat(res.BackupPath)
		if bst.Mode().Perm() != 0o600 {
			t.Fatalf("backup mode = %v, want 0600", bst.Mode().Perm())
		}
		dst, _ := os.Stat(filepath.Dir(res.BackupPath))
		if dst.Mode().Perm() != 0o700 {
			t.Fatalf("backup dir mode = %v, want 0700", dst.Mode().Perm())
		}
	}
}

func TestIdenticalContentIsANoOp(t *testing.T) {
	w, root := newWriter(t)
	p := filepath.Join(root, "s.json")
	_ = os.WriteFile(p, []byte("same"), 0o600)
	res, err := w.WriteFile(p, []byte("same"))
	if err != nil || res.Changed || res.BackupPath != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(w.BackupRoot); err == nil {
		t.Fatal("no backup directory should be created for a no-op")
	}
}

func TestSymlinkTargetIsWrittenAndLinkKept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	w, root := newWriter(t)
	real := filepath.Join(root, "dotfiles", "settings.json")
	_ = os.MkdirAll(filepath.Dir(real), 0o755)
	_ = os.WriteFile(real, []byte("v1"), 0o600)
	link := filepath.Join(root, "home", "settings.json")
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteFile(link, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	if got, _ := os.ReadFile(real); string(got) != "v2" {
		t.Fatalf("target not updated: %s", got)
	}
}

func TestTwoFilesInOneRunShareASnapshot(t *testing.T) {
	w, root := newWriter(t)
	a, b := filepath.Join(root, "a", "x.json"), filepath.Join(root, "b", "x.json")
	for _, p := range []string{a, b} {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("old"), 0o600)
	}
	ra, _ := w.WriteFile(a, []byte("new"))
	rb, _ := w.WriteFile(b, []byte("new"))
	if filepath.Dir(filepath.Dir(filepath.Dir(ra.BackupPath))) == "" || !strings.HasPrefix(ra.BackupPath, w.runDir) || !strings.HasPrefix(rb.BackupPath, w.runDir) {
		t.Fatalf("backups should share one snapshot dir: %s / %s", ra.BackupPath, rb.BackupPath)
	}
	if ra.BackupPath == rb.BackupPath {
		t.Fatal("distinct files must have distinct backup paths")
	}
}

func TestBackupRootRequired(t *testing.T) {
	if _, err := (&Writer{}).WriteFile(filepath.Join(t.TempDir(), "x"), []byte("v")); err == nil {
		t.Fatal("expected an error without BackupRoot")
	}
}
