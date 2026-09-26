package registry_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWebFormsForCollections(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1001)
	publishPublic(t, e, e.as(tok), "jia", "shared", "1.0.0", goodRig("jia", "shared", "1.0.0"))
	cl := e.signIn("/")
	_, page := getPage(t, e, cl, "/u/jia")
	if !strings.Contains(page, "New collection") || !strings.Contains(page, "New organisation") {
		t.Fatalf("the owner's profile offers the forms:\n%s", page)
	}
	csrf := csrfFrom(t, page)
	if _, page := getPage(t, e, nil, "/u/jia"); strings.Contains(page, "New collection") {
		t.Fatal("a stranger must not be offered the owner's forms")
	}
	post := func(path string, vals url.Values) (int, string) {
		vals.Set("csrf", csrf)
		e.clk.add(time.Minute)
		return postForm(t, e, cl, path, vals, e.srv.URL)
	}
	// every form needs the session, the token and the origin
	if code, _ := postForm(t, e, cl, "/manage/collections", url.Values{"title": {"x"}, "slug": {"x"}}, e.srv.URL); code != 403 {
		t.Fatalf("no csrf: %d", code)
	}
	if code, _ := postForm(t, e, cl, "/manage/collections", url.Values{"title": {"x"}, "slug": {"x"}, "csrf": {csrf}}, "https://evil.example"); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	if code, _ := postForm(t, e, e.client(), "/manage/collections", url.Values{"title": {"x"}, "slug": {"x"}, "csrf": {csrf}}, e.srv.URL); code != 401 {
		t.Fatalf("no session: %d", code)
	}
	// create, add, see, remove, delete
	if code, msg := post("/manage/collections", url.Values{"title": {"Starters"}, "slug": {"BAD SLUG"}}); code != 400 || !strings.Contains(msg, "slug") {
		t.Fatalf("%d %s", code, msg)
	}
	if code, _ := post("/manage/collections", url.Values{"title": {"Starters"}, "slug": {"starters"}, "description": {"begin here"}}); code != 303 {
		t.Fatalf("%d", code)
	}
	if code, msg := post("/manage/collections", url.Values{"title": {"Again"}, "slug": {"starters"}}); code != 409 || !strings.Contains(msg, "already have") {
		t.Fatalf("%d %s", code, msg)
	}
	if code, msg := post("/manage/collections/starters/add", url.Values{"rig": {"jia/nothing"}}); code != 404 {
		t.Fatalf("%d %s", code, msg)
	}
	if code, _ := post("/manage/collections/starters/add", url.Values{"rig": {"jia/shared"}, "note": {"a small one"}}); code != 303 {
		t.Fatalf("%d", code)
	}
	_, page = getPage(t, e, cl, "/c/jia/starters")
	if !strings.Contains(page, "jia/shared") || !strings.Contains(page, "a small one") || !strings.Contains(page, "Add a rig") || !strings.Contains(page, "remove") {
		t.Fatalf("%s", page)
	}
	if _, page := getPage(t, e, nil, "/c/jia/starters"); strings.Contains(page, "Add a rig") || strings.Contains(page, "Delete this collection") {
		t.Fatal("a visitor is not offered the owner's controls")
	}
	if code, _ := post("/manage/collections/starters/remove", url.Values{"rig": {"jia/shared"}}); code != 303 {
		t.Fatalf("%d", code)
	}
	if _, page := getPage(t, e, cl, "/c/jia/starters"); strings.Contains(page, "a small one") {
		t.Fatal("removed")
	}
	if code, msg := post("/manage/collections/starters/delete", url.Values{}); code != 400 || !strings.Contains(msg, "confirm") {
		t.Fatalf("%d %s", code, msg)
	}
	if code, _ := post("/manage/collections/starters/delete", url.Values{"confirm": {"yes"}}); code != 303 {
		t.Fatalf("%d", code)
	}
	if code, _ := getPage(t, e, cl, "/c/jia/starters"); code != 404 {
		t.Fatalf("%d", code)
	}
}

func TestWebFormsForOrganisations(t *testing.T) {
	e := newEnv(t, nil)
	e.userToken("jia", 1001)
	e.userToken("bob", 2)
	owner := e.signIn("/") // jia
	_, page := getPage(t, e, owner, "/u/jia")
	csrf := csrfFrom(t, page)
	post := func(path string, vals url.Values, token string) (int, string) {
		vals.Set("csrf", token)
		e.clk.add(time.Minute)
		return postForm(t, e, owner, path, vals, e.srv.URL)
	}
	if code, _ := post("/manage/orgs", url.Values{"login": {"bob"}}, csrf); code != 409 {
		t.Fatalf("an existing person's name: %d", code)
	}
	if code, _ := post("/manage/orgs", url.Values{"login": {"acme"}, "name": {"Acme Inc"}}, csrf); code != 303 {
		t.Fatalf("%d", code)
	}
	if code, _ := post("/manage/orgs/acme/member", url.Values{"login": {"nobody"}, "role": {"member"}}, csrf); code != 404 {
		t.Fatalf("%d", code)
	}
	if code, _ := post("/manage/orgs/acme/member", url.Values{"login": {"bob"}, "role": {"member"}}, csrf); code != 303 {
		t.Fatalf("%d", code)
	}
	_, page = getPage(t, e, owner, "/u/acme")
	if !strings.Contains(page, "Members") || !strings.Contains(page, "bob") || !strings.Contains(page, "Add or change") || !strings.Contains(page, "owner") {
		t.Fatalf("%s", page)
	}
	if _, page := getPage(t, e, nil, "/u/acme"); strings.Contains(page, "Members") || strings.Contains(page, "Add or change") {
		t.Fatalf("outsiders see neither the member list nor the controls:\n%s", page)
	}

	// bob, a plain member: sees the list, gets no controls, cannot add, may leave
	e.gh.user = map[string]any{"id": 2, "login": "bob"}
	member := e.signIn("/")
	_, page = getPage(t, e, member, "/u/acme")
	if !strings.Contains(page, "Members") || strings.Contains(page, "Add or change") || !strings.Contains(page, "leave") {
		t.Fatalf("%s", page)
	}
	mcsrf := csrfFrom(t, page)
	e.clk.add(time.Minute)
	if code, _ := postForm(t, e, member, "/manage/orgs/acme/member", url.Values{"login": {"bob"}, "role": {"owner"}, "csrf": {mcsrf}}, e.srv.URL); code != 403 {
		t.Fatalf("a plain member cannot appoint: %d", code)
	}
	e.clk.add(time.Minute)
	if code, _ := postForm(t, e, member, "/manage/orgs/acme/remove", url.Values{"login": {"jia"}, "csrf": {mcsrf}}, e.srv.URL); code != 403 {
		t.Fatalf("a plain member cannot remove the owner: %d", code)
	}
	e.clk.add(time.Minute)
	if code, _ := postForm(t, e, member, "/manage/orgs/acme/remove", url.Values{"login": {"bob"}, "csrf": {mcsrf}}, e.srv.URL); code != 303 {
		t.Fatalf("leaving: %d", code)
	}
	if _, page := getPage(t, e, member, "/u/acme"); strings.Contains(page, "Members") {
		t.Fatal("a former member no longer sees the list")
	}
}
