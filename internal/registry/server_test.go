package registry_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/registry"
	"github.com/digitaldreamer3462/rigfile/internal/registry/blob"
	"github.com/digitaldreamer3462/rigfile/internal/registry/dbtest"
)

// fakeGitHub answers the two endpoints the registry calls. The account it returns is settable.
type fakeGitHub struct {
	srv  *httptest.Server
	user map[string]any
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{user: map[string]any{"id": 1001, "login": "Jia", "name": "Jia X", "avatar_url": "https://avatars.githubusercontent.com/u/1001"}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.PostFormValue("code") != "good-code" || r.PostFormValue("client_secret") != "gh-secret" {
			_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"gh-fake-token"}`))
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-fake-token" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(f.user)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type env struct {
	t     *testing.T
	srv   *httptest.Server
	s     *registry.Server
	store *registry.Store
	gh    *fakeGitHub
	clk   *clock
}

func newEnv(t *testing.T, mut func(*registry.Config)) *env {
	t.Helper()
	db := dbtest.New(t)
	st := registry.NewStore(db)
	clk := &clock{t: time.Now().UTC()}
	st.Now = clk.now
	gh := newFakeGitHub(t)
	e := &env{t: t, store: st, gh: gh, clk: clk}
	cfg := registry.Config{Listen: "x", PublicURL: "http://127.0.0.1:0", DatabaseURL: "x", Blob: "fs:x", GitHubID: "gh-id", GitHubSecret: "gh-secret",
		GitHubWeb: gh.srv.URL, GitHubAPI: gh.srv.URL, SessionTTL: time.Hour, TokenTTL: 24 * time.Hour, MaxUpload: 1 << 20, ScanWorkers: 1}
	if mut != nil {
		mut(&cfg)
	}
	// the public URL must be the test server's own origin (the Origin check compares against it)
	holder := &registry.Server{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { holder.Handler().ServeHTTP(w, r) }))
	t.Cleanup(e.srv.Close)
	cfg.PublicURL = e.srv.URL
	gcli := &registry.GitHubHTTP{ClientID: cfg.GitHubID, ClientSecret: cfg.GitHubSecret, WebBase: cfg.GitHubWeb, APIBase: cfg.GitHubAPI}
	e.s = registry.NewServer(cfg, st, blob.FS{Root: t.TempDir()}, gcli, nil)
	*holder = *e.s
	return e
}

func (e *env) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) url(p string) string { return e.srv.URL + p }

// signIn performs the whole OAuth dance for the fake GitHub account and returns a client holding the session.
func (e *env) signIn(next string) *http.Client {
	e.t.Helper()
	c := e.client()
	resp, err := c.Get(e.url("/login?next=" + url.QueryEscape(next)))
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc.Query().Get("state") == "" || loc.Query().Get("client_id") != "gh-id" {
		e.t.Fatalf("login did not redirect to GitHub: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = c.Get(e.url("/auth/callback?code=good-code&state=" + loc.Query().Get("state")))
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		e.t.Fatalf("callback: %d", resp.StatusCode)
	}
	return c
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func csrfFrom(t *testing.T, html string) string {
	m := csrfRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no csrf token in the page:\n%s", html)
	}
	return m[1]
}

func TestOAuthStateIsChecked(t *testing.T) {
	e := newEnv(t, nil)
	c := e.client()
	resp, _ := c.Get(e.url("/login?next=/device%3Fuser_code%3DBCDF-GHJK"))
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")
	if len(state) < 30 {
		t.Fatalf("state must be random: %q", state)
	}
	if loc.Query().Get("redirect_uri") != e.srv.URL+"/auth/callback" || loc.Query().Get("scope") != "" {
		t.Fatalf("authorize URL: %s", loc)
	}

	// wrong state: refused, and no session is created
	resp, _ = c.Get(e.url("/auth/callback?code=good-code&state=wrong"))
	if resp.StatusCode != 400 {
		t.Fatalf("wrong state: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// the state is single use: the cookie was consumed by the failed attempt
	resp, _ = c.Get(e.url("/auth/callback?code=good-code&state=" + state))
	if resp.StatusCode != 400 {
		t.Fatalf("replay: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// no cookie at all (login started in another browser)
	resp, _ = e.client().Get(e.url("/auth/callback?code=good-code&state=" + state))
	if resp.StatusCode != 400 {
		t.Fatalf("no cookie: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// a code GitHub rejects
	c2 := e.client()
	resp, _ = c2.Get(e.url("/login"))
	resp.Body.Close()
	loc, _ = url.Parse(resp.Header.Get("Location"))
	resp, _ = c2.Get(e.url("/auth/callback?code=bad&state=" + loc.Query().Get("state")))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("bad code: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// the happy path lands on the requested same-site page, with a hardened session cookie
	c3 := e.client()
	resp, _ = c3.Get(e.url("/login?next=%2Fdevice%3Fuser_code%3DBCDF-GHJK"))
	resp.Body.Close()
	loc, _ = url.Parse(resp.Header.Get("Location"))
	resp, _ = c3.Get(e.url("/auth/callback?code=good-code&state=" + loc.Query().Get("state")))
	resp.Body.Close()
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/device?user_code=BCDF-GHJK" {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	var sess *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "rigfile_session" {
			sess = ck
		}
	}
	if sess == nil || !sess.HttpOnly || sess.SameSite != http.SameSiteLaxMode || sess.Path != "/" || len(sess.Value) < 40 {
		t.Fatalf("session cookie: %+v", sess)
	}
	// the account was created with a lowercased login
	if u, err := e.store.UserByLogin(t.Context(), "jia"); err != nil || u.GitHubID != 1001 {
		t.Fatalf("%v", err)
	}
}

func TestOpenRedirectsAreRefused(t *testing.T) {
	e := newEnv(t, nil)
	for _, next := range []string{"//evil.example", "https://evil.example/x", `/\evil.example`, "javascript:alert(1)", "http:evil.example"} {
		e.clk.add(time.Minute) // the login endpoints are rate limited; let the budget refill
		c := e.client()
		resp, _ := c.Get(e.url("/login?next=" + url.QueryEscape(next)))
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		resp, _ = c.Get(e.url("/auth/callback?code=good-code&state=" + loc.Query().Get("state")))
		resp.Body.Close()
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "/" {
			t.Errorf("next=%q must fall back to /, got %d %q", next, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

func TestDisabledAccountCannotSignIn(t *testing.T) {
	e := newEnv(t, nil)
	e.signIn("/")
	if err := e.store.SetDisabled(t.Context(), "jia", true); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	resp, _ := c.Get(e.url("/login"))
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp, _ = c.Get(e.url("/auth/callback?code=good-code&state=" + loc.Query().Get("state")))
	if resp.StatusCode != 403 {
		t.Fatalf("%d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCSRF(t *testing.T) {
	e := newEnv(t, nil)
	c := e.signIn("/")
	resp, _ := c.Get(e.url("/device"))
	page := body(t, resp)
	csrf := csrfFrom(t, page)
	post := func(client *http.Client, origin, csrfVal string) *http.Response {
		req, _ := http.NewRequest("POST", e.url("/logout"), strings.NewReader(url.Values{"csrf": {csrfVal}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for name, r := range map[string]*http.Response{
		"no csrf":        post(c, e.srv.URL, ""),
		"wrong csrf":     post(c, e.srv.URL, csrf+"x"),
		"foreign origin": post(c, "https://evil.example", csrf),
		"no origin":      post(c, "", csrf),
		"no session":     post(e.client(), e.srv.URL, csrf),
	} {
		body(t, r)
		if r.StatusCode != 403 && r.StatusCode != 401 {
			t.Errorf("%s: status %d, want 401/403", name, r.StatusCode)
		}
	}
	// still signed in after the refusals, then a valid request signs out
	if r, _ := c.Get(e.url("/device")); r.StatusCode != 200 {
		t.Fatalf("the refusals must not end the session: %d", r.StatusCode)
	}
	if r := post(c, e.srv.URL, csrf); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid logout: %d", r.StatusCode)
	}
	if r, _ := c.Get(e.url("/device")); r.StatusCode != 302 {
		t.Fatalf("after logout /device must redirect to sign-in: %d", r.StatusCode)
	}
}

func deviceStart(t *testing.T, e *env) (deviceCode, userCode string) {
	t.Helper()
	resp, err := http.PostForm(e.url("/v1/device/code"), url.Values{"client_id": {"rigfile-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &out); err != nil || out.DeviceCode == "" || out.URI != e.srv.URL+"/device" || out.Interval != 5 || out.ExpiresIn != 900 {
		t.Fatalf("%+v %v", out, err)
	}
	return out.DeviceCode, out.UserCode
}

func devicePoll(t *testing.T, e *env, dc string) (int, map[string]any) {
	resp, err := http.PostForm(e.url("/v1/device/token"), url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {dc}, "client_id": {"rigfile-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(body(t, resp)), &out)
	return resp.StatusCode, out
}

func TestDeviceFlowOverHTTP(t *testing.T) {
	e := newEnv(t, nil)
	if resp, _ := http.PostForm(e.url("/v1/device/code"), url.Values{"client_id": {"other"}}); resp.StatusCode != 400 {
		t.Fatalf("an unknown client id: %d", resp.StatusCode)
	}
	dc, uc := deviceStart(t, e)
	if code, out := devicePoll(t, e, dc); code != 400 || out["error"] != "authorization_pending" {
		t.Fatalf("%d %v", code, out)
	}
	if code, out := devicePoll(t, e, dc); code != 400 || out["error"] != "slow_down" {
		t.Fatalf("polling too fast: %d %v", code, out)
	}
	// the person opens /device: not signed in, so they are sent to sign in and come back to the same code
	anon := e.client()
	resp, _ := anon.Get(e.url("/device?user_code=" + url.QueryEscape(uc)))
	resp.Body.Close()
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), "/login?next=") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	c := e.signIn("/device?user_code=" + uc)
	resp, _ = c.Get(e.url("/device?user_code=" + url.QueryEscape(uc)))
	page := body(t, resp)
	if !strings.Contains(page, uc) || !strings.Contains(page, "Approve") {
		t.Fatalf("confirm page:\n%s", page)
	}
	// a wrong code is a 404 page, not an oracle for near misses
	resp, _ = c.Get(e.url("/device?user_code=BCDF-GHJK"))
	if resp.StatusCode != 404 {
		t.Fatalf("%d", resp.StatusCode)
	}
	resp.Body.Close()
	// approve (with CSRF and origin)
	req, _ := http.NewRequest("POST", e.url("/device"), strings.NewReader(url.Values{"csrf": {csrfFrom(t, page)}, "user_code": {uc}, "decision": {"approve"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e.srv.URL)
	resp, _ = c.Do(req)
	if got := body(t, resp); resp.StatusCode != 200 || !strings.Contains(got, "CLI signed in") {
		t.Fatalf("%d %s", resp.StatusCode, got)
	}
	e.clk.add(16 * time.Second) // the interval grew to 10s after the slow_down
	code, out := devicePoll(t, e, dc)
	tok, _ := out["access_token"].(string)
	if code != 200 || !strings.HasPrefix(tok, "rgf_") || out["token_type"] != "Bearer" {
		t.Fatalf("%d %v", code, out)
	}
	// the token works, and is revoked by logout
	me := func() int {
		req, _ := http.NewRequest("GET", e.url("/v1/me"), nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode == 200 {
			var m map[string]any
			_ = json.Unmarshal([]byte(body(t, r)), &m)
			if m["login"] != "jia" {
				t.Fatalf("%v", m)
			}
		} else {
			body(t, r)
		}
		return r.StatusCode
	}
	if me() != 200 {
		t.Fatal("the issued token must work")
	}
	req, _ = http.NewRequest("DELETE", e.url("/v1/tokens/current"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 204 {
		t.Fatalf("revoke: %d", r.StatusCode)
	}
	if me() != 401 {
		t.Fatal("a revoked token must be refused")
	}
	// the code cannot be used twice
	e.clk.add(16 * time.Second)
	if code, out := devicePoll(t, e, dc); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("reuse: %d %v", code, out)
	}
	if resp, _ := http.Get(e.url("/v1/me")); resp.StatusCode != 401 {
		t.Fatal("no token, no access")
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/healthz", "/login", "/device", "/static/style.css", "/nope"} {
		resp, err := e.client().Get(e.url(p))
		if err != nil {
			t.Fatal(err)
		}
		body(t, resp)
		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "script-src") ||
			!strings.Contains(csp, "frame-ancestors 'none'") || resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: headers %v", p, resp.Header)
		}
	}
	// over https: HSTS and __Host- cookies
	e2 := newEnv(t, nil)
	e2.s.Cfg.PublicURL = "https://registry.example.test"
	rec := httptest.NewRecorder()
	e2.s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS over https")
	}
	var found bool
	for _, ck := range rec.Result().Cookies() {
		if strings.HasPrefix(ck.Name, "__Host-") && ck.Secure && ck.HttpOnly && ck.Path == "/" && ck.Domain == "" {
			found = true
		}
	}
	if !found {
		t.Errorf("cookies: %v", rec.Result().Cookies())
	}
}

func TestRateLimits(t *testing.T) {
	e := newEnv(t, nil)
	limited := 0
	for i := 0; i < 12; i++ {
		resp, _ := http.PostForm(e.url("/v1/device/code"), url.Values{"client_id": {"rigfile-cli"}})
		body(t, resp)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
			if resp.Header.Get("Retry-After") == "" {
				t.Error("Retry-After")
			}
		}
	}
	if limited == 0 {
		t.Fatal("starting device sign-ins must be rate limited")
	}
	l := registry.NewLimiter(e.clk.now)
	for i := 0; i < 3; i++ {
		if !l.Allow("k", 60, 3) {
			t.Fatal("burst")
		}
	}
	if l.Allow("k", 60, 3) {
		t.Fatal("over budget")
	}
	e.clk.add(2 * time.Second)
	if !l.Allow("k", 60, 3) || !l.Allow("k", 60, 3) || l.Allow("k", 60, 3) {
		t.Fatal("refill at 1 per second")
	}
	if !l.Allow("other", 60, 3) {
		t.Fatal("keys are independent")
	}
}

func TestConfigValidation(t *testing.T) {
	env := map[string]string{"RIGFILE_REGISTRY_PUBLIC_URL": "https://registry.example.test/", "RIGFILE_REGISTRY_DATABASE_URL": "postgres://x", "RIGFILE_REGISTRY_BLOB": "fs:/tmp/b",
		"RIGFILE_REGISTRY_GITHUB_CLIENT_ID": "id", "RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET": "sec", "RIGFILE_REGISTRY_ADMINS": "Jia, other ,"}
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c, err := registry.ConfigFromEnv(get(env), nil)
	if err != nil || c.PublicURL != "https://registry.example.test" || len(c.Admins) != 2 || !c.IsAdmin("JIA") || c.IsAdmin("nobody") || !c.Secure() {
		t.Fatalf("%+v %v", c, err)
	}
	for k, v := range map[string]string{"RIGFILE_REGISTRY_PUBLIC_URL": "http://registry.example.test", "RIGFILE_REGISTRY_DATABASE_URL": "", "RIGFILE_REGISTRY_BLOB": "",
		"RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET": "", "RIGFILE_REGISTRY_MAX_UPLOAD_MB": "0"} {
		m := map[string]string{}
		for a, b := range env {
			m[a] = b
		}
		m[k] = v
		if _, err := registry.ConfigFromEnv(get(m), nil); err == nil {
			t.Errorf("%s=%q should be refused", k, v)
		}
	}
	m := map[string]string{}
	for a, b := range env {
		m[a] = b
	}
	m["RIGFILE_REGISTRY_PUBLIC_URL"] = "http://localhost:8080"
	if _, err := registry.ConfigFromEnv(get(m), nil); err != nil {
		t.Fatalf("plain http is fine for localhost: %v", err)
	}
	_ = fmt.Sprint
}
