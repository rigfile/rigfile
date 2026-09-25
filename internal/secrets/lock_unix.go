//go:build unix

package secrets

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockFile takes an exclusive advisory lock (flock) for the duration of a read-modify-write of the
// secrets file, so two `rigfile secrets set` processes cannot lose each other's writes. The lock is
// released when the returned function is called or the process exits.
func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
