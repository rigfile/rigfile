package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// ErrNotExist is returned by a Transport for a missing name.
var ErrNotExist = errors.New("vault: not found in the storage")

// Transport is the storage: dumb, and never trusted. Names are relative, slash-separated and validated.
type Transport interface {
	Read(name string) ([]byte, error)
	Write(name string, data []byte) error
	Remove(name string) error
	List(dir string) ([]string, error)
}

var transportName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

func checkName(name string) error {
	if !transportName.MatchString(name) || path.Clean(name) != name || strings.Contains(name, "..") || len(name) > 200 {
		return fmt.Errorf("vault: %q is not a valid storage name", name)
	}
	return nil
}

// DirTransport keeps the vault in a directory: a git working tree, a synced folder, a USB stick.
type DirTransport struct{ Root string }

func (d DirTransport) path(name string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	return filepath.Join(d.Root, filepath.FromSlash(name)), nil
}

// Read implements Transport.
func (d DirTransport) Read(name string) ([]byte, error) {
	p, err := d.path(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotExist
	}
	return b, err
}

// Write implements Transport atomically (temp file, then rename).
func (d DirTransport) Write(name string, data []byte) error {
	p, err := d.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := platform.RenameReplace(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Remove implements Transport (removing a missing name is not an error).
func (d DirTransport) Remove(name string) error {
	p, err := d.path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List implements Transport: the names of the regular files directly inside dir, sorted.
func (d DirTransport) List(dir string) ([]string, error) {
	if dir != "" {
		if err := checkName(dir); err != nil {
			return nil, err
		}
	}
	ents, err := os.ReadDir(filepath.Join(d.Root, filepath.FromSlash(dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.Type().IsRegular() && transportName.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
