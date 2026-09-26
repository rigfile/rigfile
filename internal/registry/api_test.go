package registry_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/registry"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/source"
)

func fakeSecret() string { return "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo" }

func manifestYAML(owner, name, version, extra string) string {
	return "apiVersion: rigfile.dev/v1\nname: " + owner + "/" + name + "\nversion: " + version + "\ndescription: A test rig\ninstructions:\n  - {id: style, file: instructions/style.md}\n" + extra
}

// rigTar builds a gzip tarball of the given files (name -> content).
func rigTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

func goodRig(owner, name, version string) map[string]string {
	return map[string]string{
		"rigfile.yaml":          manifestYAML(owner, name, version, ""),
		"instructions/style.md": "# Style\n- be terse\n",
		"README.md":             "# " + name + "\nA rig.\n",
	}
}

type client struct {
	e     *env
	token string
}

func (e *env) userToken(login string, id int64) (*registry.User, string) {
	e.t.Helper()
	u, err := e.store.UpsertUser(e.t.Context(), registry.GitHubUser{ID: id, Login: login}, false)
	if err != nil {
		e.t.Fatal(err)
	}
	tok, err := e.store.CreateToken(e.t.Context(), u.ID, "test", 30*24*time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	return u, tok
}

func (e *env) as(token string) *client { return &client{e: e, token: token} }

func (c *client) do(method, path string, body []byte, ctype string) (int, http.Header, []byte) {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, c.e.url(path), rd)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func (c *client) get(path string) (int, []byte) {
	s, _, b := c.do("GET", path, nil, "")
	return s, b
}

func (c *client) upload(owner, name string, tarball []byte) (int, map[string]any) {
	c.e.t.Helper()
	s, _, b := c.do("POST", "/v1/rigs/"+owner+"/"+name+"/versions", tarball, "application/gzip")
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return s, out
}

func (e *env) scanAll() {
	e.t.Helper()
	sc := &registry.Scanner{Store: e.store, Blobs: e.blobs, Limits: source.DefaultLimits,
		Scan: func() (*scan.Scanner, error) { return scan.New(scan.Options{}) }}
	for i := 0; i < 50; i++ {
		did, err := sc.RunOnce(e.t.Context(), "test-worker")
		if err != nil {
			e.t.Fatal(err)
		}
		if !did {
			return
		}
	}
	e.t.Fatal("the scan queue did not drain")
}

func (c *client) versionStatus(owner, name, ver string) string {
	s, b := c.get("/v1/rigs/" + owner + "/" + name + "/versions/" + ver)
	if s != 200 {
		return fmt.Sprintf("http %d", s)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m["status"].(string)
}

func TestUploadRefusesHostileArchives(t *testing.T) {
	e := newEnv(t, func(c *registry.Config) { c.MaxUpload = 64 << 10 })
	_, tok := e.userToken("jia", 1)
	c := e.as(tok)
	tarWith := func(h tar.Header, body string) []byte {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		_ = tw.WriteHeader(&h)
		_, _ = tw.Write([]byte(body))
		_ = tw.Close()
		_ = gw.Close()
		return buf.Bytes()
	}
	cases := map[string][]byte{
		"path traversal":              tarWith(tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}, "x"),
		"symlink":                     tarWith(tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o644}, ""),
		"absolute path":               tarWith(tar.Header{Name: "/etc/x", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}, "x"),
		"not gzip":                    []byte("plain text, not a tarball"),
		"no manifest":                 rigTar(t, map[string]string{"README.md": "x"}),
		"invalid manifest":            rigTar(t, map[string]string{"rigfile.yaml": "apiVersion: nope\n"}),
		"missing file":                rigTar(t, map[string]string{"rigfile.yaml": manifestYAML("jia", "demo", "1.0.0", "")}),
		"bad version":                 rigTar(t, map[string]string{"rigfile.yaml": manifestYAML("jia", "demo", "1.0", ""), "instructions/style.md": "x"}),
		"an inherited rigfile/ layer": rigTar(t, map[string]string{"rigfile.yaml": manifestYAML("jia", "demo", "1.0.0", "from: [rigfile/other]\n"), "instructions/style.md": "x"}),
	}
	for name, data := range cases {
		if s, out := c.upload("jia", "demo", data); s != 422 && s != 400 {
			t.Errorf("%s: status %d %v, want a refusal", name, s, out)
		}
	}
	// too large
	big := rigTar(t, map[string]string{"rigfile.yaml": "x", "big": strings.Repeat(string(bytes.Repeat([]byte{0}, 1)), 1)})
	_ = big
	huge := make([]byte, 128<<10)
	if s, _, _ := c.do("POST", "/v1/rigs/jia/demo/versions", huge, "application/gzip"); s != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload: %d", s)
	}
	// nothing was stored, nothing was created
	var n int
	_ = e.store.DB.QueryRow(`SELECT count(*) FROM versions`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d versions were created by refused uploads", n)
	}
}

func TestUploadNamespaceRules(t *testing.T) {
	e := newEnv(t, nil)
	_, jia := e.userToken("jia", 1)
	_, other := e.userToken("bob", 2)
	c := e.as(jia)
	good := rigTar(t, goodRig("jia", "demo", "1.0.0"))

	if s, _, _ := e.as("").do("POST", "/v1/rigs/jia/demo/versions", good, "application/gzip"); s != 401 {
		t.Errorf("no token: %d", s)
	}
	if s, _, _ := e.as("rgf_invalid").do("POST", "/v1/rigs/jia/demo/versions", good, "application/gzip"); s != 401 {
		t.Errorf("bad token: %d", s)
	}
	if s, _, _ := c.do("POST", "/v1/rigs/jia/demo/versions", good, "text/plain"); s != 415 {
		t.Errorf("content type: %d", s)
	}
	if s, _ := e.as(other).upload("jia", "demo", good); s != 403 {
		t.Errorf("publishing under someone else's name: %d", s)
	}
	if s, _ := c.upload("jia", "other-name", good); s != 422 {
		t.Errorf("manifest name must equal the URL: %d", s)
	}
	if s, _ := c.upload("Jia", "demo", good); s != 400 {
		t.Errorf("owner must be lowercase: %d", s)
	}
	if s, _ := c.upload("jia", "Bad Name", good); s != 400 {
		t.Errorf("rig name: %d", s)
	}
	if s, out := c.upload("jia", "demo", good); s != 202 || out["status"] != "pending" || out["version"] != "1.0.0" {
		t.Fatalf("%d %v", s, out)
	}
	// someone else cannot add a version to jia's rig even under their own namespace path, and an admin can
	if s, _ := e.as(other).upload("bob", "demo", rigTar(t, goodRig("bob", "demo", "1.0.0"))); s != 202 {
		t.Errorf("bob's own rig: %d", s)
	}
	adm, err := e.store.UpsertUser(t.Context(), registry.GitHubUser{ID: 3, Login: "root-admin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	admTok, _ := e.store.CreateToken(t.Context(), adm.ID, "t", time.Hour)
	if s, _ := e.as(admTok).upload("jia", "demo", rigTar(t, goodRig("jia", "demo", "1.0.1"))); s != 202 {
		t.Errorf("an admin may publish anywhere: %d", s)
	}
}

func TestReservedNames(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("openai", 7) // a real GitHub account can carry a vendor's name; the registry does not grant the namespace
	if s, out := e.as(tok).upload("openai", "tools", rigTar(t, goodRig("openai", "tools", "1.0.0"))); s != 403 {
		t.Fatalf("%d %v", s, out)
	}
	for _, o := range []string{"rigfile", "anthropic", "claude", "codex", "gemini", "cursor", "github", "google", "microsoft", "openai"} {
		if !registry.IsReservedOwner(o) || !registry.IsReservedOwner(strings.ToUpper(o)) {
			t.Errorf("%s must be reserved", o)
		}
	}
	if registry.IsReservedOwner("jia") {
		t.Error("jia is not reserved")
	}
}

func TestVersionsAreImmutable(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1)
	c := e.as(tok)
	data := rigTar(t, goodRig("jia", "demo", "1.0.0"))
	if s, _ := c.upload("jia", "demo", data); s != 202 {
		t.Fatal(s)
	}
	if s, out := c.upload("jia", "demo", data); s != 409 {
		t.Fatalf("a second upload of the same version: %d %v", s, out)
	}
	// even with different content
	changed := goodRig("jia", "demo", "1.0.0")
	changed["instructions/style.md"] = "# different\n"
	if s, _ := c.upload("jia", "demo", rigTar(t, changed)); s != 409 {
		t.Fatal("content of a version cannot change")
	}
	// concurrent uploads of a new version: exactly one wins
	var wg sync.WaitGroup
	codes := make(chan int, 6)
	up := rigTar(t, goodRig("jia", "demo", "2.0.0"))
	e.s.Lim = registry.NewLimiter(func() time.Time { return time.Now().Add(1000 * time.Hour) }) // do not let the rate limit interfere
	_ = up
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _ := c.upload("jia", "demo", up)
			codes <- s
		}()
	}
	wg.Wait()
	close(codes)
	created, conflicts := 0, 0
	for s := range codes {
		switch s {
		case 202:
			created++
		case 409:
			conflicts++
		}
	}
	if created != 1 || created+conflicts < 4 {
		t.Fatalf("created=%d conflicts=%d (other statuses are rate limits)", created, conflicts)
	}
}

func TestVisibilityPredicate(t *testing.T) {
	e := newEnv(t, nil)
	_, jia := e.userToken("jia", 1)
	_, bob := e.userToken("bob", 2)
	owner, stranger, anon := e.as(jia), e.as(bob), e.as("")
	if s, _ := owner.upload("jia", "demo", rigTar(t, goodRig("jia", "demo", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	// pending: only the owner sees it
	if owner.versionStatus("jia", "demo", "1.0.0") != "pending" {
		t.Fatal("owner sees pending")
	}
	for name, c := range map[string]*client{"stranger": stranger, "anonymous": anon} {
		if s, _ := c.get("/v1/rigs/jia/demo/versions/1.0.0"); s != 404 {
			t.Errorf("%s sees a pending version: %d", name, s)
		}
		if s, _ := c.get("/v1/rigs/jia/demo/versions/1.0.0/tarball"); s != 404 {
			t.Errorf("%s downloads a pending version: %d", name, s)
		}
	}
	e.scanAll()
	if owner.versionStatus("jia", "demo", "1.0.0") != "published" {
		t.Fatal("a clean rig is published by the scan")
	}
	// published but PRIVATE: still invisible to others, identical to a rig that does not exist
	sMissing, bMissing := anon.get("/v1/rigs/jia/nothing")
	sPriv, bPriv := anon.get("/v1/rigs/jia/demo")
	if sMissing != 404 || sPriv != 404 || string(bMissing) != string(bPriv) {
		t.Fatalf("private and missing must be indistinguishable: %d %q / %d %q", sPriv, bPriv, sMissing, bMissing)
	}
	if s, _ := stranger.get("/v1/rigs/jia/demo/versions/1.0.0/manifest"); s != 404 {
		t.Fatal("private manifest leaked")
	}
	// make it public: now everyone sees the published version, but not other people's pending ones
	if s, _, b := owner.do("POST", "/v1/rigs/jia/demo/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatalf("%d %s", s, b)
	}
	if s, _ := anon.get("/v1/rigs/jia/demo/versions/1.0.0/manifest"); s != 200 {
		t.Fatal("a public published version is readable")
	}
	if s, _ := owner.upload("jia", "demo", rigTar(t, goodRig("jia", "demo", "1.1.0"))); s != 202 {
		t.Fatal(s)
	}
	if s, _ := anon.get("/v1/rigs/jia/demo/versions/1.1.0"); s != 404 {
		t.Fatal("a pending version of a public rig is invisible to others")
	}
	_, body := anon.get("/v1/rigs/jia/demo")
	if strings.Contains(string(body), "1.1.0") {
		t.Fatalf("the rig page lists a pending version: %s", body)
	}
	// non-owners cannot change visibility, stars need a token
	if s, _, _ := stranger.do("POST", "/v1/rigs/jia/demo/visibility", []byte("visibility=private"), "application/x-www-form-urlencoded"); s != 403 {
		t.Fatalf("%d", s)
	}
	if s, _, _ := anon.do("PUT", "/v1/rigs/jia/demo/star", nil, ""); s != 401 {
		t.Fatal("stars need a token")
	}
	if s, _, _ := stranger.do("PUT", "/v1/rigs/jia/demo/star", nil, ""); s != 204 {
		t.Fatal("star")
	}
	if _, b := anon.get("/v1/rigs/jia/demo"); !strings.Contains(string(b), `"stars":1`) {
		t.Fatalf("%s", b)
	}
}

func TestScanRejectsSecretsWithoutLeakingThem(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1)
	c := e.as(tok)
	files := goodRig("jia", "leaky", "1.0.0")
	files["instructions/style.md"] = "# Style\ntoken = \"" + fakeSecret() + "\"\n"
	if s, _ := c.upload("jia", "leaky", rigTar(t, files)); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if got := c.versionStatus("jia", "leaky", "1.0.0"); got != "rejected" {
		t.Fatalf("status %s", got)
	}
	s, b := c.get("/v1/rigs/jia/leaky/versions/1.0.0")
	if s != 200 || !strings.Contains(string(b), `"kind":"secret"`) || !strings.Contains(string(b), "style.md") {
		t.Fatalf("the owner must see where: %s", b)
	}
	if strings.Contains(string(b), fakeSecret()) || strings.Contains(string(b), fakeSecret()[:20]) {
		t.Fatal("the findings leaked the value")
	}
	var stored string
	_ = e.store.DB.QueryRow(`SELECT scan_findings::text FROM versions`).Scan(&stored)
	if strings.Contains(stored, fakeSecret()[:20]) {
		t.Fatal("the value is in the database findings")
	}
	// nobody else can pull it, and it can never be made visible
	if s, _ := e.as("").get("/v1/rigs/jia/leaky/versions/1.0.0/tarball"); s != 404 {
		t.Fatal("a rejected version must not be downloadable")
	}
	if s, _, _ := c.do("POST", "/v1/rigs/jia/leaky/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 409 {
		t.Fatalf("no published version, so it cannot go public: %d", s)
	}
}

func TestPinningPublicVersusPrivate(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1)
	c := e.as(tok)
	unpinned := func(ver string) map[string]string {
		f := goodRig("jia", "pins", ver)
		f["rigfile.yaml"] = manifestYAML("jia", "pins", ver, "mcp_servers:\n  tool:\n    command: npx\n    args: ['-y', 'some-mcp']\n")
		return f
	}
	// private: published, with a warning
	if s, _ := c.upload("jia", "pins", rigTar(t, unpinned("1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	s, b := c.get("/v1/rigs/jia/pins/versions/1.0.0")
	if s != 200 || !strings.Contains(string(b), `"status":"published"`) || !strings.Contains(string(b), `"kind":"unpinned"`) {
		t.Fatalf("%s", b)
	}
	// going public is refused while the newest version has unpinned packages
	if st, _, body := c.do("POST", "/v1/rigs/jia/pins/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); st != 409 || !strings.Contains(string(body), "pin") {
		t.Fatalf("%d %s", st, body)
	}
	// a fixed version, then public is allowed
	fixed := goodRig("jia", "pins", "1.0.1")
	fixed["rigfile.yaml"] = manifestYAML("jia", "pins", "1.0.1", "mcp_servers:\n  tool:\n    command: npx\n    args: ['-y', 'some-mcp@1.2.3']\n")
	if s, _ := c.upload("jia", "pins", rigTar(t, fixed)); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if st, _, body := c.do("POST", "/v1/rigs/jia/pins/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); st != 204 {
		t.Fatalf("%d %s", st, body)
	}
	// once public, an unpinned new version is rejected
	if s, _ := c.upload("jia", "pins", rigTar(t, unpinned("2.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if got := c.versionStatus("jia", "pins", "2.0.0"); got != "rejected" {
		t.Fatalf("an unpinned version of a public rig: %s", got)
	}
}

func TestResolveRangesAndYankAndRemove(t *testing.T) {
	e := newEnv(t, nil)
	owner, tok := e.userToken("jia", 1)
	c, anon := e.as(tok), e.as("")
	for _, v := range []string{"1.0.0", "1.2.0", "1.3.0-rc1", "2.0.0"} {
		if s, out := c.upload("jia", "demo", rigTar(t, goodRig("jia", "demo", v))); s != 202 {
			t.Fatalf("%s: %d %v", v, s, out)
		}
		e.s.Lim = registry.NewLimiter(func() time.Time { return time.Now().Add(time.Duration(len(v)) * 1000 * time.Hour) })
	}
	e.scanAll()
	if s, _, _ := c.do("POST", "/v1/rigs/jia/demo/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatal(s)
	}
	resolve := func(cl *client, rng string) string {
		s, b := cl.get("/v1/rigs/jia/demo/resolve?range=" + rng)
		if s != 200 {
			return fmt.Sprintf("http %d", s)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m["version"].(string)
	}
	for rng, want := range map[string]string{"": "2.0.0", "%5E1": "1.2.0", "~1.0": "1.0.0", "1.2.0": "1.2.0", "%5E3": "http 404", "1.3.0-rc1": "1.3.0-rc1"} {
		if got := resolve(anon, rng); got != want {
			t.Errorf("resolve(%q) = %s, want %s", rng, got, want)
		}
	}
	// yank 1.2.0: ranges skip it, an exact request still finds it, the tarball still downloads with a marker
	if s, _, _ := c.do("POST", "/v1/rigs/jia/demo/versions/1.2.0/yank", []byte("reason=broken+hook"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatal(s)
	}
	if s, _, _ := anon.do("POST", "/v1/rigs/jia/demo/versions/1.0.0/yank", []byte("reason=x"), "application/x-www-form-urlencoded"); s != 401 {
		t.Fatal("yank needs a token")
	}
	if s, _, _ := c.do("POST", "/v1/rigs/jia/demo/versions/1.0.0/yank", nil, "application/x-www-form-urlencoded"); s != 400 {
		t.Fatal("yank needs a reason")
	}
	if got := resolve(anon, "%5E1"); got != "1.0.0" {
		t.Fatalf("a yanked version must not be picked by a range: %s", got)
	}
	if got := resolve(anon, "1.2.0"); got != "1.2.0" {
		t.Fatalf("an exact request for a yanked version still resolves: %s", got)
	}
	s, h, body := c.do("GET", "/v1/rigs/jia/demo/versions/1.2.0/tarball", nil, "")
	if s != 200 || h.Get("X-Rigfile-Yanked") != "true" || len(h.Get("X-Rigfile-SHA256")) != 64 || len(body) == 0 || h.Get("Cache-Control") == "" {
		t.Fatalf("yanked tarball: %d %v", s, h)
	}
	// takedown (admin): the version is gone for everyone but admins
	adm, _ := e.store.UpsertUser(t.Context(), registry.GitHubUser{ID: 9, Login: "admin-user"}, true)
	if err := e.store.RemoveVersion(t.Context(), "jia", "demo", "1.2.0", "malware report", owner); err == nil {
		t.Fatal("only admins remove")
	}
	if err := e.store.RemoveVersion(t.Context(), "jia", "demo", "1.2.0", "malware report", adm); err != nil {
		t.Fatal(err)
	}
	for _, cl := range []*client{anon, c} {
		if s, _ := cl.get("/v1/rigs/jia/demo/versions/1.2.0/tarball"); s != 404 {
			t.Fatalf("a removed version must be unavailable: %d", s)
		}
	}
	var audits int
	_ = e.store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('takedown','version.yank')`).Scan(&audits)
	if audits != 2 {
		t.Fatalf("audit entries: %d", audits)
	}
}

func TestSearchListsOnlyPublishedPublicRigs(t *testing.T) {
	e := newEnv(t, nil)
	_, jia := e.userToken("jia", 1)
	c, anon := e.as(jia), e.as("")
	for _, n := range []string{"data-science", "web-dev", "private-one"} {
		if s, _ := c.upload("jia", n, rigTar(t, goodRig("jia", n, "1.0.0"))); s != 202 {
			t.Fatal(s)
		}
		e.s.Lim = registry.NewLimiter(func() time.Time { return time.Now().Add(1000 * time.Hour * time.Duration(len(n))) })
	}
	e.scanAll()
	for _, n := range []string{"data-science", "web-dev"} {
		if s, _, _ := c.do("POST", "/v1/rigs/jia/"+n+"/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
			t.Fatal(s)
		}
	}
	search := func(q string) string {
		_, b := anon.get("/v1/search?q=" + q)
		return string(b)
	}
	if b := search("data"); !strings.Contains(b, "data-science") || strings.Contains(b, "web-dev") || strings.Contains(b, "private-one") {
		t.Fatalf("%s", b)
	}
	if b := search(""); !strings.Contains(b, "data-science") || !strings.Contains(b, "web-dev") || strings.Contains(b, "private-one") {
		t.Fatalf("%s", b)
	}
	// LIKE metacharacters are data, not patterns
	if b := search("%25"); strings.Contains(b, "data-science") {
		t.Fatalf("a literal %% must not match everything: %s", b)
	}
	if b := search("%27%3B+DROP+TABLE+rigs%3B--"); !strings.Contains(b, `"rigs":[]`) {
		t.Fatalf("%s", b)
	}
}

func TestWorkerRecoversAndLocks(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1)
	if s, _ := e.as(tok).upload("jia", "demo", rigTar(t, goodRig("jia", "demo", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	ctx := t.Context()
	// concurrent claims: exactly one worker gets the job
	var wg sync.WaitGroup
	got := make(chan bool, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := e.store.ClaimJob(ctx, fmt.Sprintf("w%d", i), time.Minute)
			got <- err == nil
		}(i)
	}
	wg.Wait()
	close(got)
	n := 0
	for ok := range got {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d workers claimed the same job", n)
	}
	if _, _, err := e.store.ClaimJob(ctx, "late", time.Minute); err == nil {
		t.Fatal("a running job must not be claimed again")
	}
	// the worker dies; after its lock expires another one picks the job up
	e.clk.add(2 * time.Minute)
	id, attempts, err := e.store.ClaimJob(ctx, "second", time.Minute)
	if err != nil || attempts != 2 {
		t.Fatalf("%v attempts=%d", err, attempts)
	}
	// repeated failures reject the version instead of retrying forever
	if err := e.store.RetryOrFail(ctx, id, 1, fmt.Errorf("boom")); err != nil {
		t.Fatal(err)
	}
	e.clk.add(time.Hour)
	_, _, _ = e.store.ClaimJob(ctx, "third", time.Minute)
	if err := e.store.RetryOrFail(ctx, id, 3, fmt.Errorf("boom")); err != nil {
		t.Fatal(err)
	}
	if got := e.as(tok).versionStatus("jia", "demo", "1.0.0"); got != "rejected" {
		t.Fatalf("after the last attempt: %s", got)
	}
}

func TestNoSQLIsBuiltFromStrings(t *testing.T) {
	// A cheap guard for docs/registry.md §7: queries are constants with placeholders. fmt.Sprintf, +-concatenation of
	// user values and string building around SQL keywords are how injection gets in.
	files, _ := filepathGlob(t)
	for _, f := range files {
		src := readFile(t, f)
		for _, bad := range []string{`Sprintf("SELECT`, `Sprintf("INSERT`, `Sprintf("UPDATE`, `Sprintf("DELETE`, `Sprintf(\"SELECT`, "Sprintf(`SELECT", "Sprintf(`INSERT", "Sprintf(`UPDATE", "Sprintf(`DELETE"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s builds SQL with Sprintf", f)
			}
		}
	}
}
