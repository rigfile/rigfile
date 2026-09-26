package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/registry"
	"github.com/digitaldreamer3462/rigfile/internal/registry/dbtest"
)

func run2(t *testing.T, dsn string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	e := env{getenv: func(k string) string {
		if k == "RIGFILE_REGISTRY_DATABASE_URL" {
			return dsn
		}
		return ""
	}, out: &out, err: &errb}
	return run(context.Background(), args, e), out.String(), errb.String()
}

func TestAdminTools(t *testing.T) {
	dsn := dbtest.DSN(t)
	if c, _, e := run2(t, dsn, "migrate"); c != 0 {
		t.Fatal(e)
	}
	if c, out, e := run2(t, dsn, "admin", "create-user", "--login", "jia", "--github-id", "1"); c != 0 || !strings.Contains(out, "account jia ready") {
		t.Fatalf("%s %s", out, e)
	}
	c, tok, e := run2(t, dsn, "admin", "token", "--login", "jia")
	tok = strings.TrimSpace(tok)
	if c != 0 || !strings.HasPrefix(tok, "rgf_") {
		t.Fatalf("%q %s", tok, e)
	}
	if c, _, _ := run2(t, dsn, "admin", "token", "--login", "nobody"); c != 1 {
		t.Fatal("unknown account")
	}
	// the token works, then disabling the account stops it
	db, err := registry.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := registry.NewStore(db)
	if _, err := st.TokenUser(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := run2(t, dsn, "admin", "disable-user", "--login", "jia", "--reason", "abuse"); c != 0 {
		t.Fatal("disable")
	}
	if _, err := st.TokenUser(context.Background(), tok); err == nil {
		t.Fatal("a disabled account's token must stop working")
	}
	if c, out, _ := run2(t, dsn, "admin", "reports"); c != 0 || !strings.Contains(out, "no open reports") {
		t.Fatal(out)
	}
	if c, out, _ := run2(t, dsn, "admin", "audit"); c != 0 || !strings.Contains(out, "admin.token") || !strings.Contains(out, "admin.disable-user") {
		t.Fatalf("the operator's actions must be audited:\n%s", out)
	}
	if c, _, _ := run2(t, dsn, "admin", "takedown", "--rig", "jia/none"); c != 1 {
		t.Fatal("takedown needs a reason and an existing rig")
	}
	// trust and incident tools
	if c, out, e := run2(t, dsn, "admin", "verify-publisher", "--login", "jia", "--kind", "organisation", "--reason", "checked"); c != 1 {
		t.Fatalf("a disabled account cannot be verified: %d %s %s", c, out, e)
	}
	run2(t, dsn, "admin", "enable-user", "--login", "jia")
	if c, out, _ := run2(t, dsn, "admin", "verify-publisher", "--login", "jia", "--kind", "organisation", "--reason", "checked"); c != 0 || !strings.Contains(out, "verified jia as organisation") {
		t.Fatal(out)
	}
	if c, _, _ := run2(t, dsn, "admin", "verify-publisher", "--login", "jia", "--kind", "celebrity"); c != 1 {
		t.Fatal("kind is validated")
	}
	if c, out, _ := run2(t, dsn, "admin", "held"); c != 0 || !strings.Contains(out, "nothing is held") {
		t.Fatal(out)
	}
	if c, _, _ := run2(t, dsn, "admin", "publishing", "pause"); c != 1 {
		t.Fatal("a pause needs a reason")
	}
	if c, out, _ := run2(t, dsn, "admin", "publishing", "pause", "--reason", "incident 1"); c != 0 || !strings.Contains(out, "paused") {
		t.Fatal(out)
	}
	if paused, why := st.PublishingPaused(context.Background()); !paused || why != "incident 1" {
		t.Fatal("the pause must be visible to the server")
	}
	if c, _, _ := run2(t, dsn, "admin", "publishing", "resume"); c != 0 {
		t.Fatal("resume")
	}
	if c, _, _ := run2(t, dsn, "admin", "revoke-tokens"); c != 1 {
		t.Fatal("revoke-tokens needs a target")
	}
	if c, out, _ := run2(t, dsn, "admin", "revoke-tokens", "--all"); c != 0 || !strings.Contains(out, "revoked") {
		t.Fatal(out)
	}
	if c, _, _ := run2(t, dsn, "bogus"); c != 2 {
		t.Fatal("unknown command")
	}
}

func TestServeRefusesAnInsecureConfiguration(t *testing.T) {
	var out, errb bytes.Buffer
	e := env{getenv: func(k string) string {
		return map[string]string{"RIGFILE_REGISTRY_PUBLIC_URL": "http://registry.example.test", "RIGFILE_REGISTRY_DATABASE_URL": "postgres://x", "RIGFILE_REGISTRY_BLOB": "fs:/tmp/x",
			"RIGFILE_REGISTRY_GITHUB_CLIENT_ID": "a", "RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET": "b"}[k]
	}, out: &out, err: &errb}
	if c := run(context.Background(), []string{"serve"}, e); c != 1 || !strings.Contains(errb.String(), "https") {
		t.Fatalf("%d %s", c, errb.String())
	}
}
