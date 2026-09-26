//go:build windows

package secrets

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock (LockFileEx on a sidecar file) for the duration of a read-modify-write of
// the secrets file, so two `rigfile secrets set` processes cannot lose each other's writes. It is released when
// the returned function is called or the process exits.
func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		f.Close()
	}, nil
}
