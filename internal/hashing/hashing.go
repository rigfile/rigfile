// Package hashing computes the content hashes Rigfile stores in rigfile.lock and state.json.
// Trees hash deterministically: sorted relative paths with forward slashes, per-file size, sha256 and
// executable bit. Symlinks inside a tree are an error: a rig item must not be able to smuggle in a
// pointer to something outside itself.
package hashing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Bytes returns the hex sha256 of b.
func Bytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// File returns the hex sha256 of a regular file's content.
func File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Entry is one file of a tree.
type Entry struct {
	Path   string // relative, forward slashes
	Size   int64
	SHA256 string
	Exec   bool
}

// TreeEntries lists a directory's regular files in deterministic order.
func TreeEntries(dir string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("hashing: %s is a symlink; not allowed inside a rig item", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("hashing: %s is not a regular file", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := File(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, Entry{Path: filepath.ToSlash(rel), Size: info.Size(), SHA256: sum, Exec: info.Mode()&0o111 != 0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Tree returns one hash for a whole directory.
func Tree(dir string) (string, error) {
	es, err := TreeEntries(dir)
	if err != nil {
		return "", err
	}
	return TreeOf(es), nil
}

// TreeOf hashes a precomputed entry list.
func TreeOf(es []Entry) string {
	var b strings.Builder
	for _, e := range es {
		x := "-"
		if e.Exec {
			x = "x"
		}
		fmt.Fprintf(&b, "%s\x00%d\x00%s\x00%s\n", e.Path, e.Size, e.SHA256, x)
	}
	return Bytes([]byte(b.String()))
}
