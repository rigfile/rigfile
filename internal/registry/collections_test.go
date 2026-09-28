package registry_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func jsonBody(v any) []byte { b, _ := json.Marshal(v); return b }

func TestCollections(t *testing.T) {
	e := newEnv(t, nil)
	_, ada := e.userToken("ada", 1)
	_, bob := e.userToken("bob", 2)
	owner, other, anon := e.as(ada), e.as(bob), e.as("")

	publishWithManifest(t, e, owner, "ada", "pub", "1.0.0", "")
	publishWithManifest(t, e, other, "bob", "theirs", "1.0.0", "")
	if s, _ := owner.upload("ada", "priv", rigTar(t, goodRig("ada", "priv", "1.0.0"))); s != 202 { // stays private
		t.Fatal(s)
	}
	e.scanAll()

	// creating: token needed, input validated, slug unique per owner
	if s, _, _ := anon.do("POST", "/v1/collections", jsonBody(map[string]string{"slug": "x", "title": "X"}), "application/json"); s != 401 {
		t.Fatalf("%d", s)
	}
	for name, in := range map[string]map[string]string{
		"bad slug": {"slug": "Not A Slug", "title": "X"}, "no title": {"slug": "x"}, "bad visibility": {"slug": "x", "title": "X", "visibility": "secret"},
	} {
		if s, _, b := owner.do("POST", "/v1/collections", jsonBody(in), "application/json"); s != 400 {
			t.Errorf("%s: %d %s", name, s, b)
		}
	}
	if s, _, b := owner.do("POST", "/v1/collections", jsonBody(map[string]string{"slug": "favs", "title": "My favourites", "description": "things I use"}), "application/json"); s != 201 || !strings.Contains(string(b), "/c/ada/favs") {
		t.Fatalf("%d %s", s, b)
	}
	if s, _, _ := owner.do("POST", "/v1/collections", jsonBody(map[string]string{"slug": "favs", "title": "Again"}), "application/json"); s != 409 {
		t.Fatalf("a duplicate slug: %d", s)
	}

	// adding: only rigs the OWNER can see; a stranger's private rig looks like a missing one
	add := func(c *client, owner, slug, rig, note string) int {
		s, _, _ := c.do("PUT", "/v1/collections/"+owner+"/"+slug+"/items", jsonBody(map[string]string{"rig": rig, "note": note}), "application/json")
		return s
	}
	if s := add(owner, "ada", "favs", "ada/pub", "start here"); s != 204 {
		t.Fatalf("%d", s)
	}
	if s := add(owner, "ada", "favs", "bob/theirs", ""); s != 204 {
		t.Fatalf("a public rig of someone else: %d", s)
	}
	if s := add(owner, "ada", "favs", "ada/priv", "my private one"); s != 204 {
		t.Fatalf("the owner's own private rig: %d", s)
	}
	if s := add(owner, "ada", "favs", "bob/nothing", ""); s != 404 {
		t.Fatalf("a missing rig: %d", s)
	}
	if s := add(owner, "ada", "favs", "not-a-ref", ""); s != 400 {
		t.Fatalf("%d", s)
	}
	if s := add(owner, "ada", "favs", "ada/pub", "updated note"); s != 204 {
		t.Fatalf("adding again updates the note: %d", s)
	}
	// nobody else can change it
	if s := add(other, "ada", "favs", "bob/theirs", ""); s != 403 {
		t.Fatalf("%d", s)
	}
	if s, _, _ := other.do("DELETE", "/v1/collections/ada/favs", nil, ""); s != 403 {
		t.Fatalf("%d", s)
	}

	// reading: a stranger sees the public rigs only, and cannot tell that a private one is in it
	s, b := anon.get("/v1/collections/ada/favs")
	if s != 200 || !strings.Contains(string(b), "ada/pub") && !strings.Contains(string(b), `"name":"pub"`) || strings.Contains(string(b), "priv") || !strings.Contains(string(b), "updated note") || !strings.Contains(string(b), `"name":"theirs"`) {
		t.Fatalf("%d %s", s, b)
	}
	if _, b := owner.get("/v1/collections/ada/favs"); !strings.Contains(string(b), `"name":"priv"`) || !strings.Contains(string(b), "my private one") {
		t.Fatalf("the owner sees their private rig: %s", b)
	}
	page := func(c *client) string { _, b := c.get("/c/ada/favs"); return string(b) }
	if p := page(anon); !strings.Contains(p, "My favourites") || !strings.Contains(p, "ada/pub") || strings.Contains(p, "ada/priv") {
		t.Fatalf("%s", p)
	}
	if _, b := anon.get("/u/ada"); !strings.Contains(string(b), "My favourites") {
		t.Fatalf("the profile lists public collections: %s", b)
	}

	// a private collection is invisible to others, identical to a missing one
	if s, _, _ := owner.do("POST", "/v1/collections", jsonBody(map[string]string{"slug": "draft", "title": "Draft", "visibility": "private"}), "application/json"); s != 201 {
		t.Fatal(s)
	}
	sMissing, bMissing := other.get("/v1/collections/ada/none")
	sPriv, bPriv := other.get("/v1/collections/ada/draft")
	if sMissing != 404 || sPriv != 404 || string(bMissing) != string(bPriv) {
		t.Fatalf("%d %q vs %d %q", sPriv, bPriv, sMissing, bMissing)
	}
	if s, _ := other.get("/c/ada/draft"); s != 404 {
		t.Fatal(s)
	}
	if _, b := other.get("/v1/users/ada/collections"); strings.Contains(string(b), "draft") {
		t.Fatalf("%s", b)
	}
	if _, b := owner.get("/v1/users/ada/collections"); !strings.Contains(string(b), "draft") {
		t.Fatalf("%s", b)
	}

	// a rig that later goes private disappears from other people's view of the collection
	if s, _, _ := other.do("POST", "/v1/rigs/bob/theirs/visibility", []byte("visibility=private"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatal(s)
	}
	if _, b := anon.get("/v1/collections/ada/favs"); strings.Contains(string(b), "theirs") {
		t.Fatalf("a rig made private must vanish from collections: %s", b)
	}

	// removing and deleting
	if s, _, _ := owner.do("DELETE", "/v1/collections/ada/favs/items/ada/pub", nil, ""); s != 204 {
		t.Fatal(s)
	}
	if _, b := anon.get("/v1/collections/ada/favs"); strings.Contains(string(b), `"name":"pub"`) {
		t.Fatalf("%s", b)
	}
	if s, _, _ := owner.do("DELETE", "/v1/collections/ada/favs", nil, ""); s != 204 {
		t.Fatal(s)
	}
	if s, _ := anon.get("/v1/collections/ada/favs"); s != 404 {
		t.Fatal("a deleted collection is gone")
	}
	if s, _, _ := owner.do("POST", "/v1/collections", jsonBody(map[string]string{"slug": "favs", "title": "Again"}), "application/json"); s != 201 {
		t.Fatalf("a deleted collection's slug is free again: %d", s)
	}
}

func TestCollectionLimits(t *testing.T) {
	e := newEnv(t, nil)
	_, ada := e.userToken("ada", 1)
	owner := e.as(ada)
	for i := 0; i < 50; i++ {
		e.clk.add(time.Minute) // the API is rate limited
		if s, _, b := owner.do("POST", "/v1/collections", jsonBody(map[string]string{"slug": fmt.Sprintf("c%d", i), "title": "C"}), "application/json"); s != 201 {
			t.Fatalf("%d: %d %s", i, s, b)
		}
	}
	e.clk.add(time.Minute)
	if s, _, _ := owner.do("POST", "/v1/collections", jsonBody(map[string]string{"slug": "one-too-many", "title": "C"}), "application/json"); s != 409 {
		t.Fatalf("a user is limited to 50 collections: %d", s)
	}
}
