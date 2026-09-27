package registry_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rigfile/rigfile/internal/registry"
	"github.com/rigfile/rigfile/internal/registry/dbtest"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newStore(t *testing.T) (*registry.Store, *clock) {
	st := registry.NewStore(dbtest.New(t))
	c := &clock{t: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}
	st.Now = c.now
	return st, c
}

func mkUser(t *testing.T, st *registry.Store, id int64, login string) *registry.User {
	u, err := st.UpsertUser(context.Background(), registry.GitHubUser{ID: id, Login: login, Name: "N " + login}, false)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUsersFollowTheGitHubIDAndDisabledAccountsCannotSignIn(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()
	u := mkUser(t, st, 42, "Jia-X")
	if u.Login != "jia-x" {
		t.Fatalf("logins are lowercased: %q", u.Login)
	}
	// a GitHub rename keeps the same account
	u2 := mkUser(t, st, 42, "jia-renamed")
	if u2.ID != u.ID || u2.Login != "jia-renamed" {
		t.Fatalf("%+v", u2)
	}
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 43, Login: "bad_login"}, false); err == nil {
		t.Fatal("a login the namespace cannot hold must be refused")
	}
	if err := st.SetDisabled(ctx, "jia-renamed", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 42, Login: "jia-renamed"}, false); !errors.Is(err, registry.ErrDisabled) {
		t.Fatalf("%v", err)
	}
	if _, err := st.UserByLogin(ctx, "jia-renamed"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("a disabled user is not found")
	}
	if err := st.SetDisabled(ctx, "nobody", true); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSessionsExpireAndAreStoredHashed(t *testing.T) {
	st, clk := newStore(t)
	ctx := context.Background()
	u := mkUser(t, st, 1, "jia")
	cookie, csrf, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, csrf2, err := st.SessionUser(ctx, cookie)
	if err != nil || got.ID != u.ID || !registry.CSRFEqual(csrf, csrf2) {
		t.Fatalf("%v", err)
	}
	if registry.CSRFEqual("", "") || registry.CSRFEqual(csrf, csrf+"x") {
		t.Fatal("CSRFEqual")
	}
	var n int
	if err := st.DB.QueryRow(`SELECT count(*) FROM sessions WHERE id_hash = convert_to($1, 'UTF8')`, cookie).Scan(&n); err != nil || n != 0 {
		t.Fatal("the cookie value must not be stored")
	}
	clk.add(2 * time.Hour)
	if _, _, err := st.SessionUser(ctx, cookie); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	c2, _, _ := st.CreateSession(ctx, u.ID, time.Hour)
	_ = st.DeleteSession(ctx, c2)
	if _, _, err := st.SessionUser(ctx, c2); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("a deleted session must not resolve")
	}
	c3, _, _ := st.CreateSession(ctx, u.ID, time.Hour)
	_ = st.SetDisabled(ctx, "jia", true)
	if _, _, err := st.SessionUser(ctx, c3); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("a disabled account's session must stop working")
	}
	for _, bad := range []string{"", "x", strings.Repeat("a", 500)} {
		if _, _, err := st.SessionUser(ctx, bad); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("%q", bad)
		}
	}
}

func TestTokensAreHashedExpireAndRevoke(t *testing.T) {
	st, clk := newStore(t)
	ctx := context.Background()
	u := mkUser(t, st, 1, "jia")
	tok, err := st.CreateToken(ctx, u.ID, "cli", 30*24*time.Hour)
	if err != nil || !strings.HasPrefix(tok, "rgf_") || len(tok) < 40 {
		t.Fatalf("%q %v", tok, err)
	}
	// only the hash is in the database
	var stored []byte
	if err := st.DB.QueryRow(`SELECT token_hash FROM api_tokens`).Scan(&stored); err != nil || bytes.Contains(stored, []byte(tok)) || len(stored) != 32 {
		t.Fatalf("stored hash %d bytes, err %v", len(stored), err)
	}
	if got, err := st.TokenUser(ctx, tok); err != nil || got.Login != "jia" {
		t.Fatalf("%v", err)
	}
	for _, bad := range []string{"", "rgf_", "rgf_nope", tok + "x", strings.TrimPrefix(tok, "rgf_"), strings.Repeat("rgf_", 100)} {
		if _, err := st.TokenUser(ctx, bad); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("token %q must not resolve", bad)
		}
	}
	// revoke
	if err := st.RevokeToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TokenUser(ctx, tok); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("a revoked token must stop working")
	}
	// expiry
	tok2, _ := st.CreateToken(ctx, u.ID, "cli", time.Hour)
	clk.add(2 * time.Hour)
	if _, err := st.TokenUser(ctx, tok2); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("an expired token must stop working")
	}
	// a disabled account's tokens stop working
	tok3, _ := st.CreateToken(ctx, u.ID, "cli", time.Hour)
	_ = st.SetDisabled(ctx, "jia", true)
	if _, err := st.TokenUser(ctx, tok3); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("disabled account")
	}
}

func TestDeviceFlow(t *testing.T) {
	st, clk := newStore(t)
	ctx := context.Background()
	u := mkUser(t, st, 1, "jia")

	dc, uc, err := st.CreateDevice(ctx, 15*time.Minute, 5)
	if err != nil || len(uc) != 9 || uc[4] != '-' {
		t.Fatalf("%q %v", uc, err)
	}
	if registry.NormalizeUserCode(strings.ToLower(strings.ReplaceAll(uc, "-", " "))) != uc || registry.NormalizeUserCode("AEIO-1234") != "" {
		t.Fatal("NormalizeUserCode")
	}
	// pending, then polling too fast is slow_down and the interval grows
	if p, _ := st.PollDevice(ctx, dc, time.Hour); p.Error != "authorization_pending" {
		t.Fatalf("%+v", p)
	}
	if p, _ := st.PollDevice(ctx, dc, time.Hour); p.Error != "slow_down" || p.Interval != 10 {
		t.Fatalf("%+v", p)
	}
	clk.add(11 * time.Second)
	if p, _ := st.PollDevice(ctx, dc, time.Hour); p.Error != "authorization_pending" {
		t.Fatalf("%+v", p)
	}
	// the person approves on the website
	if !st.DeviceExists(ctx, uc) {
		t.Fatal("exists")
	}
	if err := st.DecideDevice(ctx, uc, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.DecideDevice(ctx, uc, u.ID, false); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("a code can be decided once")
	}
	clk.add(11 * time.Second)
	p, err := st.PollDevice(ctx, dc, 30*24*time.Hour)
	if err != nil || p.Token == "" {
		t.Fatalf("%+v %v", p, err)
	}
	if got, err := st.TokenUser(ctx, p.Token); err != nil || got.ID != u.ID {
		t.Fatalf("the issued token must work: %v", err)
	}
	// the code is single use
	clk.add(11 * time.Second)
	if p2, _ := st.PollDevice(ctx, dc, time.Hour); p2.Token != "" || p2.Error != "invalid_grant" {
		t.Fatalf("reuse: %+v", p2)
	}
	// denial
	dc2, uc2, _ := st.CreateDevice(ctx, 15*time.Minute, 5)
	_ = st.DecideDevice(ctx, uc2, u.ID, false)
	if p, _ := st.PollDevice(ctx, dc2, time.Hour); p.Error != "access_denied" {
		t.Fatalf("%+v", p)
	}
	// expiry, for the poller and for the approver
	dc3, uc3, _ := st.CreateDevice(ctx, time.Minute, 5)
	clk.add(2 * time.Minute)
	if st.DeviceExists(ctx, uc3) {
		t.Fatal("an expired code must not exist")
	}
	if err := st.DecideDevice(ctx, uc3, u.ID, true); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("an expired code cannot be approved")
	}
	if p, _ := st.PollDevice(ctx, dc3, time.Hour); p.Error != "expired_token" {
		t.Fatalf("%+v", p)
	}
	if p, _ := st.PollDevice(ctx, "no-such-device-code", time.Hour); p.Error != "invalid_grant" {
		t.Fatalf("%+v", p)
	}
	// device and user codes are not stored in clear
	_, uc4, _ := st.CreateDevice(ctx, time.Hour, 5)
	var n int
	_ = st.DB.QueryRow(`SELECT count(*) FROM device_codes WHERE encode(device_code_hash,'hex') = $1`, uc4).Scan(&n)
	if n != 0 {
		t.Fatal("hash column")
	}
}
