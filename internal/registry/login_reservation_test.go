package registry_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/registry"
)

func publishStub(t *testing.T, st *registry.Store, owner string, by *registry.User) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO rigs (owner, name, created_by) VALUES ($1, 'demo', $2)`, owner, by.ID); err != nil {
		t.Fatal(err)
	}
}

// Owner decision 2026-09-26: a vacated login that owns rigs stays with the account that renamed away from it.
func TestVacatedLoginStaysReservedWhenItOwnsRigs(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()
	alice := mkUser(t, st, 1, "alice")
	publishStub(t, st, "alice", alice)
	mkUser(t, st, 1, "alice2") // GitHub rename

	// a stranger takes the freed GitHub name and signs in
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 2, Login: "alice"}, false); !errors.Is(err, registry.ErrLoginReserved) {
		t.Fatalf("a stranger must not inherit the namespace: %v", err)
	}
	// the stranger cannot rename into it either
	mallory := mkUser(t, st, 3, "mallory")
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 3, Login: "alice"}, false); !errors.Is(err, registry.ErrLoginReserved) {
		t.Fatalf("renaming into a reserved login: %v", err)
	}
	if u, err := st.UserByLogin(ctx, "mallory"); err != nil || u.ID != mallory.ID {
		t.Fatalf("a refused rename must leave the account as it was: %v", err)
	}
	// nor can an organisation take it
	if _, err := st.CreateOrg(ctx, mallory, "alice", ""); !errors.Is(err, registry.ErrLoginReserved) {
		t.Fatalf("an organisation on a reserved login: %v", err)
	}
	// the original account may rename back
	back := mkUser(t, st, 1, "alice")
	if back.ID != alice.ID {
		t.Fatalf("%+v", back)
	}
	// ...and then its second name is free for others only if it never published under it
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 4, Login: "alice2"}, false); err != nil {
		t.Fatalf("a login that owns no rigs is not reserved: %v", err)
	}
}

func TestVacatedLoginWithoutRigsIsNotReserved(t *testing.T) {
	st, _ := newStore(t)
	mkUser(t, st, 1, "bob")
	mkUser(t, st, 1, "bob2")
	if _, err := st.UpsertUser(context.Background(), registry.GitHubUser{ID: 2, Login: "bob"}, false); err != nil {
		t.Fatalf("nothing to protect, so nothing is reserved (and cycling logins cannot squat names): %v", err)
	}
}

func TestReleaseLoginFreesAReservation(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()
	carol := mkUser(t, st, 1, "carol")
	publishStub(t, st, "carol", carol)
	mkUser(t, st, 1, "carol2")
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 2, Login: "carol"}, false); !errors.Is(err, registry.ErrLoginReserved) {
		t.Fatal(err)
	}
	if err := st.ReleaseLogin(ctx, "Carol"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 2, Login: "carol"}, false); err != nil {
		t.Fatalf("after an admin release: %v", err)
	}
	if err := st.ReleaseLogin(ctx, "carol"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("releasing what is not reserved: %v", err)
	}
}

func TestRenamesBeforeTheMigrationAreBackfilled(t *testing.T) {
	// the migration reserves owner strings that are nobody's current login; simulate one by hand and re-run its statement
	st, _ := newStore(t)
	ctx := context.Background()
	dave := mkUser(t, st, 1, "dave")
	publishStub(t, st, "dave", dave)
	if _, err := st.DB.Exec(`UPDATE users SET login = 'dave-new' WHERE id = $1`, dave.ID); err != nil { // trigger reserves it
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM login_reservations`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO login_reservations (login, user_id)
		SELECT r.owner, min(r.created_by) FROM rigs r WHERE r.org_id IS NULL
		AND NOT EXISTS (SELECT 1 FROM users u WHERE u.login = r.owner) AND NOT EXISTS (SELECT 1 FROM orgs o WHERE o.login = r.owner) GROUP BY r.owner`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, registry.GitHubUser{ID: 2, Login: "dave"}, false); !errors.Is(err, registry.ErrLoginReserved) {
		t.Fatal(err)
	}
}

func TestStrangerOnAVacatedLoginIsTurnedAwayAtSignIn(t *testing.T) {
	e := newEnv(t, nil)
	e.signIn("/") // the fake GitHub account: id 1001, "Jia"
	jia, err := e.store.UserByLogin(t.Context(), "jia")
	if err != nil {
		t.Fatal(err)
	}
	publishStub(t, e.store, "jia", jia)
	mkUser(t, e.store, 1001, "jia-renamed")

	e.gh.user = map[string]any{"id": 2002, "login": "Jia", "name": "Someone else"}
	c := e.client()
	resp, _ := c.Get(e.url("/login"))
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp, _ = c.Get(e.url("/auth/callback?code=good-code&state=" + loc.Query().Get("state")))
	page := body(t, resp)
	if resp.StatusCode != 403 || !strings.Contains(page, "reserved") {
		t.Fatalf("%d %s", resp.StatusCode, page)
	}
}
