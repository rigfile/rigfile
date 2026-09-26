package rigd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

type brokerRig struct {
	b    *Broker
	c    *Client
	dir  string
	real string
	clk  *fakeClock
	up   *httptest.Server
	mu   sync.Mutex
	auth []string
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClock) add(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

func newBrokerRig(t *testing.T) *brokerRig {
	t.Helper()
	r := &brokerRig{dir: filepath.Join(t.TempDir(), "rigd"), real: fakeReal(), clk: &fakeClock{t: time.Now()}}
	r.up = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.mu.Lock()
		r.auth = append(r.auth, q.Header.Get("Authorization"))
		r.mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(r.up.Close)
	upAddr := r.up.Listener.Addr().String()
	r.b = &Broker{
		Dir: r.dir, Version: "test", Now: r.clk.now,
		Resolve: func(ref string) ([]byte, error) {
			if ref != "svc/key" {
				return nil, errors.New("not set")
			}
			return []byte(r.real), nil
		},
		Dial:        func(_ context.Context, network, _ string) (net.Conn, error) { return net.Dial(network, upAddr) },
		UpstreamTLS: &tls.Config{InsecureSkipVerify: true},
	}
	if err := r.b.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.b.Close() })
	c, err := ClientFromDir(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	r.c = c
	return r
}

var spec = SessionSpec{
	Server:  "srv",
	Secrets: []SecretSpec{{Env: "API_KEY", Ref: "svc/key", Hosts: []string{"api.example.test"}}},
	Allow:   []string{"api.example.test", "attacker.example.test"},
}

func TestBrokerSessionLifecycleAndNoRealValuesInReplies(t *testing.T) {
	r := newBrokerRig(t)
	st, err := r.c.Status()
	if err != nil || st.Sessions != 0 || st.PID != os.Getpid() {
		t.Fatalf("%+v %v", st, err)
	}
	rep, err := r.c.Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rep.Surrogates["API_KEY"], SurrogatePrefix) || !strings.Contains(rep.CAPEM, "BEGIN CERTIFICATE") || strings.Contains(rep.CAPEM, "PRIVATE") {
		t.Fatalf("%+v", rep)
	}
	if strings.Contains(fmt.Sprintf("%+v", rep), r.real) {
		t.Fatal("a reply carried the real value")
	}
	// the whole path works: the child uses the proxy URL and the CA from the reply
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(rep.CAPEM)) {
		t.Fatal("bad CA PEM")
	}
	pu, _ := url.Parse(rep.ProxyURL)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: pool}, DisableKeepAlives: true}}
	req, _ := http.NewRequest("GET", "https://api.example.test/", nil)
	req.Header.Set("Authorization", "Bearer "+rep.Surrogates["API_KEY"])
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if len(r.auth) != 1 || r.auth[0] != "Bearer "+r.real {
		t.Fatalf("upstream saw %q", r.auth)
	}
	// ending the session ends its credentials
	if st, _ := r.c.Status(); st.Sessions != 1 {
		t.Fatalf("%+v", st)
	}
	if err := r.c.Close(rep.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := hc.Get("https://api.example.test/"); err == nil {
		t.Fatal("the proxy credentials must stop working when the session ends")
	}
	if st, _ := r.c.Status(); st.Sessions != 0 {
		t.Fatalf("%+v", st)
	}
	// the audit log holds no secrets
	raw, _ := os.ReadFile(filepath.Join(r.dir, AuditFile))
	if len(raw) == 0 || strings.Contains(string(raw), r.real) || strings.Contains(string(raw), rep.Surrogates["API_KEY"]) {
		t.Fatalf("audit: %s", raw)
	}
}

func TestBrokerRefusesBadSpecsWithoutLeaking(t *testing.T) {
	r := newBrokerRig(t)
	for name, s := range map[string]SessionSpec{
		"no allowlist": {Server: "srv", Secrets: spec.Secrets},
		"no hosts":     {Server: "srv", Secrets: []SecretSpec{{Env: "K", Ref: "svc/key"}}, Allow: []string{"a.example.test"}},
		"missing":      {Server: "srv", Secrets: []SecretSpec{{Env: "K", Ref: "svc/none", Hosts: []string{"a.example.test"}}}, Allow: []string{"a.example.test"}},
		"tld wildcard": {Server: "srv", Allow: []string{"*.com"}},
	} {
		_, err := r.c.Open(s)
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != 422 || strings.Contains(ae.Message, r.real) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if st, _ := r.c.Status(); st.Sessions != 0 {
		t.Fatal("a refused spec must leave no session behind")
	}
}

func TestBrokerAuthenticatesAndRefusesBrowsers(t *testing.T) {
	r := newBrokerRig(t)
	get := func(host, auth, origin string) int {
		req, _ := http.NewRequest("GET", "http://"+r.c.API+"/v1/status", nil)
		if host != "" {
			req.Host = host
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	good := "Bearer " + r.c.Token
	for name, c := range map[string]struct {
		host, auth, origin string
		want               int
	}{
		"ok":            {"", good, "", 200},
		"no token":      {"", "", "", 401},
		"wrong token":   {"", "Bearer nope", "", 401},
		"rebinding":     {"attacker.example.test:80", good, "", 403},
		"browser":       {"", good, "http://attacker.example.test", 403},
		"basic instead": {"", "Basic " + r.c.Token, "", 401},
	} {
		if got := get(c.host, c.auth, c.origin); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	// no route hands out the CA key or a secret
	for _, p := range []string{"/", "/v1/ca", "/v1/secrets/svc/key", "/v1/sessions"} {
		req, _ := http.NewRequest("GET", "http://"+r.c.API+p, nil)
		req.Header.Set("Authorization", good)
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode == 200 {
			t.Errorf("GET %s answered 200", p)
		}
		resp.Body.Close()
	}
}

func TestBrokerSessionsExpireAndFilesAreCleanedUp(t *testing.T) {
	r := newBrokerRig(t)
	rep, err := r.c.Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	r.clk.add(SessionTTL + time.Minute)
	if st, _ := r.c.Status(); st.Sessions != 0 {
		t.Fatalf("an expired session must be gone: %+v", st)
	}
	pu, _ := url.Parse(rep.ProxyURL)
	if _, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}).Get("https://api.example.test/"); err == nil {
		t.Fatal("expired credentials must not work")
	}
	// the token file is private, and a second broker refuses to start
	if err := platform.IsPrivateFile(filepath.Join(r.dir, TokenFile)); err != nil {
		t.Fatal(err)
	}
	other := &Broker{Dir: r.dir, Resolve: r.b.Resolve}
	if err := other.Start(); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("%v", err)
	}
	r.b.Close()
	for _, f := range []string{TokenFile, InfoFile} {
		if _, err := os.Stat(filepath.Join(r.dir, f)); err == nil {
			t.Errorf("%s must be removed on close", f)
		}
	}
	if _, err := ClientFromDir(r.dir); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("%v", err)
	}
}
