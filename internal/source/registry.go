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
	// Verify, when set, checks the version's Sigstore signature against the downloaded tarball, locally. It returns nil when
	// the version is unsigned; a SignerInfo with Err set when a signature is present but does not verify.
	Verify func(ctx context.Context, spec Spec, version string, tarball []byte) (*SignerInfo, error)

	signer *SignerInfo

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
	f.signer = nil
	if f.Verify != nil {
		si, err := f.Verify(ctx, spec, version, data)
		if err != nil {
			si = &SignerInfo{Err: err.Error()}
		}
		f.signer = si
	}
	return Extract(bytes.NewReader(data), dest, false, f.Limits)
}

// SignerInfo is the outcome of verifying a version's signature on this machine.
type SignerInfo struct {
	Subject, Issuer string
	ByPublisher     bool // the signer is the publisher's own GitHub Actions identity
	BundleSHA256    string
	Err             string // a signature is present but could not be verified
}

// String is the form recorded in state.json.
func (s *SignerInfo) String() string {
	if s == nil || s.Err != "" || s.Subject == "" {
		return ""
	}
	return s.Subject + " (" + s.Issuer + ")"
}

// LastSigner returns the verification outcome for the version fetched last (nil = not signed or not checked).
func (f *RegistryFetcher) LastSigner() *SignerInfo { return f.signer }

// LastYank reports whether the version fetched last was yanked, and why.
func (f *RegistryFetcher) LastYank() (bool, string) { return f.yanked, f.yankedNote }

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
