package main

import (
	"context"
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
