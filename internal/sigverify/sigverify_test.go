package sigverify

import (
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/testing/data"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const workflow = "https://github.com/ada/rigs/.github/workflows/release.yml@refs/tags/v1.0.0"

func virtual(t *testing.T) *ca.VirtualSigstore {
	old := sctThreshold
	sctThreshold = 0 // the virtual Sigstore issues certificates without SCTs
	t.Cleanup(func() { sctThreshold = old })
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestVerifyBundleAcceptsAGenuineSignatureAndReportsTheIdentity(t *testing.T) {
	vs := virtual(t)
	artifact := []byte("the rig tarball bytes")
	entity, err := vs.Sign(workflow, IssuerGitHubActions, artifact)
	if err != nil {
		t.Fatal(err)
	}
	res, err := verifyEntity(vs, entity, verify.WithArtifact(bytesReader(artifact)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Issuer != IssuerGitHubActions || res.Subject != workflow {
		t.Fatalf("%+v", res)
	}
	if !PublisherIdentity(res.Issuer, res.Subject, "ada") {
		t.Fatal("the publisher's own workflow is the publisher")
	}
}

func TestVerifyBundleRefusesWhatItShould(t *testing.T) {
	vs := virtual(t)
	artifact := []byte("the rig tarball bytes")
	entity, _ := vs.Sign(workflow, IssuerGitHubActions, artifact)

	// a different artifact
	if _, err := verifyEntity(vs, entity, verify.WithArtifact(bytesReader([]byte("tampered tarball")))); err == nil {
		t.Error("a signature over other bytes must not verify")
	}
	// a Sigstore that is not the trusted one
	other := virtual(t)
	if _, err := verifyEntity(other, entity, verify.WithArtifact(bytesReader(artifact))); err == nil {
		t.Error("an untrusted root must not verify")
	}
	// junk in place of a bundle
	for _, bad := range [][]byte{nil, []byte(""), []byte("not json"), []byte(`{"mediaType":"x"}`), make([]byte, 300<<10)} {
		if _, err := Verify(vs, bad, artifact); err == nil {
			t.Errorf("%q should not parse", string(bad[:min(len(bad), 20)]))
		}
	}
}

func TestPublisherIdentity(t *testing.T) {
	for _, c := range []struct {
		issuer, subject, login string
		want                   bool
	}{
		{IssuerGitHubActions, workflow, "ada", true},
		{IssuerGitHubActions, "https://github.com/ada/other-repo/.github/workflows/ci.yml@refs/heads/main", "ada", true},
		{IssuerGitHubActions, workflow, "bob", false},                                                                // someone else's repository
		{IssuerGitHubActions, "https://github.com/ada-evil/rigs/.github/workflows/r.yml@refs/tags/v1", "ada", false}, // prefix trick
		{IssuerGitHubActions, "https://github.com/ada/rigs/.github/workflows/r.yml@refs/pull/9/merge", "ada", false}, // not a branch or tag
		{IssuerGitHubActions, "https://evil.example/ada/rigs/.github/workflows/r.yml@refs/tags/v1", "ada", false},
		{IssuerGitHubOAuth, "ada@example.org", "ada", false}, // a person's e-mail login is not proof
		{"https://accounts.google.com", "ada@example.org", "ada", false},
		{IssuerGitHubActions, workflow, "", false},
		{IssuerGitHubActions, workflow, "ji.a", false},
	} {
		if got := PublisherIdentity(c.issuer, c.subject, c.login); got != c.want {
			t.Errorf("PublisherIdentity(%q, %q, %q) = %v, want %v", c.issuer, c.subject, c.login, got, c.want)
		}
	}
}

// The bundle JSON path, against a real public-good bundle and a real trusted root shipped with sigstore-go's tests.
func TestRealPublicGoodBundleThroughTheJSONPath(t *testing.T) {
	tr := data.TrustedRoot(t, "public-good.json")
	b := data.Bundle(t, "sigstore.js@2.0.0-provenance.sigstore.json")
	js, err := b.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	// this bundle attests a digest, so verify by digest; the digest of the artifact it was made for is inside the statement
	env, err := b.Envelope()
	if err != nil {
		t.Fatal(err)
	}
	st, err := env.Statement()
	if err != nil {
		t.Fatal(err)
	}
	for algo, hexd := range st.Subject[0].Digest {
		digest := mustHex(t, hexd)
		res, err := VerifyDigest(tr, js, algo, digest)
		if err != nil {
			t.Fatalf("a real Sigstore bundle must verify: %v", err)
		}
		if res.Issuer != IssuerGitHubActions || res.Subject != "https://github.com/sigstore/sigstore-js/.github/workflows/release.yml@refs/heads/main" {
			t.Fatalf("%+v", res)
		}
		// wrong digest
		digest[0] ^= 0xff
		if _, err := VerifyDigest(tr, js, algo, digest); err == nil {
			t.Fatal("a different digest must not verify")
		}
		break
	}
	// a trusted root that does not contain the signing CA
	if _, err := VerifyDigest(data.TrustedRoot(t, "scaffolding.json"), js, "sha512", make([]byte, 64)); err == nil {
		t.Fatal("wrong root")
	}
}

func TestTrustedRootFromFileAndMissingFile(t *testing.T) {
	if _, err := TrustedRoot("/no/such/file.json", ""); err == nil {
		t.Fatal("a missing trusted root file must be an error")
	}
}
