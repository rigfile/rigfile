package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/regclient"
)

// RegistryFetcher pulls rigs from a Rigfile registry. The "commit" of a registry source is the SHA-256 of the version's
// tarball: Resolve returns it, Fetch downloads the version whose tarball has exactly that hash and refuses anything
// else, so a registry that swaps a version's content is detected.
type RegistryFetcher struct {
	Token  func(base string) string // the stored token for that registry, "" = anonymous
	Limits Limits

	yanked     bool
	yankedNote string
}

func (f *RegistryFetcher) client(spec Spec) *regclient.Client {
	return &regclient.Client{Base: spec.URL, Token: func() string {
		if f.Token == nil {
			return ""
		}
		return f.Token(spec.URL)
	}}
}

func splitPath(p string) (owner, name string) {
	owner, name, _ = strings.Cut(p, "/")
	return
}

// Resolve implements Fetcher.
func (f *RegistryFetcher) Resolve(ctx context.Context, spec Spec) (string, error) {
	owner, name := splitPath(spec.Path)
	v, err := f.client(spec).Resolve(ctx, owner, name, spec.Ref)
	if err != nil {
		return "", fmt.Errorf("registry %s: %w", spec.URL, err)
	}
	return v.SHA256, nil
}

// Fetch implements Fetcher.
func (f *RegistryFetcher) Fetch(ctx context.Context, spec Spec, commit, dest string) error {
	owner, name := splitPath(spec.Path)
	c := f.client(spec)
	info, err := c.Rig(ctx, owner, name)
	if err != nil {
		return fmt.Errorf("registry %s: %w", spec.URL, err)
	}
	version := ""
	for _, v := range info.Versions {
		if v.SHA256 == commit && (v.Status == "published" || v.Status == "yanked") {
			version = v.Version
			if v.Status == "yanked" {
				f.yanked, f.yankedNote = true, v.YankReason
			}
			break
		}
	}
	if version == "" {
		return fmt.Errorf("registry %s: no available version of %s has the content %s (it may have been removed)", spec.URL, spec.Path, short(commit))
	}
	rc, _, err := c.Download(ctx, owner, name, version)
	if err != nil {
		return err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != commit {
		return fmt.Errorf("%w: the registry served %s@%s with a different hash than it advertised; refusing", ErrChanged, spec.Path, version)
	}
	return Extract(bytes.NewReader(data), dest, false, f.Limits)
}

// LastYank reports whether the version fetched last was yanked, and why.
func (f *RegistryFetcher) LastYank() (bool, string) { return f.yanked, f.yankedNote }

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
