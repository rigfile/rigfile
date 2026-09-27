package registry_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rigfile/rigfile/internal/registry"
)

func putJSON(c *client, method, path string, v any) int {
	s, _, _ := c.do(method, path, jsonBody(v), "application/json")
	return s
}

func TestOrganisations(t *testing.T) {
	e := newEnv(t, nil)
	_, jia := e.userToken("jia", 1)
	_, bob := e.userToken("bob", 2)
	_, eve := e.userToken("eve", 3)
	_, carol := e.userToken("carol", 4)
	J, B, E, C, anon := e.as(jia), e.as(bob), e.as(eve), e.as(carol), e.as("")
	step := func() { e.clk.add(time.Minute) } // API rate limits

	// ---- namespaces are shared between people and organisations
	if s, _, b := J.do("POST", "/v1/orgs", jsonBody(map[string]string{"login": "acme", "name": "Acme Inc"}), "application/json"); s != 201 {
		t.Fatalf("%d %s", s, b)
	}
	for name, login := range map[string]string{"duplicate": "acme", "an existing person": "bob", "reserved": "openai"} {
		if s := putJSON(J, "POST", "/v1/orgs", map[string]string{"login": login}); s != 409 {
			t.Errorf("%s: %d", name, s)
		}
	}
	if s := putJSON(J, "POST", "/v1/orgs", map[string]string{"login": "Not OK!"}); s != 400 {
		t.Fatalf("%d", s)
	}
	if s := putJSON(anon, "POST", "/v1/orgs", map[string]string{"login": "anon-org"}); s != 401 {
		t.Fatalf("%d", s)
	}
	if _, err := e.store.UpsertUser(t.Context(), registry.GitHubUser{ID: 99, Login: "acme"}, false); !errors.Is(err, registry.ErrNameTaken) {
		t.Fatalf("a person cannot sign in under an organisation's name: %v", err)
	}

	// ---- membership rules
	step()
	if s := putJSON(J, "PUT", "/v1/orgs/acme/members/bob", map[string]string{"role": "member"}); s != 204 {
		t.Fatalf("%d", s)
	}
	if s := putJSON(J, "PUT", "/v1/orgs/acme/members/nobody", map[string]string{"role": "member"}); s != 404 {
		t.Fatalf("an unknown user: %d", s)
	}
	if s := putJSON(J, "PUT", "/v1/orgs/acme/members/eve", map[string]string{"role": "boss"}); s != 400 {
		t.Fatalf("%d", s)
	}
	if s := putJSON(E, "PUT", "/v1/orgs/acme/members/eve", map[string]string{"role": "owner"}); s != 404 {
		t.Fatalf("an outsider cannot even tell the organisation is there to change: %d", s)
	}
	if s := putJSON(B, "PUT", "/v1/orgs/acme/members/eve", map[string]string{"role": "member"}); s != 403 {
		t.Fatalf("a plain member cannot add people: %d", s)
	}
	if s, _ := B.get("/v1/orgs/acme/members"); s != 200 {
		t.Fatalf("members list the members: %d", s)
	}
	if s, _ := E.get("/v1/orgs/acme/members"); s != 404 {
		t.Fatalf("outsiders cannot list them: %d", s)
	}
	if _, b := B.get("/v1/me/orgs"); !strings.Contains(string(b), `"acme"`) || !strings.Contains(string(b), `"member"`) {
		t.Fatalf("%s", b)
	}

	// ---- publishing under the organisation's name: members only, and the rig belongs to the organisation
	upload := func(c *client, ver string) int {
		s, _ := c.upload("acme", "tool", rigTar(t, goodRig("acme", "tool", ver)))
		return s
	}
	if s := upload(B, "1.0.0"); s != 202 {
		t.Fatalf("a member publishes: %d", s)
	}
	if s := upload(E, "1.0.1"); s != 403 {
		t.Fatalf("an outsider cannot: %d", s)
	}
	if s := upload(J, "1.0.1"); s != 202 { // another member adds to a rig bob created
		t.Fatalf("%d", s)
	}
	// pending versions are visible to the members, not to outsiders
	if st := J.versionStatus("acme", "tool", "1.0.1"); st != "pending" {
		t.Fatalf("the owner sees the pending version: %q", st)
	}
	if st := E.versionStatus("acme", "tool", "1.0.1"); st == "pending" {
		t.Fatal("an outsider sees a pending version")
	}
	e.scanAll()
	// private: invisible to outsiders, identical to a rig that does not exist
	sMissing, bMissing := E.get("/v1/rigs/acme/nothing")
	sPriv, bPriv := E.get("/v1/rigs/acme/tool")
	if sMissing != 404 || sPriv != 404 || strings.Replace(string(bPriv), "tool", "nothing", 1) != string(bMissing) {
		t.Fatalf("%d %q / %d %q", sPriv, bPriv, sMissing, bMissing)
	}
	for _, p := range []string{"/v1/rigs/acme/tool/versions/1.0.0", "/v1/rigs/acme/tool/versions/1.0.0/tarball", "/v1/rigs/acme/tool/diff", "/r/acme/tool", "/r/acme/tool/diff"} {
		if s, _ := E.get(p); s != 404 {
			t.Errorf("outsider GET %s: %d", p, s)
		}
		if s, _ := anon.get(p); s != 404 {
			t.Errorf("anonymous GET %s: %d", p, s)
		}
	}
	for name, c := range map[string]*client{"owner": J, "member": B} {
		step()
		if s, _ := c.get("/v1/rigs/acme/tool"); s != 200 {
			t.Errorf("%s cannot see the organisation's private rig: %d", name, s)
		}
		if s, _ := c.get("/v1/rigs/acme/tool/diff"); s != 200 {
			t.Errorf("%s cannot diff it: %d", name, s)
		}
	}
	if _, b := anon.get("/v1/search?q=tool"); strings.Contains(string(b), "acme") {
		t.Fatalf("a private rig is never in search: %s", b)
	}

	// a collection never shows it to outsiders
	step()
	putJSON(J, "POST", "/v1/collections", map[string]string{"slug": "mine", "title": "Mine"})
	if s := putJSON(J, "PUT", "/v1/collections/jia/mine/items", map[string]string{"rig": "acme/tool"}); s != 204 {
		t.Fatalf("a member can collect the organisation's rig: %d", s)
	}
	if _, b := E.get("/v1/collections/jia/mine"); strings.Contains(string(b), "acme") {
		t.Fatalf("collections must not reveal private organisation rigs: %s", b)
	}
	if _, b := J.get("/v1/collections/jia/mine"); !strings.Contains(string(b), `"name":"tool"`) {
		t.Fatalf("%s", b)
	}
	if s := putJSON(E, "PUT", "/v1/collections/eve/none/items", map[string]string{"rig": "acme/tool"}); s != 404 {
		t.Fatalf("%d", s)
	}

	// ---- visibility and yanking need different roles
	step()
	if s, _, _ := B.do("POST", "/v1/rigs/acme/tool/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 403 {
		t.Fatalf("a plain member cannot publish the rig to the world: %d", s)
	}
	if s, _, _ := B.do("POST", "/v1/rigs/acme/tool/versions/1.0.0/yank", []byte("reason=oops"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatalf("a member may yank: %d", s)
	}
	if s, _, _ := E.do("POST", "/v1/rigs/acme/tool/versions/1.0.1/yank", []byte("reason=x"), "application/x-www-form-urlencoded"); s != 404 {
		t.Fatalf("an outsider cannot see it, let alone yank: %d", s)
	}
	if s, _, b := J.do("POST", "/v1/rigs/acme/tool/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatalf("an owner may: %d %s", s, b)
	}
	if s, _ := anon.get("/v1/rigs/acme/tool"); s != 200 {
		t.Fatal("now public")
	}
	if _, b := anon.get("/r/acme/tool"); !strings.Contains(string(b), "acme") || strings.Contains(string(b), "bob") {
		t.Fatalf("the page names the organisation as publisher, not the member who uploaded: %s", b)
	}
	if _, b := anon.get("/u/acme"); !strings.Contains(string(b), "organisation") || !strings.Contains(string(b), "acme/tool") || strings.Contains(string(b), "Members") {
		t.Fatalf("the organisation page lists its public rigs and hides its members from outsiders: %s", b)
	}
	e.gh.user["id"] = 1 // the web sign-in is jia
	web := e.signIn("/")
	resp, err := web.Get(e.url("/u/acme"))
	if err != nil {
		t.Fatal(err)
	}
	if b := body(t, resp); !strings.Contains(b, "Members") || !strings.Contains(b, "bob") {
		t.Fatalf("members see the member list: %s", b)
	}
	step()
	if s, _, _ := J.do("POST", "/v1/rigs/acme/tool/visibility", []byte("visibility=private"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatal(s)
	}

	// ---- roles: admins manage plain members only; the last owner stays
	step()
	putJSON(J, "PUT", "/v1/orgs/acme/members/carol", map[string]string{"role": "admin"})
	if s := putJSON(C, "PUT", "/v1/orgs/acme/members/eve", map[string]string{"role": "member"}); s != 204 {
		t.Fatalf("an admin adds a plain member: %d", s)
	}
	if s := putJSON(C, "PUT", "/v1/orgs/acme/members/bob", map[string]string{"role": "admin"}); s != 403 {
		t.Fatalf("an admin cannot appoint admins: %d", s)
	}
	if s, _, _ := C.do("DELETE", "/v1/orgs/acme/members/jia", nil, ""); s != 403 {
		t.Fatalf("an admin cannot remove an owner: %d", s)
	}
	if s, _, _ := J.do("DELETE", "/v1/orgs/acme/members/jia", nil, ""); s != 409 {
		t.Fatalf("the last owner cannot leave: %d", s)
	}
	if s := putJSON(J, "PUT", "/v1/orgs/acme/members/jia", map[string]string{"role": "admin"}); s != 409 {
		t.Fatalf("the last owner cannot be demoted: %d", s)
	}

	// ---- leaving takes access away at once, even from the person who created the rig
	if s, _, _ := B.do("DELETE", "/v1/orgs/acme/members/bob", nil, ""); s != 204 {
		t.Fatalf("anyone may leave: %d", s)
	}
	if s, _ := B.get("/v1/rigs/acme/tool"); s != 404 {
		t.Fatalf("a former member sees a private rig: %d", s)
	}
	if s := upload(B, "1.0.2"); s != 403 {
		t.Fatalf("a former member publishes: %d", s)
	}
	if s, _ := B.get("/v1/orgs/acme/members"); s != 404 {
		t.Fatal(s)
	}

	// ---- personal rigs are unaffected
	step()
	if s, _ := J.upload("jia", "solo", rigTar(t, goodRig("jia", "solo", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if s, _ := E.get("/v1/rigs/jia/solo"); s != 404 {
		t.Fatalf("a personal private rig stays private: %d", s)
	}
	if s, _ := J.get("/v1/rigs/jia/solo"); s != 200 {
		t.Fatal(s)
	}
}

// A person and an organisation racing for one name: exactly one wins, whichever the database sees first.
func TestNamespaceRaceHasOneWinner(t *testing.T) {
	e := newEnv(t, nil)
	creator, _ := e.userToken("creator", 1)
	for round := 0; round < 8; round++ {
		name := fmt.Sprintf("race%d", round)
		results := make(chan error, 2)
		go func() {
			_, err := e.store.UpsertUser(t.Context(), registry.GitHubUser{ID: int64(100 + round), Login: name}, false)
			results <- err
		}()
		go func() {
			_, err := e.store.CreateOrg(t.Context(), creator, name, "")
			results <- err
		}()
		a, b := <-results, <-results
		if (a == nil) == (b == nil) {
			t.Fatalf("round %d: exactly one claim must succeed, got %v and %v", round, a, b)
		}
	}
}

func TestDisabledOrganisationVanishes(t *testing.T) {
	e := newEnv(t, nil)
	_, jia := e.userToken("jia", 1)
	J, anon := e.as(jia), e.as("")
	putJSON(J, "POST", "/v1/orgs", map[string]string{"login": "acme"})
	if s, _ := J.upload("acme", "tool", rigTar(t, goodRig("acme", "tool", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if s, _, _ := J.do("POST", "/v1/rigs/acme/tool/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatal(s)
	}
	if s, _ := anon.get("/v1/rigs/acme/tool"); s != 200 {
		t.Fatal(s)
	}
	if err := e.store.SetOrgDisabled(t.Context(), "acme", true); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*client{"anonymous": anon, "the owner": J} {
		if s, _ := c.get("/v1/rigs/acme/tool"); s != 404 {
			t.Errorf("%s still sees a disabled organisation's rig: %d", name, s)
		}
	}
	if s, _ := anon.get("/v1/orgs/acme"); s != 404 {
		t.Fatalf("%d", s)
	}
	if s, _ := J.upload("acme", "tool", rigTar(t, goodRig("acme", "tool", "1.0.1"))); s != 403 {
		t.Fatalf("a disabled organisation accepts nothing: %d", s)
	}
	if err := e.store.SetOrgDisabled(t.Context(), "acme", false); err != nil {
		t.Fatal(err)
	}
	if s, _ := anon.get("/v1/rigs/acme/tool"); s != 200 {
		t.Fatalf("re-enabled: %d", s)
	}
}
