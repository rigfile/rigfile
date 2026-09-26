package pkgcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

func mfst(t *testing.T, servers string) *manifest.Manifest {
	m, err := manifest.Parse([]byte("apiVersion: rigfile.dev/v1\nname: x/demo\nversion: 1.0.0\nmcp_servers:\n" + servers))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestExtractPinnedPackages(t *testing.T) {
	m := mfst(t, `  a: {command: npx, args: ['-y', 'some-mcp@1.2.3']}
  b: {command: npx, args: ['-y', '@scope/tool@0.4.0']}
  c: {command: uvx, args: ['pypi-tool==2.0.1']}
  d: {command: pipx, args: ['run', 'other-tool==0.9']}
  e: {command: npx, args: ['-y', 'unpinned-mcp']}
  f: {command: bash, args: ['-c', 'run']}
  g: {command: npx, args: ['-y', 'bad name@1.0.0']}
  h: {url: 'https://mcp.example.test/mcp', transport: http}
  i: {command: pnpm, args: ['dlx', 'dlx-tool@3.1.0']}
`)
	got := map[string]bool{}
	for _, p := range Extract(m) {
		got[p.String()] = true
	}
	for _, want := range []string{"npm some-mcp@1.2.3", "npm @scope/tool@0.4.0", "PyPI pypi-tool@2.0.1", "PyPI other-tool@0.9", "npm dlx-tool@3.1.0"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if len(got) != 5 {
		t.Errorf("only pinned, well-formed packages are returned: %v", got)
	}
}

func fakeOSV(t *testing.T, handler func(req map[string]any) (int, string)) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/query" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		code, body := handler(m)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}
}

func TestPackageLookups(t *testing.T) {
	var last map[string]any
	c := fakeOSV(t, func(req map[string]any) (int, string) {
		last = req
		pkg := req["package"].(map[string]any)
		switch pkg["name"] {
		case "evil-mcp":
			return 200, `{"vulns":[{"id":"MAL-2026-1234","summary":"Malicious code in evil-mcp","database_specific":{}}]}`
		case "old-mcp":
			return 200, `{"vulns":[{"id":"GHSA-xxxx-yyyy-zzzz","summary":"Prototype pollution","database_specific":{"severity":"HIGH"}}]}`
		case "clean-mcp":
			return 200, `{}`
		case "broken-mcp":
			return 200, `not json`
		case "down-mcp":
			return 503, `busy`
		case "hostile-mcp":
			return 200, `{"vulns":[{"id":"GHSA-a","summary":"<script>alert(1)</script>\u0007‮` + strings.Repeat("A", 500) + `"}]}`
		}
		return 200, `{}`
	})
	ctx := context.Background()
	adv, err := c.Check(ctx, Package{"npm", "evil-mcp", "1.0.0"})
	if err != nil || len(adv) != 1 || !adv[0].Malicious() {
		t.Fatalf("malicious: %+v %v", adv, err)
	}
	if last["version"] != "1.0.0" || last["package"].(map[string]any)["ecosystem"] != "npm" {
		t.Fatalf("request shape: %v", last)
	}
	if adv, err = c.Check(ctx, Package{"npm", "old-mcp", "1.0.0"}); err != nil || len(adv) != 1 || adv[0].Malicious() || adv[0].Severity != "HIGH" {
		t.Fatalf("vulnerable: %+v %v", adv, err)
	}
	if adv, err = c.Check(ctx, Package{"PyPI", "clean-mcp", "1.0.0"}); err != nil || len(adv) != 0 {
		t.Fatalf("clean: %+v %v", adv, err)
	}
	for _, name := range []string{"broken-mcp", "down-mcp"} {
		if _, err := c.Check(ctx, Package{"npm", name, "1.0.0"}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s must be 'unavailable', got %v", name, err)
		}
	}
	// text from OSV is bounded and stripped of control and bidi characters before it is stored
	adv, _ = c.Check(ctx, Package{"npm", "hostile-mcp", "1.0.0"})
	if len(adv) != 1 || len(adv[0].Summary) > 160 || strings.ContainsAny(adv[0].Summary, "\a‮") {
		t.Fatalf("%q", adv[0].Summary)
	}
	// nothing malformed is ever sent
	for _, p := range []Package{{"npm", "../x", "1"}, {"npm", "ok", "1 2"}, {"crates", "ok", "1"}, {"npm", "", "1"}} {
		if _, err := c.Check(ctx, p); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%v must not be sent: %v", p, err)
		}
	}
	// an unreachable server
	dead := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := dead.Check(ctx, Package{"npm", "x", "1.0.0"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
}
