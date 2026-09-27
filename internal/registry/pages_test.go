package registry_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rigfile/rigfile/internal/registry"
)

func publishPublic(t *testing.T, e *env, c *client, owner, name, version string, files map[string]string) {
	t.Helper()
	if s, out := c.upload(owner, name, rigTar(t, files)); s != 202 {
		t.Fatalf("upload: %d %v", s, out)
	}
	e.scanAll()
	if s, _, b := c.do("POST", "/v1/rigs/"+owner+"/"+name+"/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatalf("visibility: %d %s", s, b)
	}
}

func getPage(t *testing.T, e *env, cl *http.Client, path string) (int, string) {
	t.Helper()
	if cl == nil {
		cl = e.client()
	}
	resp, err := cl.Get(e.url(path))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body(t, resp)
}

func TestPagesShowPublicRigsAndKeepPrivateOnesPrivate(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1001)
	c := e.as(tok)
	files := goodRig("jia", "shared", "1.0.0")
	files["README.md"] = "# Shared\n\nSome **bold** text and a [link](https://example.org/x).\n"
	files["rigfile.yaml"] = manifestYAML("jia", "shared", "1.0.0", "targets:\n  include: [claude-code, codex]\nsecrets:\n  demo/key: {description: Demo key}\nlogins:\n  - {provider: github}\n")
	publishPublic(t, e, c, "jia", "shared", "1.0.0", files)
	if s, _ := c.upload("jia", "hidden", rigTar(t, goodRig("jia", "hidden", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()

	code, page := getPage(t, e, nil, "/")
	if code != 200 || !strings.Contains(page, "jia/shared") || strings.Contains(page, "jia/hidden") || !strings.Contains(page, "rigfile pull owner/name --registry "+e.srv.URL) {
		t.Fatalf("home: %d\n%s", code, page)
	}
	if _, page = getPage(t, e, nil, "/search?q=shar"); !strings.Contains(page, "jia/shared") {
		t.Fatalf("search:\n%s", page)
	}
	if _, page = getPage(t, e, nil, "/search?q=zzz"); !strings.Contains(page, "Nothing matches") {
		t.Fatalf("empty search:\n%s", page)
	}
	code, page = getPage(t, e, nil, "/r/jia/shared")
	for _, want := range []string{"jia/shared", "<strong>bold</strong>", `rel="`, "rigfile pull jia/shared --registry " + e.srv.URL, "apiVersion: rigfile.dev/v1", "instructions/style.md", "claude-code", "demo/key", "github", "Report this rig", "sha256"} {
		if code != 200 || !strings.Contains(page, want) {
			t.Fatalf("rig page missing %q (code %d):\n%s", want, code, page)
		}
	}
	// a file view and the raw text
	code, page = getPage(t, e, nil, "/r/jia/shared/v/1.0.0/files/instructions/style.md")
	if code != 200 || !strings.Contains(page, "be terse") {
		t.Fatalf("file view: %d\n%s", code, page)
	}
	resp, _ := e.client().Get(e.url("/r/jia/shared/v/1.0.0/raw/instructions/style.md"))
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("raw file headers: %v", resp.Header)
	}
	body(t, resp)
	// files that are not in the index, traversal attempts, and unknown versions are 404
	for _, p := range []string{"/r/jia/shared/v/1.0.0/files/nothing.md", "/r/jia/shared/v/1.0.0/files/../../etc/passwd", "/r/jia/shared/v/9.9.9", "/r/jia/shared/v/1.0.0/raw/%2e%2e/x"} {
		if code, _ := getPage(t, e, nil, p); code != 404 && code != 400 && code != 301 && code != 307 { // the mux itself cleans ".." and redirects
			t.Errorf("%s: %d", p, code)
		}
	}
	// the private rig: 404 for everyone but its owner
	if code, _ := getPage(t, e, nil, "/r/jia/hidden"); code != 404 {
		t.Fatalf("anonymous sees a private rig: %d", code)
	}
	owner := e.signIn("/")
	code, page = getPage(t, e, owner, "/r/jia/hidden")
	if code != 200 || !strings.Contains(page, "private") || !strings.Contains(page, "Make public") {
		t.Fatalf("the owner sees the private rig: %d\n%s", code, page)
	}
	// the profile lists only what the viewer may see
	if _, page = getPage(t, e, nil, "/u/jia"); !strings.Contains(page, "jia/shared") || strings.Contains(page, "jia/hidden") {
		t.Fatalf("profile for a stranger:\n%s", page)
	}
	if _, page = getPage(t, e, owner, "/u/jia"); !strings.Contains(page, "jia/hidden") {
		t.Fatalf("profile for the owner:\n%s", page)
	}
	if code, _ := getPage(t, e, nil, "/u/nobody"); code != 404 {
		t.Fatal("unknown profile")
	}
	if code, _ := getPage(t, e, nil, "/no/such/page"); code != 404 {
		t.Fatal("unknown page")
	}
}

func postForm(t *testing.T, e *env, cl *http.Client, path string, vals url.Values, origin string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.url(path), strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body(t, resp)
}

func TestStarAndVisibilityFormsNeedTheSessionCSRFAndOrigin(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1001)
	c := e.as(tok)
	publishPublic(t, e, c, "jia", "shared", "1.0.0", goodRig("jia", "shared", "1.0.0"))
	cl := e.signIn("/")
	_, page := getPage(t, e, cl, "/r/jia/shared")
	csrf := csrfFrom(t, page)

	if code, _ := postForm(t, e, cl, "/r/jia/shared/star", url.Values{"action": {"star"}}, e.srv.URL); code != 403 {
		t.Fatalf("no csrf token: %d", code)
	}
	if code, _ := postForm(t, e, cl, "/r/jia/shared/star", url.Values{"action": {"star"}, "csrf": {csrf}}, "https://evil.example"); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	if code, _ := postForm(t, e, e.client(), "/r/jia/shared/star", url.Values{"action": {"star"}, "csrf": {csrf}}, e.srv.URL); code != 401 {
		t.Fatalf("no session: %d", code)
	}
	if code, _ := postForm(t, e, cl, "/r/jia/shared/star", url.Values{"action": {"star"}, "csrf": {csrf}}, e.srv.URL); code != http.StatusSeeOther {
		t.Fatalf("a valid star: %d", code)
	}
	if _, page = getPage(t, e, cl, "/r/jia/shared"); !strings.Contains(page, "Unstar") || !strings.Contains(page, "(1)") {
		t.Fatalf("after starring:\n%s", page)
	}
	if code, _ := postForm(t, e, cl, "/r/jia/shared/visibility", url.Values{"visibility": {"private"}, "csrf": {csrf}}, e.srv.URL); code != http.StatusSeeOther {
		t.Fatalf("owner makes it private: %d", code)
	}
	if code, _ := getPage(t, e, nil, "/r/jia/shared"); code != 404 {
		t.Fatal("now private")
	}
	// another signed-in user cannot change it
	e.gh.user = map[string]any{"id": 2002, "login": "bob", "name": "Bob", "avatar_url": ""}
	e.clk.add(time.Minute)
	bob := e.signIn("/")
	_, bpage := getPage(t, e, bob, "/")
	if code, out := postForm(t, e, bob, "/r/jia/shared/visibility", url.Values{"visibility": {"public"}, "csrf": {csrfFrom(t, bpage)}}, e.srv.URL); code != 404 {
		t.Fatalf("a stranger changing visibility of a private rig: %d %s", code, out)
	}
}

func TestPagesEscapeHostileContent(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1001)
	c := e.as(tok)
	files := goodRig("jia", "evil", "1.0.0")
	files["README.md"] = "# Hi\n\n<script>alert('readme')</script>\n\n<img src=x onerror=alert(1)>\n\n<iframe src=\"https://evil.example\"></iframe>\n\n" +
		"[click](javascript:alert(2)) and [data](data:text/html;base64,PHNjcmlwdD4=) and ![p](javascript:alert(3))\n\n" +
		"<a href=\"javascript:alert(4)\" onclick=\"alert(5)\">raw anchor</a>\n\n[ok](https://example.org)\n\n`<script>inline</script>`\n"
	files["instructions/style.md"] = "</pre></code><script>alert('file')</script>\n"
	files[`instructions/a"><script>alert(9)</script>.md`] = "hostile file name"
	files["rigfile.yaml"] = manifestYAML("jia", "evil", "1.0.0", "") + "# </pre><script>alert('manifest')</script>\n"
	// the description comes from the manifest too
	files["rigfile.yaml"] = strings.Replace(files["rigfile.yaml"], "description: A test rig", "description: <script>alert('desc')</script>", 1)
	publishPublic(t, e, c, "jia", "evil", "1.0.0", files)

	pages := []string{"/", "/search?q=evil", "/u/jia", "/r/jia/evil", "/r/jia/evil/v/1.0.0", "/r/jia/evil/v/1.0.0/files/instructions/style.md"}
	scriptTag := regexp.MustCompile(`(?i)<script`)
	for _, p := range pages {
		code, page := getPage(t, e, nil, p)
		if code != 200 {
			t.Fatalf("%s: %d", p, code)
		}
		if scriptTag.MatchString(page) {
			t.Errorf("%s contains a <script tag:\n%s", p, page)
		}
		low := strings.ToLower(page)
		for _, bad := range []string{"onerror=", "onclick=", "javascript:", "<iframe", "data:text/html"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s contains %q", p, bad)
			}
		}
	}
	_, page := getPage(t, e, nil, "/r/jia/evil")
	if !strings.Contains(page, "&lt;script&gt;") { // the manifest and description are shown, escaped
		t.Errorf("hostile text must be visible as text, escaped")
	}
	if !strings.Contains(page, `href="https://example.org"`) || !strings.Contains(page, "noopener") && !strings.Contains(page, "nofollow") {
		t.Errorf("safe links stay, with rel attributes:\n%s", page)
	}
	// the raw view is plain text with a locked-down policy, whatever the content
	resp, _ := e.client().Get(e.url("/r/jia/evil/v/1.0.0/raw/instructions/style.md"))
	b := body(t, resp)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || !strings.Contains(b, "<script>") {
		t.Fatalf("raw: %v %q", resp.Header, b)
	}
}

func TestOwnerSeesPendingAndRejectedVersionsOthersDoNot(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1001)
	c := e.as(tok)
	publishPublic(t, e, c, "jia", "demo", "1.0.0", goodRig("jia", "demo", "1.0.0"))
	bad := goodRig("jia", "demo", "1.1.0")
	bad["instructions/style.md"] = "token = \"" + fakeSecret() + "\"\n"
	if s, _ := c.upload("jia", "demo", rigTar(t, bad)); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	owner := e.signIn("/")
	code, page := getPage(t, e, owner, "/r/jia/demo/v/1.1.0")
	if code != 200 || !strings.Contains(page, "rejected") || !strings.Contains(page, "secret") {
		t.Fatalf("owner: %d\n%s", code, page)
	}
	if strings.Contains(page, fakeSecret()) {
		t.Fatal("the page leaked the secret")
	}
	if code, _ := getPage(t, e, nil, "/r/jia/demo/v/1.1.0"); code != 404 {
		t.Fatalf("anonymous sees a rejected version: %d", code)
	}
	if _, page = getPage(t, e, nil, "/r/jia/demo"); strings.Contains(page, "1.1.0") {
		t.Fatal("the public page lists a rejected version")
	}
	_ = registry.CompareVersions
}
