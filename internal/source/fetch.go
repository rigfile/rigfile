package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigfile/rigfile/internal/manifest"
)

// Fetcher resolves and downloads one kind of source.
type Fetcher interface {
	Resolve(ctx context.Context, spec Spec) (commit string, err error)
	Fetch(ctx context.Context, spec Spec, commit, dest string) error
}

// Client picks a fetcher per source kind and owns the cache.
type Client struct {
	CacheDir string
	HTTPS    Fetcher // GitHub and GitLab; nil = the real services
	Git      Fetcher // other git URLs; nil = the git binary
	Registry Fetcher // Rigfile registries; nil = an anonymous RegistryFetcher
	Getenv   func(string) string
}

// Fetched is a pulled rig on disk.
type Fetched struct {
	Spec       Spec
	Dir        string // the rig directory (holds rigfile.yaml), inside the cache
	Commit     string
	TreeSHA256 string
	Yanked     bool // the pulled version was yanked by its publisher
	YankReason string
	Signer     *SignerInfo // registry sources: the local verification of the version's signature
}

// Pin is what a lock or state file remembers about a source.
type Pin struct {
	Commit     string
	TreeSHA256 string
}

// ErrChanged means the fetched content no longer matches its pin.
var ErrChanged = errors.New("source content changed")

// Get resolves spec (or uses pin.Commit when pinned), fetches it, verifies it against the pin, and returns the cached
// directory. A pinned fetch never re-resolves the ref: a moved tag cannot change what is applied.
func (c *Client) Get(ctx context.Context, spec Spec, pin *Pin) (*Fetched, error) {
	f := c.fetcher(spec)
	commit := ""
	if pin != nil {
		commit = pin.Commit
	} else {
		var err error
		if commit, err = f.Resolve(ctx, spec); err != nil {
			return nil, err
		}
	}
	if pin != nil && pin.TreeSHA256 != "" {
		if dir := c.cachePath(pin.TreeSHA256); dir != "" {
			if ok, _ := c.valid(dir, pin.TreeSHA256); ok {
				return &Fetched{Spec: spec, Dir: dir, Commit: commit, TreeSHA256: pin.TreeSHA256}, nil
			}
		}
	}
	if err := os.MkdirAll(c.CacheDir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(c.CacheDir, ".fetch-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	tree := filepath.Join(tmp, "tree")
	if err := f.Fetch(ctx, spec, commit, tree); err != nil {
		return nil, err
	}
	root := tree
	if spec.Subdir != "" {
		root, err = manifest.PathInside(tree, spec.Subdir)
		if err != nil {
			return nil, fmt.Errorf("source: subdirectory %q: %w", spec.Subdir, err)
		}
	}
	if st, err := os.Stat(filepath.Join(root, manifest.FileName)); err != nil || st.IsDir() {
		return nil, fmt.Errorf("source: %s has no %s%s", spec, manifest.FileName, where(spec.Subdir))
	}
	sum, err := TreeHash(root)
	if err != nil {
		return nil, err
	}
	if pin != nil && pin.TreeSHA256 != "" && sum != pin.TreeSHA256 {
		return nil, fmt.Errorf("%w: %s at commit %s hashes to %s but %s was pinned", ErrChanged, spec, commit[:12], sum[:12], pin.TreeSHA256[:12])
	}
	dest := c.cachePath(sum)
	if _, err := os.Stat(dest); err == nil {
		if ok, _ := c.valid(dest, sum); !ok {
			_ = os.RemoveAll(dest)
		}
	}
	if _, err := os.Stat(dest); err != nil {
		if err := os.Rename(root, dest); err != nil {
			return nil, err
		}
	}
	out := &Fetched{Spec: spec, Dir: dest, Commit: commit, TreeSHA256: sum}
	if y, ok := f.(interface{ LastYank() (bool, string) }); ok {
		out.Yanked, out.YankReason = y.LastYank()
	}
	if sg, ok := f.(interface{ LastSigner() *SignerInfo }); ok {
		out.Signer = sg.LastSigner()
	}
	return out, nil
}

func where(sub string) string {
	if sub == "" {
		return ""
	}
	return " in " + sub
}

func (c *Client) cachePath(sum string) string {
	if len(sum) < 16 {
		return ""
	}
	return filepath.Join(c.CacheDir, "sha256-"+sum)
}

// valid re-hashes a cache entry: files edited after the fetch make it invalid.
func (c *Client) valid(dir, sum string) (bool, error) {
	got, err := TreeHash(dir)
	return err == nil && got == sum, err
}

func (c *Client) fetcher(spec Spec) Fetcher {
	switch spec.Kind {
	case Registry:
		if c.Registry != nil {
			return c.Registry
		}
		return &RegistryFetcher{}
	case GitHub, GitLab:
		if c.HTTPS != nil {
			return c.HTTPS
		}
		return &HTTPS{Getenv: c.Getenv}
	}
	if c.Git != nil {
		return c.Git
	}
	return &GitFetcher{}
}
