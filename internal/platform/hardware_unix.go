//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"syscall"
)

func readFileString(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

// freeBytes measures the volume of the nearest existing ancestor of p (the cache may not exist yet).
func freeBytes(p string) (uint64, error) {
	for {
		var st syscall.Statfs_t
		err := syscall.Statfs(p, &st)
		if err == nil {
			return uint64(st.Bavail) * uint64(st.Bsize), nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, err
		}
		p = parent
	}
}
