package registry_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/registry"
)

func TestLegalPagesAreServedAndMarkedAsDrafts(t *testing.T) {
	e := newEnv(t, nil)
	for path, want := range map[string]string{"/legal/terms": "Terms of Service", "/legal/acceptable-use": "Acceptable Use Policy", "/legal/takedown": "Takedown and abuse policy"} {
		code, page := getPage(t, e, nil, path)
		if code != 200 || !strings.Contains(page, want) || !strings.Contains(page, "has not been reviewed by a lawyer") {
			t.Errorf("%s: %d\n%s", path, code, page)
		}
	}
	if code, _ := getPage(t, e, nil, "/legal/nothing"); code != 404 {
		t.Fatal("unknown document")
	}
}

func TestReportFlowAndTakedown(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.userToken("jia", 1001)
	c := e.as(tok)
	publishPublic(t, e, c, "jia", "shared", "1.0.0", goodRig("jia", "shared", "1.0.0"))
	if s, _ := c.upload("jia", "hidden", rigTar(t, goodRig("jia", "hidden", "1.0.0"))); s != 202 {
		t.Fatal(s)
	}
	e.scanAll()

	// reporting needs a sign-in
	code, _ := getPage(t, e, nil, "/report?rig=jia/shared")
	if code != http.StatusFound {
		t.Fatalf("anonymous report form: %d", code)
	}
	e.gh.user = map[string]any{"id": 2002, "login": "bob", "name": "Bob", "avatar_url": ""}
	bob := e.signIn("/")
	code, page := getPage(t, e, bob, "/report?rig=jia/shared&version=1.0.0")
	if code != 200 || !strings.Contains(page, `value="jia/shared"`) {
		t.Fatalf("%d\n%s", code, page)
	}
	csrf := csrfFrom(t, page)
	send := func(vals url.Values) int {
		vals.Set("csrf", csrf)
		code, _ := postForm(t, e, bob, "/report", vals, e.srv.URL)
		return code
	}
	if code := send(url.Values{"rig": {"jia/shared"}, "version": {"1.0.0"}, "reason": {"malware"}, "details": {"runs a hidden curl"}}); code != 200 {
		t.Fatalf("a valid report: %d", code)
	}
	if code := send(url.Values{"rig": {"jia/shared"}, "reason": {"because"}}); code != 400 {
		t.Fatalf("an unknown reason: %d", code)
	}
	// reports cannot probe for private rigs: same answer as for a rig that does not exist
	a := send(url.Values{"rig": {"jia/hidden"}, "reason": {"other"}})
	b := send(url.Values{"rig": {"jia/nothing-here"}, "reason": {"other"}})
	if a != b || a != 400 {
		t.Fatalf("private vs missing: %d %d", a, b)
	}
	if code := send(url.Values{"rig": {"../../x"}, "reason": {"other"}}); code != 400 {
		t.Fatalf("malformed rig name: %d", code)
	}

	// an administrator handles it
	ctx := t.Context()
	rs, err := e.store.OpenReports(ctx)
	if err != nil || len(rs) != 1 || rs[0].Reporter != "bob" || rs[0].Reason != "malware" {
		t.Fatalf("%+v %v", rs, err)
	}
	adm := &registry.User{Login: "operator", IsAdmin: true}
	if err := e.store.RemoveVersion(ctx, "jia", "shared", "1.0.0", "malware report #1", adm); err != nil {
		t.Fatal(err)
	}
	if err := e.store.ResolveReport(ctx, rs[0].ID, "actioned", adm); err != nil {
		t.Fatal(err)
	}
	if err := e.store.ResolveReport(ctx, rs[0].ID, "actioned", adm); err == nil {
		t.Fatal("a report is closed once")
	}
	if err := e.store.ResolveReport(ctx, rs[0].ID, "actioned", &registry.User{}); err == nil {
		t.Fatal("only admins resolve reports")
	}
	if code, _ := getPage(t, e, nil, "/r/jia/shared/v/1.0.0"); code != 404 {
		t.Fatalf("the removed version must be gone: %d", code)
	}
	// removing a whole rig hides it entirely
	if err := e.store.RemoveVersion(ctx, "jia", "shared", "", "abuse", adm); err != nil {
		t.Fatal(err)
	}
	if code, _ := getPage(t, e, nil, "/r/jia/shared"); code != 404 {
		t.Fatal("a removed rig is gone")
	}
	if _, page := getPage(t, e, nil, "/"); strings.Contains(page, "jia/shared") {
		t.Fatal("a removed rig is not listed")
	}
	if es, _ := e.store.RecentAudit(ctx, 20); len(es) < 3 {
		t.Fatalf("audit entries: %d", len(es))
	}
}
