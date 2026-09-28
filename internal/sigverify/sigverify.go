// Package sigverify verifies Sigstore bundles (docs/trust.md §3): a signature over an artifact made with a short-lived
// Fulcio certificate, recorded in the Rekor transparency log, checked against the Sigstore trusted root. Rigfile only
// verifies; cosign (or another Sigstore client) creates the bundle. Both the registry (at upload) and the CLI (on every
// pull, with its own copy of the trusted root) call this package, so neither has to take the other's word.
package sigverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// Issuers Rigfile knows how to tie to a publisher.
const (
	IssuerGitHubActions = "https://token.actions.githubusercontent.com"
	IssuerGitHubOAuth   = "https://github.com/login/oauth"
)

// Result is what a successful verification established about the signer.
type Result struct {
	Issuer       string `json:"issuer"`
	Subject      string `json:"subject"`
	BundleSHA256 string `json:"bundle_sha256"`
}

// String is the form recorded in state.json and shown on screen.
func (r *Result) String() string { return r.Subject + " (" + r.Issuer + ")" }

// ErrNoBundle means there is nothing to verify.
var ErrNoBundle = errors.New("sigverify: no signature bundle")

// ParseBundle reads a Sigstore bundle in its JSON form (what `cosign sign-blob --bundle` writes).
func ParseBundle(data []byte) (*bundle.Bundle, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ErrNoBundle
	}
	if len(data) > 256<<10 {
		return nil, errors.New("sigverify: the bundle is implausibly large")
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(data); err != nil {
		return nil, fmt.Errorf("sigverify: not a valid Sigstore bundle: %w", err)
	}
	return &b, nil
}

// sctThreshold is the number of signed certificate timestamps required. Production keeps it at 1; only the tests that use
// sigstore-go's virtual Sigstore (whose certificates carry none) lower it.
var sctThreshold = 1

func verifier(trusted root.TrustedMaterial) (*verify.Verifier, error) {
	// a certificate timestamp, a transparency-log entry and an observer timestamp are all required: an offline forgery
	// cannot fake the log
	opts := []verify.VerifierOption{verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1)}
	if sctThreshold > 0 {
		opts = append(opts, verify.WithSignedCertificateTimestamps(sctThreshold))
	}
	return verify.NewVerifier(trusted, opts...)
}

// Verify checks that bundleJSON is a valid Sigstore signature over artifact by SOME identity, and returns that identity.
// Whether the identity is the right one is a separate question (see PublisherIdentity).
func Verify(trusted root.TrustedMaterial, bundleJSON, artifact []byte) (*Result, error) {
	b, err := ParseBundle(bundleJSON)
	if err != nil {
		return nil, err
	}
	res, err := verifyEntity(trusted, b, verify.WithArtifact(bytes.NewReader(artifact)))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bundleJSON)
	res.BundleSHA256 = hex.EncodeToString(sum[:])
	return res, nil
}

// VerifyDigest is Verify for a bundle that signs a digest (an in-toto attestation) rather than the artifact itself.
func VerifyDigest(trusted root.TrustedMaterial, bundleJSON []byte, algo string, digest []byte) (*Result, error) {
	b, err := ParseBundle(bundleJSON)
	if err != nil {
		return nil, err
	}
	return verifyEntity(trusted, b, verify.WithArtifactDigest(algo, digest))
}

func verifyEntity(trusted root.TrustedMaterial, entity verify.SignedEntity, art verify.ArtifactPolicyOption) (*Result, error) {
	v, err := verifier(trusted)
	if err != nil {
		return nil, err
	}
	res, err := v.Verify(entity, verify.NewPolicy(art, verify.WithoutIdentitiesUnsafe()))
	if err != nil {
		return nil, fmt.Errorf("sigverify: the signature does not verify: %w", err)
	}
	if res.Signature == nil || res.Signature.Certificate == nil {
		return nil, errors.New("sigverify: the signature carries no certificate identity (keyless signatures only)")
	}
	c := res.Signature.Certificate
	return &Result{Issuer: c.Issuer, Subject: c.SubjectAlternativeName}, nil
}

var loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// PublisherIdentity reports whether a verified signer is the publisher: a GitHub Actions workflow in a repository owned
// by the publisher's GitHub account. Any other identity (another account's workflow, a person's e-mail login) is a valid
// signature but not proof of who published the rig, and is presented that way.
func PublisherIdentity(issuer, subject, publisherLogin string) bool {
	if issuer != IssuerGitHubActions || !loginRe.MatchString(publisherLogin) {
		return false
	}
	re := regexp.MustCompile(`^https://github\.com/` + regexp.QuoteMeta(publisherLogin) + `/[A-Za-z0-9._-]+/\.github/workflows/[^@]+@refs/(heads|tags)/.+$`)
	return re.MatchString(subject)
}

// TrustedRoot returns the Sigstore trusted root. With path set, that JSON file is used (offline and for pinning);
// otherwise the public-good root is fetched through Sigstore's TUF root of trust and cached under cacheDir. The fetch needs
// the network (docs/owner-checklist.md: not exercised by the automated tests).
func TrustedRoot(path, cacheDir string) (root.TrustedMaterial, error) {
	if path != "" {
		tr, err := root.NewTrustedRootFromPath(path)
		if err != nil {
			return nil, fmt.Errorf("sigverify: reading the trusted root %s: %w", path, err)
		}
		return tr, nil
	}
	opts := tuf.DefaultOptions()
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, err
		}
		opts.CachePath = filepath.Join(cacheDir, "sigstore-tuf")
	}
	tr, err := root.FetchTrustedRootWithOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("sigverify: could not fetch the Sigstore trusted root (network needed): %w", err)
	}
	return tr, nil
}
