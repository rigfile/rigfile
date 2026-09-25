// Package apply performs the only file writes Rigfile makes: backup first, then an atomic replace
// (CLAUDE.md working agreement 6: no direct overwrites).
//
// Threat note: backups can contain secrets (settings files sometimes do), so backup files are 0600 in
// 0700 directories. The write target is resolved through symlinks so dotfile managers that symlink
// ~/.claude/settings.json keep working and we never replace the link itself with a regular file.
package apply

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// Writer writes files with backups.
type Writer struct {
	// BackupRoot is the directory under which each run gets a timestamped snapshot,
	// e.g. ~/.rigfile/backups. Required.
	BackupRoot string
	// Now is injectable for tests.
	Now func() time.Time
	// runDir is fixed on first use so all files of one run share a snapshot directory.
	runDir string
}

// Result describes what WriteFile did.
type Result struct {
	Path       string // the file that was (or would have been) written, after symlink resolution
	Changed    bool
	Created    bool   // the file did not exist before
	BackupPath string // "" if nothing needed backing up
}

// WriteFile replaces path with data. If the file exists and differs it is first copied to the
// backup snapshot. If content is identical nothing is touched.
func (w *Writer) WriteFile(path string, data []byte) (Result, error) {
	if w.BackupRoot == "" {
		return Result{}, errors.New("apply: BackupRoot is required")
	}
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return Result{}, err
	}
	res := Result{Path: abs}

	mode := os.FileMode(0o600) // new files are private by default
	old, err := os.ReadFile(abs)
	switch {
	case err == nil:
		if bytes.Equal(old, data) {
			return res, nil
		}
		if st, serr := os.Stat(abs); serr == nil {
			mode = st.Mode().Perm()
		}
		bp, berr := w.backup(abs, old)
		if berr != nil {
			return res, fmt.Errorf("backup before writing %s: %w", abs, berr)
		}
		res.BackupPath = bp
	case errors.Is(err, os.ErrNotExist):
		res.Created = true
	default:
		return res, err
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return res, err
	}
	if err := atomicWrite(abs, data, mode); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

func (w *Writer) backup(abs string, content []byte) (string, error) {
	rel, err := platform.SafeRelative(abs)
	if err != nil {
		return "", err
	}
	if w.runDir == "" {
		now := time.Now
		if w.Now != nil {
			now = w.Now
		}
		w.runDir = filepath.Join(w.BackupRoot, now().UTC().Format("2006-01-02T15-04-05Z"))
	}
	dst := filepath.Join(w.runDir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("backup %s already exists (two writes to the same file in one run)", dst)
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return "", err
	}
	return dst, nil
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
