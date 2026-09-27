package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/registry"
	"github.com/digitaldreamer3462/rigfile/internal/registry/blob"
	"github.com/digitaldreamer3462/rigfile/internal/registry/dbtest"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/sigverify"
	"github.com/digitaldreamer3462/rigfile/internal/source"
)

type noGitHub struct{}

func (noGitHub) AuthorizeURL(string, string) string { return "" }
func (noGitHub) Exchange(context.Context, string, string) (string, error) {
	return "", context.Canceled
}
func (noGitHub) User(context.Context, string) (registry.GitHubUser, error) {
	return registry.GitHubUser{}, context.Canceled
}

// startRegistry runs a real registry (Postgres, filesystem blobs, scan worker) on a local port.
func startRegistry(t *testing.T) (url string, store *registry.Store) {
	t.Helper()
	db := dbtest.New(t)
	store = registry.NewStore(db)
	srv := httptest.NewUnstartedServer(nil)
	cfg := registry.Config{PublicURL: "http://" + srv.Listener.Addr().String(), SessionTTL: time.Hour, TokenTTL: 24 * time.Hour,
		MaxUpload: 1 << 20, DeviceInterval: 1, GitHubID: "x", GitHubSecret: "x"}
	blobs := blob.FS{Root: t.TempDir()}
	s := registry.NewServer(cfg, store, blobs, noGitHub{}, nil)
	s.VerifySignature = fakeSigVerify
	srv.Config.Handler = s.Handler()
	srv.Start()
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sc := &registry.Scanner{Store: store, Blobs: blobs, Limits: source.DefaultLimits, Scan: func() (*scan.Scanner, error) { return scan.New(scan.Options{}) }}
	go func() {
		for ctx.Err() == nil {
			if did, _ := sc.RunOnce(ctx, "e2e"); !did {
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
	return cfg.PublicURL, store
}

func registryMachine(t *testing.T, regURL string) *machine {
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	if err := platform.WritePrivate(pass, []byte("correct horse battery staple\n")); err != nil {
		t.Fatal(err)
	}
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	m.env["RIGFILE_REGISTRY"] = regURL
	m.pollEvery = 100 * time.Millisecond
	return m
}

func regRig(t *testing.T, owner, name, version, extra string) string {
	d := t.TempDir()
	put(t, d, "rigfile.yaml", "apiVersion: rigfile.dev/v1\nname: "+owner+"/"+name+"\nversion: "+version+"\ndescription: A shared rig\ninstructions:\n  - {id: style, file: instructions/style.md}\ncommands:\n  - {path: commands/hi.md}\n"+extra, 0o644)
	put(t, d, "instructions/style.md", "# Style\n- be terse\n", 0o644)
	put(t, d, "commands/hi.md", "say hi", 0o644)
	return d
}

// approveWhenAsked plays the person at the website: it approves the pending device sign-in for login.
func approveWhenAsked(t *testing.T, store *registry.Store, login string) {
	go func() {
		u, err := store.UserByLogin(context.Background(), login)
		if err != nil {
			return
		}
		for i := 0; i < 100; i++ {
			var code string
			if err := store.DB.QueryRow(`SELECT user_code FROM device_codes WHERE status = 'pending' AND user_id IS NULL LIMIT 1`).Scan(&code); err == nil {
				_ = store.DecideDevice(context.Background(), code, u.ID, true)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

func TestRegistryEndToEndPublishThenPullOnAnotherMachine(t *testing.T) {
	regURL, store := startRegistry(t)
	ctx := context.Background()
	if _, err := store.UpsertUser(ctx, registry.GitHubUser{ID: 1, Login: "jia"}, false); err != nil {
		t.Fatal(err)
	}
	a := registryMachine(t, regURL) // the publisher's machine
	b := registryMachine(t, regURL) // someone else's machine

	// not signed in: publishing says so
	if r := a.run("", "publish", regRig(t, "jia", "shared", "1.0.0", ""), "--to-registry", "--ack-personal"); r.code != 1 || !strings.Contains(r.err, "rigfile login") {
		t.Fatalf("%+v", r)
	}
	// sign in with the device flow; "the person" approves in the browser
	approveWhenAsked(t, store, "jia")
	r := a.run("", "login")
	if r.code != 0 || !strings.Contains(r.out, "signed in to "+regURL+" as jia") || !strings.Contains(r.out, "-") {
		t.Fatalf("%+v", r)
	}
	if r = a.run("", "whoami"); r.code != 0 || !strings.Contains(r.out, "jia") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.out+r.err, "rgf_") {
		t.Fatal("the token was printed")
	}

	// publish (private by default): scanned by the registry, then published
	rig := regRig(t, "jia", "shared", "1.0.0", "")
	r = a.run("", "publish", rig, "--to-registry", "--ack-personal")
	if r.code != 0 || !strings.Contains(r.out, "published jia/shared@1.0.0") || !strings.Contains(r.out, "is private") {
		t.Fatalf("%+v", r)
	}
	// a stranger cannot pull a private rig, and cannot tell it exists
	if r = b.run("", "pull", "jia/shared", "--plan-only", "--no-git"); r.code != 1 || !strings.Contains(r.err, "no ") {
		t.Fatalf("%+v", r)
	}
	// the same version cannot be published twice
	if r = a.run("", "publish", rig, "--to-registry", "--ack-personal"); r.code != 1 || !strings.Contains(r.err, "immutable") {
		t.Fatalf("%+v", r)
	}
	// a new version, made public
	rig2 := regRig(t, "jia", "shared", "1.0.1", "")
	if r = a.run("", "publish", rig2, "--to-registry", "--public", "--ack-personal"); r.code != 0 || !strings.Contains(r.out, "is public") {
		t.Fatalf("%+v", r)
	}

	// the other machine pulls it: banner, plan, apply
	r = b.run("", "pull", "jia/shared", "--plan-only", "--no-git")
	for _, want := range []string{"Source: rigfile+" + regURL + "/jia/shared", "the Rigfile registry", "you did not write", "Rig: jia/shared@1.0.1",
		"Trust: jia/shared@1.0.1 by jia", "0 star(s)", "Trust: not signed", "ANALYSIS  no suspicious patterns"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("missing %q:\n%+v", want, r)
		}
	}
	if _, err := os.Stat(filepath.Join(b.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("plan-only wrote to the machine")
	}
	if r = b.run("", "pull", "jia/shared@^1.0", "--yes", "--no-git"); r.code != 0 || !strings.Contains(r.out, "applied") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(b.home, ".claude", "commands", "hi.md")); err != nil {
		t.Fatal("the pulled rig was not applied")
	}

	// a rig can inherit a registry rig: `from: [jia/shared@^1]` resolves through the registry and is pinned in the lock
	top := regRig(t, "bob", "top", "1.0.0", "from: [jia/shared@^1]\n")
	c := registryMachine(t, regURL)
	if r = c.run("", "apply", top, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	lock := string(mustRead(t, filepath.Join(top, "rigfile.lock")))
	if !strings.Contains(lock, `"source": "rigfile+`+regURL+`/jia/shared@^1"`) || !strings.Contains(lock, `"commit"`) {
		t.Fatalf("the registry layer must be pinned in the lock:\n%s", lock)
	}

	// an update: the publisher ships 1.1.0, the other machine follows
	if r = a.run("", "publish", regRig(t, "jia", "shared", "1.1.0", ""), "--to-registry", "--ack-personal"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r = b.run("", "update", "--plan-only", "--no-git"); r.code != 0 || !strings.Contains(r.out, "Update: ") || !strings.Contains(r.out, "1.1.0") {
		t.Fatalf("%+v", r)
	}

	// sign out revokes the token on the server
	if r = a.run("", "logout"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r = a.run("", "whoami"); r.code != 1 {
		t.Fatalf("a signed-out machine must not be recognised: %+v", r)
	}
	var revoked int
	_ = store.DB.QueryRow(`SELECT count(*) FROM api_tokens WHERE revoked_at IS NOT NULL`).Scan(&revoked)
	if revoked != 1 {
		t.Fatalf("revoked tokens: %d", revoked)
	}
}

func TestSecretsBackendFileSwitchDisablesTheKeychain(t *testing.T) {
	if !keyringDisabled(env{getenv: func(k string) string {
		if k == "RIGFILE_SECRETS_BACKEND" {
			return "file"
		}
		return ""
	}}) {
		t.Fatal("RIGFILE_SECRETS_BACKEND=file must disable the OS keychain")
	}
	if keyringDisabled(env{getenv: func(string) string { return "" }}) {
		t.Fatal("the keychain is the default")
	}
}

// fakeSigVerify stands in for Sigstore in CLI tests (the cryptography is tested in internal/sigverify): a "bundle" names a
// signer and the SHA-256 of the bytes it claims to have signed.
func fakeSigVerify(bundle, artifact []byte) (*sigverify.Result, error) {
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

func writeFakeBundle(t *testing.T, path, issuer, subject string, tarball []byte) {
	h := sha256.Sum256(tarball)
	b, _ := json.Marshal(map[string]string{"issuer": issuer, "subject": subject, "sha256": hex.EncodeToString(h[:])})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSignedRigsAreVerifiedOnThePullingMachineAndSignerChangesAreRefused(t *testing.T) {
	regURL, store := startRegistry(t)
	ctx := context.Background()
	u, err := store.UpsertUser(ctx, registry.GitHubUser{ID: 1, Login: "jia"}, false)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := store.CreateToken(ctx, u.ID, "t", 24*time.Hour)
	a := registryMachine(t, regURL)
	a.verify = fakeSigVerify
	b := registryMachine(t, regURL)
	b.verify = fakeSigVerify
	// store the publisher's token on machine A the way `rigfile login` would
	if r := a.run(tok+"\n", "secrets", "set", tokenRef(regURL)); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	dir := t.TempDir()
	workflow := "https://github.com/jia/rigs/.github/workflows/release.yml@refs/tags/v1.0.0"

	publishSigned := func(version, subject string) {
		rig := regRig(t, "jia", "signed", version, "")
		tarPath := filepath.Join(dir, "rig-"+version+".tgz")
		if r := a.run("", "publish", rig, "--write-tarball", tarPath, "--ack-personal"); r.code != 0 || !strings.Contains(r.out, "cosign sign-blob") {
			t.Fatalf("%+v", r)
		}
		tb, _ := os.ReadFile(tarPath)
		bundle := filepath.Join(dir, "bundle-"+version+".json")
		writeFakeBundle(t, bundle, sigverify.IssuerGitHubActions, subject, tb)
		args := []string{"publish", rig, "--to-registry", "--sign-bundle", bundle, "--ack-personal"}
		if version == "1.0.0" {
			args = append(args, "--public")
		}
		if r := a.run("", args...); r.code != 0 || !strings.Contains(r.out, "published jia/signed@"+version) {
			t.Fatalf("%+v", r)
		}
	}
	publishSigned("1.0.0", workflow)

	// a signature that does not match what is uploaded is refused by the registry
	rig := regRig(t, "jia", "signed", "1.0.5", "")
	bad := filepath.Join(dir, "bad.json")
	writeFakeBundle(t, bad, sigverify.IssuerGitHubActions, workflow, []byte("other bytes"))
	if r := a.run("", "publish", rig, "--to-registry", "--sign-bundle", bad, "--ack-personal"); r.code != 1 || !strings.Contains(r.err, "signature does not verify") {
		t.Fatalf("%+v", r)
	}

	// the puller verifies on their own machine and shows who signed
	r := b.run("", "pull", "jia/signed", "--plan-only", "--no-git", "--require-signature")
	if r.code != 0 || !strings.Contains(r.out, "Signature: verified on this machine; signed by the publisher's own GitHub Actions identity") || !strings.Contains(r.out, "release.yml") {
		t.Fatalf("%+v", r)
	}
	if r = b.run("", "pull", "jia/signed", "--yes", "--no-git", "--require-signature"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	// an unsigned version is refused by --require-signature, and shown as unsigned otherwise
	if r = a.run("", "publish", regRig(t, "jia", "unsigned", "1.0.0", ""), "--to-registry", "--public", "--ack-personal"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r = b.run("", "pull", "jia/unsigned", "--plan-only", "--no-git", "--require-signature"); r.code != 1 || !strings.Contains(r.err, "--require-signature") {
		t.Fatalf("%+v", r)
	}
	if r = b.run("", "pull", "jia/unsigned", "--plan-only", "--no-git"); r.code != 0 || !strings.Contains(r.out, "Signature: none") {
		t.Fatalf("%+v", r)
	}
	// the next version is signed by a DIFFERENT identity: `update` refuses until the person accepts the change
	publishSigned("1.1.0", "https://github.com/mallory/x/.github/workflows/r.yml@refs/tags/v1")
	if r = b.run("", "update", "--plan-only", "--no-git"); r.code != 1 || !strings.Contains(r.err, "the signer changed") {
		t.Fatalf("%+v", r)
	}
	if r = b.run("", "update", "--plan-only", "--no-git", "--accept-signer-change"); r.code != 0 || !strings.Contains(r.out, "NOT the publisher's identity") {
		t.Fatalf("%+v", r)
	}
}

func TestRegistryCollectionsForksAndChangesFromTheCLI(t *testing.T) {
	regURL, store := startRegistry(t)
	if _, err := store.UpsertUser(context.Background(), registry.GitHubUser{ID: 1, Login: "jia"}, false); err != nil {
		t.Fatal(err)
	}
	a := registryMachine(t, regURL)
	approveWhenAsked(t, store, "jia")
	if r := a.run("", "login"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "publish", regRig(t, "jia", "shared", "1.0.0", ""), "--to-registry", "--public", "--ack-personal"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	extra := "mcp_servers:\n  search:\n    command: npx\n    args: [\"-y\", \"search-mcp@1.0.0\"]\n"
	if r := a.run("", "publish", regRig(t, "jia", "shared", "1.1.0", extra), "--to-registry", "--ack-personal"); r.code != 0 {
		t.Fatalf("%+v", r)
	}

	// what changed between two registry versions, on the pulling side
	r := a.run("", "changes", "jia/shared@1.0.0", "jia/shared@1.1.0")
	if r.code != 0 || !strings.Contains(r.out, "Look at these before you accept") || !strings.Contains(r.out, "adds an MCP server that runs: npx -y search-mcp@1.0.0") {
		t.Fatalf("%+v", r)
	}

	// collections
	if r := a.run("", "collection", "create", "starters", "--title", "Starter rigs", "--description", "where to begin"); r.code != 0 || !strings.Contains(r.out, "Created jia/starters (public)") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "collection", "add", "starters", "jia/shared", "--note", "a small one"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "collection", "show", "jia/starters"); r.code != 0 || !strings.Contains(r.out, "Starter rigs") || !strings.Contains(r.out, "jia/shared@1.1.0   a small one") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "collection", "list"); r.code != 0 || !strings.Contains(r.out, "jia/starters") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "collection", "add", "starters", "jia/nothing"); r.code != 1 || !strings.Contains(r.err, "no such") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "collection", "rm", "starters", "jia/shared"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "collection", "delete", "starters"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "collection", "show", "jia/starters"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
	for _, args := range [][]string{{"collection"}, {"collection", "create"}, {"collection", "add", "x"}, {"collection", "nope"}} {
		if r := a.run("", args...); r.code != 2 {
			t.Fatalf("%v: %+v", args, r)
		}
	}

	// fork a registry rig, and extend one
	out := filepath.Join(t.TempDir(), "mine")
	if r := a.run("", "fork", "jia/shared@1.0.0", "--name", "me/mine", "--out", out); r.code != 0 || !strings.Contains(r.out, "forked from jia/shared@1.0.0") {
		t.Fatalf("%+v", r)
	}
	ext := filepath.Join(t.TempDir(), "ext")
	if r := a.run("", "fork", "jia/shared@1.0.0", "--name", "me/ext", "--out", ext, "--extend"); r.code != 0 || !strings.Contains(string(mustRead(t, filepath.Join(ext, "rigfile.yaml"))), "from:\n  - jia/shared@^1.0") {
		t.Fatalf("%+v", r)
	}
}

func TestRegistryOrganisationsFromTheCLI(t *testing.T) {
	regURL, store := startRegistry(t)
	ctx := context.Background()
	for i, l := range []string{"jia", "bob"} {
		if _, err := store.UpsertUser(ctx, registry.GitHubUser{ID: int64(i + 1), Login: l}, false); err != nil {
			t.Fatal(err)
		}
	}
	a, b := registryMachine(t, regURL), registryMachine(t, regURL)
	for _, x := range []struct {
		m     *machine
		login string
	}{{a, "jia"}, {b, "bob"}} {
		approveWhenAsked(t, store, x.login)
		if r := x.m.run("", "login"); r.code != 0 {
			t.Fatalf("%s: %+v", x.login, r)
		}
	}
	if r := a.run("", "org", "create", "acme", "--title", "Acme"); r.code != 0 || !strings.Contains(r.out, "you are its owner") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "org", "create", "bob"); r.code != 1 || !strings.Contains(r.err, "already") {
		t.Fatalf("%+v", r)
	}
	// bob is outside: cannot publish, cannot list members
	rig := regRig(t, "acme", "tool", "1.0.0", "")
	if r := b.run("", "publish", rig, "--to-registry", "--ack-personal"); r.code != 1 || !strings.Contains(r.err, "organisation") {
		t.Fatalf("%+v", r)
	}
	if r := b.run("", "org", "members", "acme"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "org", "add", "acme", "bob"); r.code != 0 || !strings.Contains(r.out, "bob is now a member of acme") {
		t.Fatalf("%+v", r)
	}
	if r := b.run("", "org", "list"); r.code != 0 || !strings.Contains(r.out, "acme  (member)") {
		t.Fatalf("%+v", r)
	}
	if r := b.run("", "publish", rig, "--to-registry", "--ack-personal"); r.code != 0 || !strings.Contains(r.out, "published acme/tool@1.0.0") {
		t.Fatalf("a member publishes under the organisation: %+v", r)
	}
	if r := a.run("", "org", "members", "acme"); r.code != 0 || !strings.Contains(r.out, "jia  (owner)") || !strings.Contains(r.out, "bob  (member)") {
		t.Fatalf("%+v", r)
	}
	if r := b.run("", "org", "rm", "acme", "bob"); r.code != 0 {
		t.Fatalf("leaving: %+v", r)
	}
	if r := b.run("", "pull", "acme/tool", "--plan-only", "--no-git"); r.code != 1 {
		t.Fatalf("a former member cannot pull the private rig: %+v", r)
	}
	for _, args := range [][]string{{"org"}, {"org", "create"}, {"org", "add", "acme"}, {"org", "nope"}} {
		if r := a.run("", args...); r.code != 2 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}
