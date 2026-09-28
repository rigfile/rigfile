package localui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type fake struct {
	mu      sync.Mutex
	stored  map[string]string
	applied int
}

func newFixture(t *testing.T) (*Server, *fake, string) {
	t.Helper()
	f := &fake{stored: map[string]string{}}
	s := &Server{B: Backend{
		Title: "ada/demo 1.0.0",
		Plan:  func() (string, error) { return "Rig: ada/demo@1.0.0\n  + skills/x <script>alert(1)</script>", nil },
		Needs: func() ([]Need, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			st := "not set"
			if _, ok := f.stored["alpaca/key"]; ok {
				st = "set"
			}
			return []Need{{Kind: "secret", Ref: "alpaca/key", Description: "the API key", Status: st}, {Kind: "login", Ref: "github", Method: "browser"}}, nil
		},
		SetSecret: func(ref string, v []byte) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if string(v) == "explode" {
				return errors.New("keychain said no to explode")
			}
			f.stored[ref] = string(v)
			return nil
		},
		Apply: func() (string, error) { f.mu.Lock(); f.applied++; f.mu.Unlock(); return "applied 3 change(s)", nil },
	}}
	u, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx, c := context.WithCancel(context.Background()); c(); s.Wait(ctx) })
	return s, f, u
}

func browser(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func read(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func csrfOf(t *testing.T, page string) string {
	i := strings.Index(page, `name="csrf" value="`)
	if i < 0 {
		t.Fatalf("no csrf token in the page:\n%s", page)
	}
	rest := page[i+len(`name="csrf" value="`):]
	return rest[:strings.Index(rest, `"`)]
}

func TestLandingSwapsTheTokenForACookieAndEscapesEverything(t *testing.T) {
	s, _, landing := newFixture(t)
	b := browser(t)
	// no cookie, no token: refused
	resp, _ := http.Get("http://" + s.addr + "/")
	if resp.StatusCode != 403 {
		t.Fatalf("%d", resp.StatusCode)
	}
	// wrong token: refused
	if resp, _ := b.Get("http://" + s.addr + "/?t=wrong"); resp.StatusCode != 403 {
		t.Fatalf("%d", resp.StatusCode)
	}
	// the real token lands, sets a cookie and leaves the address clean
	resp, err := b.Get(landing)
	if err != nil || resp.StatusCode != 303 || resp.Header.Get("Location") != "/" || !strings.Contains(resp.Header.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(resp.Header.Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("%v %v", resp, err)
	}
	resp, _ = b.Get("http://" + s.addr + "/")
	page := read(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(page, "ada/demo 1.0.0") || strings.Contains(page, "<script>alert(1)") || !strings.Contains(page, "&lt;script&gt;alert(1)") {
		t.Fatalf("the plan must be shown escaped: %s", page)
	}
	for _, want := range []string{"Secret: alpaca/key", "not set yet", "Sign in: github", "rigfile logins", "type=\"password\""} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	h := resp.Header
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || strings.Contains(h.Get("Content-Security-Policy"), "unsafe") || h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("%v", h)
	}
	if strings.Contains(page, "<script") || strings.Contains(page, "style=") {
		t.Fatal("no script and no inline style")
	}
}

func TestGuardRefusesRebindingCrossOriginAndMissingCSRF(t *testing.T) {
	s, f, landing := newFixture(t)
	b := browser(t)
	b.Get(landing)
	get := func(host, origin, site string) int {
		req, _ := http.NewRequest("GET", "http://"+s.addr+"/", nil)
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		resp, err := b.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for name, c := range map[string]struct {
		host, origin, site string
		want               int
	}{
		"same origin": {"", "", "same-origin", 200}, "typed address": {"", "", "none", 200},
		"rebinding": {"evil.example.test:80", "", "", 403}, "foreign origin": {"", "http://evil.example.test", "", 403}, "cross-site fetch": {"", "", "cross-site", 403},
		// real Chrome sends the literal string "null" as Origin for a top-level form POST when the page's own
		// Referrer-Policy is no-referrer (confirmed live: Chrome 153, macOS) — this must still be accepted, with
		// Sec-Fetch-Site carrying the weight instead of Origin.
		"null origin, same-origin fetch metadata": {"", "null", "same-origin", 200},
		// a cross-site request cannot forge Sec-Fetch-Site (the browser sets it, unspoofable by page script), so
		// pairing a "null" Origin with cross-site fetch metadata must still be refused.
		"null origin, cross-site fetch metadata": {"", "null", "cross-site", 403},
	} {
		if got := get(c.host, c.origin, c.site); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	// a POST without the page's csrf token changes nothing
	post := func(path string, form url.Values) int {
		resp, err := b.PostForm("http://"+s.addr+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("/secret", url.Values{"ref": {"alpaca/key"}, "value": {"v"}}); got != 403 {
		t.Fatalf("%d", got)
	}
	if got := post("/apply", url.Values{"confirm": {"yes"}, "csrf": {"guess"}}); got != 403 {
		t.Fatalf("%d", got)
	}
	// a cookie-less client with the right csrf token still cannot
	resp, _ := b.Get("http://" + s.addr + "/")
	tok := csrfOf(t, read(t, resp))
	anon := &http.Client{}
	resp, _ = anon.PostForm("http://"+s.addr+"/apply", url.Values{"confirm": {"yes"}, "csrf": {tok}})
	if resp.StatusCode != 403 {
		t.Fatalf("%d", resp.StatusCode)
	}
	if len(f.stored) != 0 || f.applied != 0 {
		t.Fatal("a refused request changed something")
	}
}

func TestStoringSecretsAndApplying(t *testing.T) {
	s, f, landing := newFixture(t)
	b := browser(t)
	b.Get(landing)
	resp, _ := b.Get("http://" + s.addr + "/")
	tok := csrfOf(t, read(t, resp))
	post := func(path string, form url.Values) (int, string) {
		form.Set("csrf", tok)
		resp, err := b.PostForm("http://"+s.addr+path, form)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, read(t, resp)
	}
	secret := "FAKE-" + "UI-VALUE-" + "8c2e7d41"
	// only what the rig declares can be stored
	if c, body := post("/secret", url.Values{"ref": {"other/thing"}, "value": {secret}}); c != 400 || !strings.Contains(body, "not one this rig asks for") {
		t.Fatalf("%d %s", c, body)
	}
	if c, _ := post("/secret", url.Values{"ref": {"alpaca/key"}, "value": {""}}); c != 400 {
		t.Fatalf("%d", c)
	}
	if c, _ := post("/secret", url.Values{"ref": {"alpaca/key"}, "value": {strings.Repeat("x", 20<<10)}}); c != 400 && c != 403 {
		t.Fatalf("an oversized value: %d", c)
	}
	// an error that quotes the value never shows it
	if c, body := post("/secret", url.Values{"ref": {"alpaca/key"}, "value": {"explode"}}); c != 500 || strings.Contains(body, "explode") && !strings.Contains(body, "[value]") {
		t.Fatalf("%d %s", c, body)
	}
	c, _ := post("/secret", url.Values{"ref": {"alpaca/key"}, "value": {secret + "\n"}})
	if c != 303 || f.stored["alpaca/key"] != secret {
		t.Fatalf("%d %q", c, f.stored["alpaca/key"])
	}
	resp, _ = b.Get("http://" + s.addr + "/?done=secret")
	page := read(t, resp)
	if strings.Contains(page, secret) || !strings.Contains(page, "already stored") || !strings.Contains(page, "Saved.") {
		t.Fatalf("the value must never come back: %s", page)
	}
	// apply needs the confirmation
	if c, body := post("/apply", url.Values{}); c != 400 || !strings.Contains(body, "Tick the box") || f.applied != 0 {
		t.Fatalf("%d %s", c, body)
	}
	if c, body := post("/apply", url.Values{"confirm": {"yes"}}); c != 200 || !strings.Contains(body, "applied 3 change(s)") || f.applied != 1 {
		t.Fatalf("%d %s", c, body)
	}
}

func TestQuitAndIdleTimeout(t *testing.T) {
	s, _, landing := newFixture(t)
	b := browser(t)
	b.Get(landing)
	resp, _ := b.Get("http://" + s.addr + "/")
	tok := csrfOf(t, read(t, resp))
	done := make(chan struct{})
	go func() { s.Wait(context.Background()); close(done) }()
	resp, _ = b.PostForm("http://"+s.addr+"/quit", url.Values{"csrf": {tok}})
	if !strings.Contains(read(t, resp), "stopped") {
		t.Fatal("quit")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the Quit button must stop the server")
	}

	idle := &Server{Idle: 50 * time.Millisecond, B: Backend{Title: "x", Plan: func() (string, error) { return "", nil }, Needs: func() ([]Need, error) { return nil, nil }}}
	if _, err := idle.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { idle.Wait(context.Background()); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("an unused page must stop by itself")
	}
}

// TestApplySucceedsWithARealBrowsersNullOrigin is a regression test for a real bug (found live, Chrome 153,
// macOS, 2026-09-27): every POST in this page is a plain <form method="post"> — there is no script, by design —
// so a click on Apply is a top-level navigation, not a fetch. For that kind of request, a spec-compliant browser
// sends the literal string "null" as Origin, not the real origin, because the page's own Referrer-Policy is
// no-referrer. go's http.Client (used by every other test here via PostForm) never sends an Origin header at all,
// so this case slipped past every existing test, including a hand check with curl (which had to be told an
// Origin to send). This test sends exactly what a real browser sends.
func TestApplySucceedsWithARealBrowsersNullOrigin(t *testing.T) {
	s, f, landing := newFixture(t)
	b := browser(t)
	b.Get(landing)
	resp, _ := b.Get("http://" + s.addr + "/")
	tok := csrfOf(t, read(t, resp))
	form := url.Values{"confirm": {"yes"}, "csrf": {tok}}
	req, _ := http.NewRequest("POST", "http://"+s.addr+"/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	got, err := b.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := read(t, got)
	if got.StatusCode != 200 || !strings.Contains(body, "applied 3 change(s)") {
		t.Fatalf("a real browser's Apply click must succeed: %d %s", got.StatusCode, body)
	}
	if f.applied != 1 {
		t.Fatalf("applied=%d", f.applied)
	}
}
