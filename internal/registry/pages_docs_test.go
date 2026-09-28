package registry_test

import (
	"strings"
	"testing"
)

func TestDocsPagesRender(t *testing.T) {
	e := newEnv(t, nil)
	code, page := getPage(t, e, nil, "/docs")
	if code != 200 || !strings.Contains(page, "Getting started") || !strings.Contains(page, "CLI reference") {
		t.Fatalf("/docs: %d\n%s", code, page)
	}
	for _, p := range []struct{ path, want string }{
		{"/docs/getting-started", "Install the CLI"},
		{"/docs/cli", "rigfile pull"},
		{"/docs/manifest", "mcp_servers"},
		{"/docs/faq", "no registry is configured"},
	} {
		code, page := getPage(t, e, nil, p.path)
		if code != 200 || !strings.Contains(page, p.want) {
			t.Errorf("%s: %d, want %q", p.path, code, p.want)
		}
		if strings.Contains(page, "%REGISTRY%") {
			t.Errorf("%s: the registry placeholder was not replaced", p.path)
		}
	}
	if code, page := getPage(t, e, nil, "/docs/getting-started"); code != 200 || !strings.Contains(page, "rigfile login --registry "+e.srv.URL) {
		t.Errorf("examples should name this registry's own address:\n%s", page)
	}
	if code, page := getPage(t, e, nil, "/docs/tools"); code != 200 || !strings.Contains(page, "Claude Code") || !strings.Contains(page, "Zed") {
		t.Errorf("/docs/tools: %d", code)
	}
	if code, _ := getPage(t, e, nil, "/docs/no-such-page"); code != 404 {
		t.Errorf("unknown docs page: %d, want 404", code)
	}
}

func TestExploreAndHomeShell(t *testing.T) {
	e := newEnv(t, nil)
	if _, page := getPage(t, e, nil, "/search"); !strings.Contains(page, "Explore rigs") {
		t.Error("an empty search is the Explore page")
	}
	_, page := getPage(t, e, nil, "/")
	for _, want := range []string{"Get started", `href="/docs"`, `rel="icon"`, `name="description"`, "Claude Code"} {
		if !strings.Contains(page, want) {
			t.Errorf("home page lacks %q", want)
		}
	}
}
