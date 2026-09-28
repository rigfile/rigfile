package registry_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/registry"
	"github.com/rigfile/rigfile/internal/sigverify"
)

// fakeBundle stands in for a Sigstore bundle in server tests (the cryptography is tested in internal/sigverify): it
// names a signer and the artifact hash it claims to have signed.
func fakeBundle(issuer, subject string, artifact []byte) []byte {
	h := sha256.Sum256(artifact)
	b, _ := json.Marshal(map[string]string{"issuer": issuer, "subject": subject, "sha256": hex.EncodeToString(h[:])})
	return b
}

func fakeVerify(bundle, artifact []byte) (*sigverify.Result, error) {
	var m map[string]string
	if err := json.Unmarshal(bundle, &m); err != nil {
		return nil, errors.New("not a bundle")
	}
	h := sha256.Sum256(artifact)
	if m["sha256"] != hex.EncodeToString(h[:]) {
		return nil, errors.New("the signature is over different bytes")
	}
	return &sigverify.Result{Issuer: m["issuer"], Subject: m["subject"]}, nil
}

func (c *client) uploadSigned(owner, name string, tarball, bundle []byte) (int, map[string]any) {
	c.e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	w, _ := mw.CreateFormFile("tarball", "rig.tar.gz")
	_, _ = w.Write(tarball)
	if bundle != nil {
		bw, _ := mw.CreateFormFile("bundle", "bundle.json")
		_, _ = bw.Write(bundle)
	}
	_ = mw.Close()
	s, _, b := c.do("POST", "/v1/rigs/"+owner+"/"+name+"/versions", body.Bytes(), mw.FormDataContentType())
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return s, out
}

const adaWorkflow = "https://github.com/ada/rigs/.github/workflows/release.yml@refs/tags/v1.0.0"

func TestSignedUploadsAreVerifiedStoredAndShown(t *testing.T) {
	e := newEnv(t, nil)
	e.s.VerifySignature = fakeVerify
	_, tok := e.userToken("ada", 1001)
	c := e.as(tok)
	tb := rigTar(t, goodRig("ada", "demo", "1.0.0"))

	// a bundle that does not verify is refused and creates nothing
	if s, out := c.uploadSigned("ada", "demo", tb, fakeBundle(sigverify.IssuerGitHubActions, adaWorkflow, []byte("other bytes"))); s != 422 {
		t.Fatalf("%d %v", s, out)
	}
	if s, out := c.uploadSigned("ada", "demo", tb, []byte("garbage")); s != 422 {
		t.Fatalf("%d %v", s, out)
	}
	var n int
	_ = e.store.DB.QueryRow(`SELECT count(*) FROM versions`).Scan(&n)
	if n != 0 {
		t.Fatal("a refused signature must not create a version")
	}
	// signed by the publisher's own workflow
	if s, out := c.uploadSigned("ada", "demo", tb, fakeBundle(sigverify.IssuerGitHubActions, adaWorkflow, tb)); s != 202 {
		t.Fatalf("%d %v", s, out)
	}
	e.scanAll()
	_, page := c.get("/v1/rigs/ada/demo/versions/1.0.0/trust")
	var tr registry.Trust
	if json.Unmarshal(page, &tr) != nil || !tr.Signature.Signed || !tr.Signature.ByPublisher || tr.Signature.Subject != adaWorkflow || tr.Signature.Issuer != sigverify.IssuerGitHubActions {
		t.Fatalf("%s", page)
	}
	// the bundle is downloadable so the CLI can verify it itself
	s, b := c.get("/v1/rigs/ada/demo/versions/1.0.0/bundle")
	if s != 200 || !strings.Contains(string(b), "release.yml") {
		t.Fatalf("%d %s", s, b)
	}
	// signed by somebody else's workflow: valid, but not the publisher
	tb2 := rigTar(t, goodRig("ada", "demo", "1.1.0"))
	if s, out := c.uploadSigned("ada", "demo", tb2, fakeBundle(sigverify.IssuerGitHubActions, "https://github.com/mallory/x/.github/workflows/r.yml@refs/tags/v1", tb2)); s != 202 {
		t.Fatalf("%d %v", s, out)
	}
	e.scanAll()
	_, page = c.get("/v1/rigs/ada/demo/versions/1.1.0/trust")
	tr = registry.Trust{}
	_ = json.Unmarshal(page, &tr)
	if !tr.Signature.Signed || tr.Signature.ByPublisher {
		t.Fatalf("someone else's signature must not read as the publisher's: %s", page)
	}
	// unsigned uploads still work, and say so
	if s, _ := c.upload("ada", "demo", rigTar(t, goodRig("ada", "demo", "1.2.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if s, _ := c.get("/v1/rigs/ada/demo/versions/1.2.0/bundle"); s != 404 {
		t.Fatalf("an unsigned version has no bundle: %d", s)
	}
	// an oversized bundle part, and an unknown part, are refused
	big := bytes.Repeat([]byte("x"), 300<<10)
	if s, _ := c.uploadSigned("ada", "demo", rigTar(t, goodRig("ada", "demo", "1.3.0")), big); s != 413 && s != 400 {
		t.Fatalf("a huge bundle: %d", s)
	}
	// a page for the signed version shows the badge
	publishPublicSigned(t, e, c)
}

func publishPublicSigned(t *testing.T, e *env, c *client) {
	if s, _, b := c.do("POST", "/v1/rigs/ada/demo/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatalf("%d %s", s, b)
	}
	code, page := getPage(t, e, nil, "/r/ada/demo/v/1.0.0")
	if code != 200 || !strings.Contains(page, "Signed by the publisher") || !strings.Contains(page, "release.yml") {
		t.Fatalf("the rig page must show the signature:\n%s", page)
	}
	_, page = getPage(t, e, nil, "/r/ada/demo/v/1.1.0")
	if !strings.Contains(page, "not by the publisher") {
		t.Fatalf("the page must not present a stranger's signature as the publisher's:\n%s", page)
	}
}

func TestPopularRigPolicy(t *testing.T) {
	e := newEnv(t, func(c *registry.Config) { c.PopularStars = 2 })
	e.s.VerifySignature = fakeVerify
	_, tok := e.userToken("ada", 1001)
	c := e.as(tok)
	adm := admin(t, e)
	publishPublic(t, e, c, "ada", "demo", "1.0.0", goodRig("ada", "demo", "1.0.0")) // not yet popular: unsigned is fine
	for i, l := range []string{"a1", "a2"} {
		u, _ := e.userToken(l, int64(500+i))
		if err := e.store.SetStar(t.Context(), "ada", "demo", u, true); err != nil {
			t.Fatal(err)
		}
	}
	// now popular: an unsigned new version is rejected, and the reason is explained
	if s, _ := c.upload("ada", "demo", rigTar(t, goodRig("ada", "demo", "1.1.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if got := c.versionStatus("ada", "demo", "1.1.0"); got != "rejected" {
		t.Fatalf("unsigned version of a popular rig: %s", got)
	}
	_, b := c.get("/v1/rigs/ada/demo/versions/1.1.0")
	if !strings.Contains(string(b), `"kind":"policy"`) || !strings.Contains(string(b), "GitHub Actions identity") {
		t.Fatalf("%s", b)
	}
	// signed by the publisher, but the publisher is not verified yet
	tb := rigTar(t, goodRig("ada", "demo", "1.2.0"))
	if s, _ := c.uploadSigned("ada", "demo", tb, fakeBundle(sigverify.IssuerGitHubActions, adaWorkflow, tb)); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	_, b = c.get("/v1/rigs/ada/demo/versions/1.2.0")
	if !strings.Contains(string(b), `"status":"rejected"`) || !strings.Contains(string(b), "verified publisher") {
		t.Fatalf("%s", b)
	}
	// verified and signed: published
	if err := e.store.SetVerified(t.Context(), "ada", "person", "known", adm); err != nil {
		t.Fatal(err)
	}
	tb = rigTar(t, goodRig("ada", "demo", "1.3.0"))
	if s, _ := c.uploadSigned("ada", "demo", tb, fakeBundle(sigverify.IssuerGitHubActions, adaWorkflow, tb)); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if got := c.versionStatus("ada", "demo", "1.3.0"); got != "published" {
		t.Fatalf("signed and verified: %s", got)
	}
	// a private rig is never subject to the policy
	if s, _ := c.upload("ada", "quiet", rigTar(t, goodRig("ada", "quiet", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if got := c.versionStatus("ada", "quiet", "1.0.0"); got != "published" {
		t.Fatal(got)
	}
	_ = http.StatusOK
}

func TestSigstoreConfig(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	base := map[string]string{"RIGFILE_REGISTRY_PUBLIC_URL": "https://r.example.test", "RIGFILE_REGISTRY_DATABASE_URL": "x", "RIGFILE_REGISTRY_BLOB": "fs:/x",
		"RIGFILE_REGISTRY_GITHUB_CLIENT_ID": "a", "RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET": "b", "RIGFILE_REGISTRY_POPULAR_STARS": "25", "RIGFILE_REGISTRY_SIGSTORE_ROOT": "/etc/trusted_root.json"}
	c, err := registry.ConfigFromEnv(get(base), nil)
	if err != nil || c.PopularStars != 25 || c.SigstoreRoot != "/etc/trusted_root.json" {
		t.Fatalf("%+v %v", c, err)
	}
	base["RIGFILE_REGISTRY_POPULAR_STARS"] = "many"
	if _, err := registry.ConfigFromEnv(get(base), nil); err == nil {
		t.Fatal("a non-numeric threshold must be refused")
	}
}
