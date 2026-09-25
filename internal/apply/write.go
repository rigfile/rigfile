// Package apply performs the only file writes Rigfile makes: backup first, then an atomic replace
// (CLAUDE.md working agreement 6: no direct overwrites). Every change is journaled so a whole run can be
// rolled back exactly (`rigfile rollback`).
//
// Threat note: backups can contain secrets (settings files sometimes do), so backup files are 0600 in
// 0700 directories. The write target is resolved through symlinks so dotfile managers that symlink
// ~/.claude/settings.json keep working and we never replace the link itself with a regular file.
// Rollback refuses to overwrite a file that changed after Rigfile wrote it (unless forced) so it can
// never destroy the user's later edits.
package apply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// RunFile is the journal file inside a run's snapshot directory.
const RunFile = "run.json"

// FileRecord is one journaled change.
type FileRecord struct {
	Path        string   `json:"path"`              // absolute, symlinks resolved
	Backup      string   `json:"backup,omitempty"`  // where the previous content was saved ("" if the file was created)
	Created     bool     `json:"created,omitempty"` // the file did not exist before
	Deleted     bool     `json:"deleted,omitempty"` // Rigfile removed it (Backup holds the content)
	Mode        uint32   `json:"mode"`              // permission bits of the previous file (or the new one if created)
	BeforeSHA   string   `json:"beforeSha256,omitempty"`
	AfterSHA    string   `json:"afterSha256,omitempty"`
	CreatedDirs []string `json:"createdDirs,omitempty"` // directories Rigfile had to create, outermost first
}

// Run is a committed snapshot.
type Run struct {
	ID    string       `json:"id"`
	Time  string       `json:"time"`
	Note  string       `json:"note,omitempty"`
	Files []FileRecord `json:"files"`
}

// Writer writes files with backups.
type Writer struct {
	// BackupRoot is the directory under which each run gets a timestamped snapshot,
	// e.g. ~/.rigfile/backups. Required.
	BackupRoot string
	// Now is injectable for tests.
	Now func() time.Time

	runDir  string
	runTime time.Time
	journal []FileRecord
}

// Result describes what WriteFile did.
type Result struct {
	Path       string // the file that was (or would have been) written, after symlink resolution
	Changed    bool
	Created    bool   // the file did not exist before
	BackupPath string // "" if nothing needed backing up
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func resolve(path string) (string, error) {
	target := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		target = r
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return filepath.Abs(target)
}

// WriteFile replaces path with data. If the file exists and differs it is first copied to the
// backup snapshot. If content is identical nothing is touched.
func (w *Writer) WriteFile(path string, data []byte) (Result, error) {
	return w.WriteFileMode(path, data, 0)
}

// WriteFileMode is WriteFile with an explicit permission for NEW files (0 = private 0600). An existing
// file keeps its own mode. Use 0o755 for scripts that must be executable.
func (w *Writer) WriteFileMode(path string, data []byte, mode os.FileMode) (Result, error) {
	if w.BackupRoot == "" {
		return Result{}, errors.New("apply: BackupRoot is required")
	}
	abs, err := resolve(path)
	if err != nil {
		return Result{}, err
	}
	res := Result{Path: abs}
	rec := FileRecord{Path: abs, Mode: 0o600, AfterSHA: hashing.Bytes(data)}
	if mode != 0 {
		rec.Mode = uint32(mode.Perm())
	}

	old, err := os.ReadFile(abs)
	switch {
	case err == nil:
		if bytes.Equal(old, data) {
			return res, nil
		}
		if st, serr := os.Stat(abs); serr == nil {
			rec.Mode = uint32(st.Mode().Perm())
		}
		rec.BeforeSHA = hashing.Bytes(old)
		bp, berr := w.backup(abs, old)
		if berr != nil {
			return res, fmt.Errorf("backup before writing %s: %w", abs, berr)
		}
		res.BackupPath, rec.Backup = bp, bp
	case errors.Is(err, os.ErrNotExist):
		res.Created, rec.Created = true, true
	default:
		return res, err
	}

	rec.CreatedDirs, err = mkdirAllTracked(filepath.Dir(abs))
	if err != nil {
		return res, err
	}
	if err := atomicWrite(abs, data, os.FileMode(rec.Mode)); err != nil {
		return res, err
	}
	res.Changed = true
	w.journal = append(w.journal, rec)
	return res, nil
}

// Delete removes a file Rigfile owns, saving its content first. Deleting a missing file is a no-op.
func (w *Writer) Delete(path string) (Result, error) {
	if w.BackupRoot == "" {
		return Result{}, errors.New("apply: BackupRoot is required")
	}
	abs, err := resolve(path)
	if err != nil {
		return Result{}, err
	}
	res := Result{Path: abs}
	old, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	rec := FileRecord{Path: abs, Deleted: true, Mode: 0o600, BeforeSHA: hashing.Bytes(old)}
	if st, serr := os.Stat(abs); serr == nil {
		rec.Mode = uint32(st.Mode().Perm())
	}
	bp, err := w.backup(abs, old)
	if err != nil {
		return res, fmt.Errorf("backup before deleting %s: %w", abs, err)
	}
	res.BackupPath, rec.Backup = bp, bp
	if err := os.Remove(abs); err != nil {
		return res, err
	}
	res.Changed = true
	w.journal = append(w.journal, rec)
	return res, nil
}

// Journal returns the changes made so far in this run.
func (w *Writer) Journal() []FileRecord { return append([]FileRecord(nil), w.journal...) }

// Commit writes the run journal (run.json) next to the backups and returns the run ID. If nothing
// changed it writes nothing and returns "".
func (w *Writer) Commit(note string) (string, error) {
	if len(w.journal) == 0 || w.runDir == "" {
		// created-only runs have no backup dir yet
		if len(w.journal) == 0 {
			return "", nil
		}
		if err := w.ensureRunDir(); err != nil {
			return "", err
		}
	}
	run := Run{ID: filepath.Base(w.runDir), Time: w.runTime.UTC().Format(time.RFC3339), Note: note, Files: w.journal}
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(w.runDir, RunFile), append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	return run.ID, nil
}

func (w *Writer) ensureRunDir() error {
	if w.runDir != "" {
		return nil
	}
	w.runTime = w.now()
	base := filepath.Join(w.BackupRoot, w.runTime.UTC().Format("2006-01-02T15-04-05Z"))
	dir := base
	for n := 2; ; n++ { // two runs in the same second must not collide
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			break
		}
		dir = fmt.Sprintf("%s-%d", base, n)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	w.runDir = dir
	return nil
}

func (w *Writer) backup(abs string, content []byte) (string, error) {
	rel, err := platform.SafeRelative(abs)
	if err != nil {
		return "", err
	}
	if err := w.ensureRunDir(); err != nil {
		return "", err
	}
	dst := filepath.Join(w.runDir, "files", rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("backup %s already exists (two changes to the same file in one run)", dst)
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

// mkdirAllTracked creates dir and returns the directories it had to create, outermost first.
func mkdirAllTracked(dir string) ([]string, error) {
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			break
		}
		missing = append([]string{d}, missing...)
		if filepath.Dir(d) == d {
			break
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return missing, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".rigfile-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(e error) error { tmp.Close(); _ = os.Remove(name); return e }
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// --- runs and rollback ---------------------------------------------------------------------------

// ListRuns returns committed runs, newest first.
func ListRuns(backupRoot string) ([]Run, error) {
	ents, err := os.ReadDir(backupRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []Run
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(backupRoot, e.Name(), RunFile))
		if err != nil {
			continue // a directory without a journal is not a committed run
		}
		var r Run
		if json.Unmarshal(b, &r) == nil && r.ID != "" {
			runs = append(runs, r)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID > runs[j].ID })
	return runs, nil
}

// Outcome is one line of a rollback report.
type Outcome struct {
	Path   string
	Action string // restored | removed | skipped
	Reason string
}

// Rollback undoes a run in reverse order. Without force it skips (and reports) any file that no longer
// matches what Rigfile wrote, so the user's later edits are never overwritten.
func Rollback(backupRoot, runID string, force bool) ([]Outcome, error) {
	if runID == "" || filepath.Base(runID) != runID {
		return nil, fmt.Errorf("apply: invalid run id %q", runID)
	}
	b, err := os.ReadFile(filepath.Join(backupRoot, runID, RunFile))
	if err != nil {
		return nil, fmt.Errorf("apply: no run %q: %w", runID, err)
	}
	var run Run
	if err := json.Unmarshal(b, &run); err != nil {
		return nil, err
	}
	var out []Outcome
	for i := len(run.Files) - 1; i >= 0; i-- {
		r := run.Files[i]
		cur, rerr := os.ReadFile(r.Path)
		exists := rerr == nil
		curSHA := ""
		if exists {
			curSHA = hashing.Bytes(cur)
		}
		switch {
		case r.Deleted:
			if exists && !force {
				out = append(out, Outcome{r.Path, "skipped", "the file was re-created after Rigfile removed it"})
				continue
			}
			if err := restore(r); err != nil {
				return out, err
			}
			out = append(out, Outcome{r.Path, "restored", ""})
		case r.Created:
			if !exists {
				out = append(out, Outcome{r.Path, "skipped", "already gone"})
			} else if curSHA != r.AfterSHA && !force {
				out = append(out, Outcome{r.Path, "skipped", "modified since Rigfile created it"})
				continue
			} else {
				if err := os.Remove(r.Path); err != nil {
					return out, err
				}
				out = append(out, Outcome{r.Path, "removed", ""})
			}
			for j := len(r.CreatedDirs) - 1; j >= 0; j-- {
				_ = os.Remove(r.CreatedDirs[j]) // only succeeds when empty
			}
		default:
			if exists && curSHA != r.AfterSHA && !force {
				out = append(out, Outcome{r.Path, "skipped", "modified since Rigfile wrote it (use --force to overwrite)"})
				continue
			}
			if err := restore(r); err != nil {
				return out, err
			}
			out = append(out, Outcome{r.Path, "restored", ""})
		}
	}
	return out, nil
}

func restore(r FileRecord) error {
	old, err := os.ReadFile(r.Backup)
	if err != nil {
		return fmt.Errorf("apply: backup for %s is missing: %w", r.Path, err)
	}
	if hashing.Bytes(old) != r.BeforeSHA {
		return fmt.Errorf("apply: backup for %s is corrupted (hash mismatch); refusing to restore", r.Path)
	}
	if _, err := mkdirAllTracked(filepath.Dir(r.Path)); err != nil {
		return err
	}
	return atomicWrite(r.Path, old, os.FileMode(r.Mode))
}
