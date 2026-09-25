//go:build !windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// WritePrivate atomically writes data to path with mode 0600 (owner read/write only), creating the
// parent directory with 0700 if needed. Secret-carrying files must use this (plan §7.3 rule 5).
// The data is written to a temp file in the same directory and renamed, so a crash never leaves a
// half-written secret file, and the final file is never briefly world-readable.
func WritePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rigfile-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
