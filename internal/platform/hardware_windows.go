//go:build windows

package platform

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func readFileString(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

// freeBytes measures the volume of the nearest existing ancestor of p (the cache may not exist yet).
func freeBytes(p string) (uint64, error) {
	for {
		ptr, err := windows.UTF16PtrFromString(p)
		if err == nil {
			var avail, total, free uint64
			if err = windows.GetDiskFreeSpaceEx(ptr, &avail, &total, &free); err == nil {
				return avail, nil
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, err
		}
		p = parent
	}
}
