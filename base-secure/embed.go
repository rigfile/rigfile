// Package basesecure embeds rigfile/base-secure, the always-on safety layer (RIGFILE_PLAN.md §8), so it works
// offline, cannot be impersonated from disk, and its version is the binary's version.
package basesecure

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/rigfile/rigfile/internal/manifest"
)

//go:embed rigfile.yaml instructions
var content embed.FS

// Name is the reserved layer name.
const Name = "rigfile/base-secure"

// Files returns every embedded file by forward-slash relative path.
func Files() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := content.ReadFile(p)
		out[p] = b
		return err
	})
	return out, err
}

// Extract writes the embedded layer into a fresh private temporary directory (0700; files 0600) and returns it
// with a cleanup function. The layer loader and the adapters read files from disk; extracting per run means
// what they read is exactly what this binary carries, nothing persists between runs, and `plan` leaves no
// trace on the machine.
func Extract() (dir string, cleanup func(), err error) {
	files, err := Files()
	if err != nil {
		return "", nil, err
	}
	l, err := manifest.Parse(files["rigfile.yaml"])
	if err != nil {
		return "", nil, fmt.Errorf("basesecure: embedded manifest is invalid: %w", err)
	}
	root, err := os.MkdirTemp("", "rigfile-base-secure-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(root) }
	dir = filepath.Join(root, l.Version)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		dst := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			cleanup()
			return "", nil, err
		}
		if err := os.WriteFile(dst, files[n], 0o600); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dir, cleanup, nil
}
