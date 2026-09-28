package registry_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const mcpV2 = "mcp_servers:\n  search:\n    command: npx\n    args: [\"-y\", \"search-mcp@1.0.0\"]\n    network:\n      allow: [\"api.search.test\"]\n"

func TestDiffBetweenVersions(t *testing.T) {
	e := newEnv(t, nil)
	_, ada := e.userToken("ada", 1)
	_, bob := e.userToken("bob", 2)
	owner, stranger, anon := e.as(ada), e.as(bob), e.as("")

	if s, _ := owner.upload("ada", "demo", rigTar(t, goodRig("ada", "demo", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	v2 := goodRig("ada", "demo", "1.1.0")
	v2["rigfile.yaml"] = manifestYAML("ada", "demo", "1.1.0", mcpV2)
	v2["instructions/style.md"] = "# Style\n- be terse\n- mention <script>alert(1)</script> never\n"
	if s, _ := owner.upload("ada", "demo", rigTar(t, v2)); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if owner.versionStatus("ada", "demo", "1.1.0") != "published" {
		t.Fatalf("v2 must be published: %s", owner.versionStatus("ada", "demo", "1.1.0"))
	}

	// private rig: invisible to others, byte-identical to a rig that does not exist
	sMissing, bMissing := stranger.get("/v1/rigs/ada/nothing/diff")
	sPriv, bPriv := stranger.get("/v1/rigs/ada/demo/diff")
	if sPriv != 404 || sMissing != 404 || strings.Replace(string(bPriv), "nothing", "demo", 1) != strings.Replace(string(bMissing), "nothing", "demo", 1) && string(bPriv) != string(bMissing) {
		t.Fatalf("private rig: %d %q vs %d %q", sPriv, bPriv, sMissing, bMissing)
	}
	if s, _ := stranger.get("/r/ada/demo/diff"); s != 404 {
		t.Fatalf("private rig page: %d", s)
	}
	if s, _, _ := owner.do("POST", "/v1/rigs/ada/demo/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatal(s)
	}

	// API: defaults compare the newest published version with the one before it
	s, b := anon.get("/v1/rigs/ada/demo/diff")
	if s != 200 {
		t.Fatalf("%d %s", s, b)
	}
	var res struct {
		From, To string
		Entries  []struct {
			Category, Key, Change string
			Notes                 []struct{ Level, Text string }
		}
		Files []struct{ Path, Change, Diff string }
	}
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	if res.From != "1.0.0" || res.To != "1.1.0" {
		t.Fatalf("%+v", res)
	}
	review := ""
	for _, en := range res.Entries {
		for _, n := range en.Notes {
			if n.Level == "review" {
				review += en.Category + " " + en.Key + ": " + n.Text + "\n"
			}
		}
	}
	if !strings.Contains(review, "mcp_servers search: adds an MCP server that runs: npx -y search-mcp@1.0.0") {
		t.Fatalf("the new MCP server must be flagged:\n%s", review)
	}
	var sawStyle bool
	for _, f := range res.Files {
		if f.Path == "instructions/style.md" && f.Change == "changed" && strings.Contains(f.Diff, "+- mention <script>") {
			sawStyle = true
		}
	}
	if !sawStyle {
		t.Fatalf("the changed instruction file must be diffed: %+v", res.Files)
	}
	// explicit ends, and the reverse direction
	if s, b := anon.get("/v1/rigs/ada/demo/diff?from=1.1.0&to=1.0.0"); s != 200 || !strings.Contains(string(b), `"from":"1.1.0"`) {
		t.Fatalf("%d %s", s, b)
	}

	// the page escapes everything it shows
	s, b = anon.get("/r/ada/demo/diff")
	page := string(b)
	if s != 200 || !strings.Contains(page, "Look at these before you accept") || !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") || strings.Contains(page, "<script>alert(1)") {
		t.Fatalf("%d\n%s", s, page)
	}

	// nothing earlier than the first version; unknown versions are 404
	if s, b := anon.get("/v1/rigs/ada/demo/diff?to=1.0.0"); s != 404 || !strings.Contains(string(b), "no earlier version") {
		t.Fatalf("%d %s", s, b)
	}
	if s, _ := anon.get("/v1/rigs/ada/demo/diff?from=9.9.9"); s != 404 {
		t.Fatalf("%d", s)
	}

	// diffs are rate limited per client
	limited := false
	for i := 0; i < 15; i++ {
		if s, _ := anon.get("/v1/rigs/ada/demo/diff"); s == 429 {
			limited = true
		}
	}
	if !limited {
		t.Fatal("diffs must be rate limited")
	}
	e.clk.add(2 * time.Minute)

	// a pending version is visible only to its owner, in every way to ask for it
	if s, _ := owner.upload("ada", "demo", rigTar(t, goodRig("ada", "demo", "1.2.0"))); s != 202 {
		t.Fatal(s)
	}
	for name, c := range map[string]*client{"stranger": stranger, "anonymous": anon} {
		if s, _ := c.get("/v1/rigs/ada/demo/diff?to=1.2.0"); s != 404 {
			t.Errorf("%s reads a diff of a pending version: %d", name, s)
		}
		if s, _ := c.get("/v1/rigs/ada/demo/diff?from=1.2.0&to=1.1.0"); s != 404 {
			t.Errorf("%s reads a diff FROM a pending version: %d", name, s)
		}
		if s, _ := c.get("/r/ada/demo/diff?to=1.2.0"); s != 404 {
			t.Errorf("%s reads a page of a pending version: %d", name, s)
		}
	}
	if s, _ := owner.get("/v1/rigs/ada/demo/diff?to=1.2.0&from=1.1.0"); s != 200 {
		t.Fatalf("the owner may compare a pending version: %d", s)
	}
}

func publishWithManifest(t *testing.T, e *env, c *client, owner, name, version, extra string) {
	t.Helper()
	files := goodRig(owner, name, version)
	files["rigfile.yaml"] = manifestYAML(owner, name, version, extra)
	if s, b := c.upload(owner, name, rigTar(t, files)); s != 202 {
		t.Fatalf("%d %v", s, b)
	}
	e.scanAll()
	if c.versionStatus(owner, name, version) != "published" {
		t.Fatalf("%s/%s@%s: %s", owner, name, version, c.versionStatus(owner, name, version))
	}
	if s, _, b := c.do("POST", "/v1/rigs/"+owner+"/"+name+"/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatalf("%d %s", s, b)
	}
}

func TestDerivedRigsAndUseAsBase(t *testing.T) {
	e := newEnv(t, nil)
	_, ada := e.userToken("ada", 1)
	_, bob := e.userToken("bob", 2)
	owner, other, anon := e.as(ada), e.as(bob), e.as("")

	publishWithManifest(t, e, owner, "ada", "base", "1.4.2", "")
	publishWithManifest(t, e, other, "bob", "child", "0.1.0", "from:\n  - ada/base@^1.4\n")
	// a private rig that builds on it is never listed
	if s, _ := other.upload("bob", "secretrig", rigTar(t, map[string]string{"rigfile.yaml": manifestYAML("bob", "secretrig", "0.1.0", "from:\n  - ada/base@^1.4\n"), "instructions/style.md": "x\n"})); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	// a public rig that does not build on it, and one that mentions it only in its description
	publishWithManifest(t, e, other, "bob", "unrelated", "0.1.0", "")

	s, b := anon.get("/v1/rigs/ada/base/derived")
	if s != 200 || !strings.Contains(string(b), `"total":1`) || !strings.Contains(string(b), `"name":"child"`) || strings.Contains(string(b), "secretrig") || strings.Contains(string(b), "unrelated") {
		t.Fatalf("%d %s", s, b)
	}
	if _, b := anon.get("/v1/rigs/ada/base"); !strings.Contains(string(b), `"derived":1`) {
		t.Fatalf("%s", b)
	}
	page := func() string { _, b := anon.get("/r/ada/base"); return string(b) }
	if p := page(); !strings.Contains(p, "Built on this rig (1)") || !strings.Contains(p, "bob/child") || !strings.Contains(p, "from:\n  - ada/base@^1.4") || !strings.Contains(p, "rigfile fork ada/base") {
		t.Fatalf("the rig page must show who builds on it and a use-as-base snippet:\n%s", p)
	}

	// a newer version of the child that no longer builds on it stops counting
	files := goodRig("bob", "child", "0.2.0")
	if s, _ := other.upload("bob", "child", rigTar(t, files)); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if st := other.versionStatus("bob", "child", "0.2.0"); st != "published" {
		t.Fatalf("0.2.0 is %s", st)
	}
	if _, b := anon.get("/v1/rigs/ada/base/derived"); !strings.Contains(string(b), `"total":0`) {
		t.Fatalf("%s", b)
	}
	if strings.Contains(page(), "Built on this rig") {
		t.Fatal("the section disappears with the last derived rig")
	}

	// a private base: nobody else can even ask
	if s, _, _ := owner.do("POST", "/v1/rigs/ada/base/visibility", []byte("visibility=private"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatal(s)
	}
	if s, _ := anon.get("/v1/rigs/ada/base/derived"); s != 404 {
		t.Fatalf("%d", s)
	}
}
