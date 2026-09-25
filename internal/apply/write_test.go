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

// --- journal, commit, rollback ----------------------------------------------------------------

func TestJournalCommitAndRollbackRestoreEverythingExactly(t *testing.T) {
	w, root := newWriter(t)
	existing := filepath.Join(root, "home", ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(existing), 0o755)
	_ = os.WriteFile(existing, []byte(`{"user":"edit"}`), 0o640)
	newFile := filepath.Join(root, "home", ".claude", "skills", "pdf", "SKILL.md") // parent dirs do not exist yet
	doomed := filepath.Join(root, "home", ".claude", "commands", "old.md")
	_ = os.MkdirAll(filepath.Dir(doomed), 0o755)
	_ = os.WriteFile(doomed, []byte("delete me"), 0o644)

	if _, err := w.WriteFile(existing, []byte(`{"user":"edit","rig":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteFile(newFile, []byte("skill")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Delete(doomed); err != nil {
		t.Fatal(err)
	}
	id, err := w.Commit("test run")
	if err != nil || id == "" {
		t.Fatalf("commit: %q %v", id, err)
	}
	if len(w.Journal()) != 3 {
		t.Fatalf("journal = %+v", w.Journal())
	}
	runs, _ := ListRuns(w.BackupRoot)
	if len(runs) != 1 || runs[0].ID != id || runs[0].Note != "test run" || len(runs[0].Files) != 3 {
		t.Fatalf("runs = %+v", runs)
	}

	out, err := Rollback(w.BackupRoot, id, false)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, o := range out {
		actions[filepath.Base(o.Path)] = o.Action
	}
	if actions["settings.json"] != "restored" || actions["SKILL.md"] != "removed" || actions["old.md"] != "restored" {
		t.Fatalf("outcomes = %+v", out)
	}
	if b, _ := os.ReadFile(existing); string(b) != `{"user":"edit"}` {
		t.Fatalf("settings not restored: %s", b)
	}
	if b, _ := os.ReadFile(doomed); string(b) != "delete me" {
		t.Fatal("deleted file not restored")
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Fatal("created file must be removed")
	}
	if _, err := os.Stat(filepath.Join(root, "home", ".claude", "skills")); !os.IsNotExist(err) {
		t.Fatal("directories Rigfile created must be removed when empty")
	}
	if _, err := os.Stat(filepath.Join(root, "home", ".claude")); err != nil {
		t.Fatal("pre-existing directories must be left alone")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(existing); st.Mode().Perm() != 0o640 {
			t.Fatalf("mode not restored: %v", st.Mode().Perm())
		}
	}
}

func TestRollbackNeverClobbersLaterUserEdits(t *testing.T) {
	w, root := newWriter(t)
	p := filepath.Join(root, "settings.json")
	_ = os.WriteFile(p, []byte("v0"), 0o600)
	created := filepath.Join(root, "new.md")
	_, _ = w.WriteFile(p, []byte("v1 by rigfile"))
	_, _ = w.WriteFile(created, []byte("by rigfile"))
	id, _ := w.Commit("")

	_ = os.WriteFile(p, []byte("v2 user edit"), 0o600)
	_ = os.WriteFile(created, []byte("user edit"), 0o600)
	out, err := Rollback(w.BackupRoot, id, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if o.Action != "skipped" || !strings.Contains(o.Reason, "modified since") {
			t.Fatalf("must skip edited files: %+v", o)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "v2 user edit" {
		t.Fatal("user edit was overwritten")
	}
	if b, _ := os.ReadFile(created); string(b) != "user edit" {
		t.Fatal("user-edited created file was removed")
	}
	// --force does what it says
	if _, err := Rollback(w.BackupRoot, id, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "v0" {
		t.Fatalf("force restore failed: %s", b)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("force should remove the created file")
	}
}

func TestRollbackDetectsCorruptedBackup(t *testing.T) {
	w, root := newWriter(t)
	p := filepath.Join(root, "s.json")
	_ = os.WriteFile(p, []byte("original"), 0o600)
	res, _ := w.WriteFile(p, []byte("changed"))
	id, _ := w.Commit("")
	_ = os.WriteFile(res.BackupPath, []byte("tampered backup"), 0o600)
	if _, err := Rollback(w.BackupRoot, id, false); err == nil || !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("a tampered backup must not be restored: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "changed" {
		t.Fatal("file must be untouched when the backup is bad")
	}
}

func TestRollbackRejectsBadIDsAndCommitOfNothing(t *testing.T) {
	w, _ := newWriter(t)
	for _, id := range []string{"", "../x", "a/b", "nope"} {
		if _, err := Rollback(w.BackupRoot, id, false); err == nil {
			t.Errorf("run id %q should be rejected", id)
		}
	}
	if id, err := w.Commit("nothing"); id != "" || err != nil {
		t.Fatalf("an empty run must write nothing: %q %v", id, err)
	}
	if runs, _ := ListRuns(w.BackupRoot); len(runs) != 0 {
		t.Fatal("no runs expected")
	}
}

func TestTwoRunsInTheSameSecondGetDistinctIDs(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "f")
	_ = os.WriteFile(p, []byte("0"), 0o600)
	var ids []string
	for i := 1; i <= 2; i++ {
		w := &Writer{BackupRoot: filepath.Join(root, "b"), Now: fixedNow}
		_, _ = w.WriteFile(p, []byte(strings.Repeat("x", i)))
		id, err := w.Commit("")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] {
		t.Fatalf("run ids collided: %v", ids)
	}
	runs, _ := ListRuns(filepath.Join(root, "b"))
	if len(runs) != 2 || runs[0].ID <= runs[1].ID {
		t.Fatalf("runs must be listed newest first: %+v", runs)
	}
	// undoing the newest then the older run walks back to the original content
	if _, err := Rollback(filepath.Join(root, "b"), runs[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Rollback(filepath.Join(root, "b"), runs[1].ID, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "0" {
		t.Fatalf("content = %q", b)
	}
}

func TestDeleteOfMissingFileIsANoOp(t *testing.T) {
	w, root := newWriter(t)
	res, err := w.Delete(filepath.Join(root, "ghost"))
	if err != nil || res.Changed || len(w.Journal()) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}
