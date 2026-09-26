package login

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/secrets"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMem() *memStore { return &memStore{m: map[string]string{}} }
func (s *memStore) Set(ref string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[ref] = string(v)
	return nil
}
func (s *memStore) Get(ref string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[ref]; ok {
		return []byte(v), nil
	}
	return nil, secrets.ErrNotFound
}
func (s *memStore) Delete(ref string) error { delete(s.m, ref); return nil }
func (s *memStore) Kind() string            { return "memory" }

const fakeAccess = "fake-access-token-0123456789"

// fakeProvider is a token endpoint that checks PKCE, plus a scripted device endpoint.
type fakeProvider struct {
	srv       *httptest.Server
	challenge string
	code      string
	pending   int // device flow: "authorization_pending" answers before success
	slow      bool
	deny      bool
	expire    bool
}

func newProvider(t *testing.T) *fakeProvider {
	p := &fakeProvider{code: "the-auth-code"}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != p.code || base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "secret detail " + fakeAccess})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fakeAccess, "refresh_token": "refresh-xyz", "expires_in": 3600})
		case "urn:ietf:params:oauth:grant-type:device_code":
			switch {
			case p.deny:
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
			case p.expire:
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
			case p.slow:
				p.slow = false
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			case p.pending > 0:
				p.pending--
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fakeAccess})
			}
		default:
			w.WriteHeader(400)
		}
	})
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "dev-code", "user_code": "ABCD-EFGH", "verification_uri": p.srv.URL + "/verify", "interval": 1})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeProvider) oauth() OAuth {
	return OAuth{AuthURL: p.srv.URL + "/authorize", DeviceURL: p.srv.URL + "/device", TokenURL: p.srv.URL + "/token", ClientID: "public-client", Scopes: []string{"read", "write"}}
}

// browser plays the user: it follows the authorization URL straight to the redirect with the given extra behaviour.
func (p *fakeProvider) browser(t *testing.T, before func(redirect, state string)) func(string) error {
	return func(authURL string) error {
		u, _ := url.Parse(authURL)
		q := u.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "public-client" || q.Get("response_type") != "code" || q.Get("scope") != "read write" {
			t.Errorf("bad authorization request: %v", q)
		}
		if !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") || len(q.Get("state")) < 30 {
			t.Errorf("redirect must be loopback with a random state: %v", q)
		}
		p.challenge = q.Get("code_challenge")
		go func() {
			if before != nil {
				before(q.Get("redirect_uri"), q.Get("state"))
			}
			resp, err := http.Get(q.Get("redirect_uri") + "?code=" + p.code + "&state=" + q.Get("state"))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLoopbackPKCEGivesATokenAndIgnoresWrongCallbacks(t *testing.T) {
	p := newProvider(t)
	var shown bytes.Buffer
	c := &Client{Show: func(f string, a ...any) { fmt.Fprintf(&shown, f, a...) }}
	c.Open = p.browser(t, func(redirect, state string) {
		// a forged callback with the wrong state must be ignored, and must not stop the real one
		if resp, err := http.Get(redirect + "?code=evil&state=wrong"); err == nil {
			if resp.StatusCode != 400 {
				t.Errorf("forged callback got %d", resp.StatusCode)
			}
			resp.Body.Close()
		}
	})
	tok, err := c.LoopbackPKCE(context.Background(), p.oauth())
	if err != nil || tok.Access != fakeAccess || tok.Refresh != "refresh-xyz" {
		t.Fatalf("%+v %v", tok, err)
	}
	if strings.Contains(shown.String(), fakeAccess) {
		t.Fatal("the token was shown")
	}
}

func TestLoopbackPKCEErrorsAndTimeout(t *testing.T) {
	p := newProvider(t)
	// the provider answers with an error
	c := &Client{Open: func(authURL string) error {
		u, _ := url.Parse(authURL)
		go func() {
			r, err := http.Get(u.Query().Get("redirect_uri") + "?error=access_denied&state=" + u.Query().Get("state"))
			if err == nil {
				r.Body.Close()
			}
		}()
		return nil
	}}
	if _, err := c.LoopbackPKCE(context.Background(), p.oauth()); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("%v", err)
	}
	// nobody ever comes back
	c = &Client{Timeout: 200 * time.Millisecond, Open: func(string) error { return errors.New("no browser") }, Show: func(string, ...any) {}}
	if _, err := c.LoopbackPKCE(context.Background(), p.oauth()); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("%v", err)
	}
	// a token endpoint that refuses the code: only the error code surfaces, never the body
	c = &Client{Open: func(authURL string) error {
		u, _ := url.Parse(authURL)
		p.challenge = u.Query().Get("code_challenge")
		go func() {
			r, err := http.Get(u.Query().Get("redirect_uri") + "?code=wrong-code&state=" + u.Query().Get("state"))
			if err == nil {
				r.Body.Close()
			}
		}()
		return nil
	}}
	_, err := c.LoopbackPKCE(context.Background(), p.oauth())
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), fakeAccess) || strings.Contains(err.Error(), "secret detail") {
		t.Fatalf("%v", err)
	}
	// insecure endpoints are refused
	bad := p.oauth()
	bad.TokenURL = "http://example.com/token"
	if _, err := c.LoopbackPKCE(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("%v", err)
	}
}

func TestDeviceFlowPollsHonoursSlowDownAndStopsOnDenial(t *testing.T) {
	p := newProvider(t)
	p.pending, p.slow = 2, true
	var sleeps []time.Duration
	var shown bytes.Buffer
	c := &Client{Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }, Show: func(f string, a ...any) { fmt.Fprintf(&shown, f, a...) }}
	tok, err := c.DeviceFlow(context.Background(), p.oauth())
	if err != nil || tok.Access != fakeAccess {
		t.Fatalf("%+v %v", tok, err)
	}
	if !strings.Contains(shown.String(), "ABCD-EFGH") || strings.Contains(shown.String(), "dev-code") {
		t.Fatalf("the user code is shown, the device code is not: %q", shown.String())
	}
	if len(sleeps) != 4 || sleeps[0] != time.Second || sleeps[2] != 6*time.Second {
		t.Fatalf("polling intervals: %v", sleeps)
	}
	p.deny = true
	if _, err := c.DeviceFlow(context.Background(), p.oauth()); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("%v", err)
	}
	p.deny, p.expire = false, true
	if _, err := c.DeviceFlow(context.Background(), p.oauth()); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("%v", err)
	}
}

func env(t *testing.T, out *bytes.Buffer) (Env, *memStore, *[][]string) {
	st := newMem()
	var ran [][]string
	pi, _ := platform.New(platform.Options{GOOS: "linux", Getenv: func(k string) string { return map[string]string{"HOME": "/h", "DISPLAY": ":0"}[k] }})
	return Env{Out: out, Store: st, Plat: pi, Getenv: func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] },
		Hidden:   func(string) (string, error) { return "fake-api-key-value", nil },
		Line:     func(string) (string, error) { return "y", nil },
		LookPath: func(string) (string, error) { return "/bin/x", nil },
		RunCmd:   func(_ context.Context, argv []string) error { ran = append(ran, argv); return nil },
	}, st, &ran
}

func TestAPIKeyLoginStoresInTheSecretStoreOnlyAndSkipsWhenPresent(t *testing.T) {
	var out bytes.Buffer
	e, st, _ := env(t, &out)
	res := RunAll(context.Background(), e, []Request{{Provider: "acme", Reason: "for the acme MCP"}})
	if !res[0].OK || res[0].Method != APIKey {
		t.Fatalf("%+v", res)
	}
	if v, _ := st.Get("logins/acme/api_key"); string(v) != "fake-api-key-value" {
		t.Fatal("not stored")
	}
	if strings.Contains(out.String(), "fake-api-key-value") {
		t.Fatal("the key was printed")
	}
	asked := 0
	e.Hidden = func(string) (string, error) { asked++; return "x", nil }
	if res = RunAll(context.Background(), e, []Request{{Provider: "acme"}}); !res[0].OK || asked != 0 {
		t.Fatalf("an existing key must not be asked for again: %+v", res)
	}
	e2, _, _ := env(t, &out)
	e2.Hidden = func(string) (string, error) { return "  ", nil }
	if res = RunAll(context.Background(), e2, []Request{{Provider: "acme"}}); res[0].OK {
		t.Fatal("an empty key is a skip")
	}
}

func TestVendorCLILoginRunsTheVendorsOwnCommandAndChecksIt(t *testing.T) {
	var out bytes.Buffer
	e, _, ran := env(t, &out)
	res := RunAll(context.Background(), e, []Request{{Provider: "github"}, {Provider: "codex"}, {Provider: "claude-code"}})
	for _, r := range res {
		if !r.OK {
			t.Fatalf("%+v", res)
		}
	}
	want := "gh auth login|gh auth status|codex login"
	var got []string
	for _, a := range *ran {
		got = append(got, strings.Join(a, " "))
	}
	if strings.Join(got, "|") != want {
		t.Fatalf("ran %v", got)
	}
	// a failing check means the login did not take
	e.RunCmd = func(_ context.Context, argv []string) error {
		if argv[1] == "auth" && argv[2] == "status" {
			return errors.New("exit 1")
		}
		return nil
	}
	if res = RunAll(context.Background(), e, []Request{{Provider: "github"}}); res[0].OK {
		t.Fatal("a failed status check must fail the login")
	}
	// missing tool, and a manual login the user says is not done
	e.LookPath = func(string) (string, error) { return "", errors.New("nope") }
	if res = RunAll(context.Background(), e, []Request{{Provider: "codex"}}); res[0].OK || !strings.Contains(res[0].Note, "not installed") {
		t.Fatalf("%+v", res)
	}
	e.Line = func(string) (string, error) { return "n", nil }
	if res = RunAll(context.Background(), e, []Request{{Provider: "claude-code"}}); res[0].OK {
		t.Fatal("manual login not confirmed")
	}
}

func TestOAuthLoginChoosesDeviceFlowWhenHeadlessAndStoresTokensOnlyInTheStore(t *testing.T) {
	p := newProvider(t)
	oa := p.oauth()
	Providers["fakeoauth"] = Provider{Default: OAuthWeb, OAuth: &oa}
	defer delete(Providers, "fakeoauth")
	var out bytes.Buffer
	e, st, _ := env(t, &out)
	e.Client = &Client{Sleep: func(time.Duration) {}, Show: func(f string, a ...any) { fmt.Fprintf(&out, f, a...) }}
	e.Getenv = func(k string) string { return map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}[k] }
	res := RunAll(context.Background(), e, []Request{{Provider: "fakeoauth"}})
	if !res[0].OK || !strings.Contains(out.String(), "device-code") {
		t.Fatalf("%+v\n%s", res, out.String())
	}
	if v, _ := st.Get("logins/fakeoauth/access_token"); string(v) != fakeAccess {
		t.Fatal("token not stored")
	}
	if strings.Contains(out.String(), fakeAccess) {
		t.Fatal("token printed")
	}
	// no client registered for a provider
	if res = RunAll(context.Background(), e, []Request{{Provider: "nobody", Method: OAuthWeb}}); res[0].OK || !strings.Contains(res[0].Note, "no OAuth client") {
		t.Fatalf("%+v", res)
	}
}

func TestHeadless(t *testing.T) {
	linux, _ := platform.New(platform.Options{GOOS: "linux", Getenv: func(string) string { return "" }})
	mac, _ := platform.New(platform.Options{GOOS: "darwin", Getenv: func(string) string { return "" }})
	none := func(string) string { return "" }
	if !Headless(linux, none) || Headless(mac, none) {
		t.Fatal("a Linux session without a display is headless; a Mac is not")
	}
	if !Headless(mac, func(k string) string { return map[string]string{"SSH_TTY": "/dev/pts/0"}[k] }) {
		t.Fatal("ssh is headless")
	}
	if Headless(linux, func(k string) string { return map[string]string{"WAYLAND_DISPLAY": "wayland-0"}[k] }) {
		t.Fatal("a Wayland session has a browser")
	}
}
