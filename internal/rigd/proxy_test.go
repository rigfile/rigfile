package rigd

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeReal builds an obviously fake "real" secret at run time.
func fakeReal() string { return "REAL" + "-" + "value" + "-" + "9f3a1c77d2b4" }

type seen struct {
	Host, Path, Query, Auth, Body, Custom string
}

type rig struct {
	t        *testing.T
	proxy    *httptest.Server
	audit    *MemAudit
	sess     *Session
	store    *SessionStore
	real     string
	sur      string
	upstream *httptest.Server
	mu       sync.Mutex
	got      []seen
	handler  http.HandlerFunc // optional per-test upstream behaviour
	ca       *CA
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, audit: &MemAudit{}, real: fakeReal()}
	r.upstream = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		b, _ := io.ReadAll(q.Body)
		r.mu.Lock()
		r.got = append(r.got, seen{q.Host, q.URL.Path, q.URL.RawQuery, q.Header.Get("Authorization"), string(b), q.Header.Get("X-Custom")})
		r.mu.Unlock()
		if r.handler != nil {
			q.Body = io.NopCloser(strings.NewReader(string(b)))
			r.handler(w, q)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(r.upstream.Close)
	ca, err := NewCA(nil)
	if err != nil {
		t.Fatal(err)
	}
	r.ca = ca
	r.store = NewSessionStore(nil)
	upAddr := r.upstream.Listener.Addr().String()
	p := &Proxy{
		CA: ca, Sessions: r.store, Audit: r.audit, MaxBody: 1 << 10,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upAddr)
		},
		UpstreamTLS: &tls.Config{InsecureSkipVerify: true},
	}
	r.proxy = httptest.NewServer(p.Handler())
	t.Cleanup(r.proxy.Close)
	r.sess, err = r.store.New(SessionSpec{
		Server:  "srv",
		Secrets: []SecretSpec{{Env: "API_KEY", Ref: "svc/key", Hosts: []string{"api.example.test"}}},
		Allow:   []string{"api.example.test", "other.example.test"},
	}, func(string) ([]byte, error) { return []byte(r.real), nil })
	if err != nil {
		t.Fatal(err)
	}
	r.sur = r.sess.Surrogates(SessionSpec{Secrets: []SecretSpec{{Env: "API_KEY"}}})["API_KEY"]
	return r
}

func (r *rig) client(u string) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(r.ca.Certificate())
	pu, _ := url.Parse(u)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: pool}, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (r *rig) sessionClient() *http.Client {
	return r.client(r.sess.ProxyURL(r.proxy.Listener.Addr().String()))
}

func (r *rig) lastSeen() seen {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) == 0 {
		r.t.Fatal("the upstream received nothing")
	}
	return r.got[len(r.got)-1]
}

func (r *rig) upstreamCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *rig) do(method, u string, hdr map[string]string, body string) (*http.Response, string, error) {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, u, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := r.sessionClient().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b), nil
}

func TestProxySwapsOnlyForTheBoundHost(t *testing.T) {
	r := newRig(t)
	resp, _, err := r.do("POST", "https://api.example.test/v1/"+r.sur+"/x?key="+r.sur, map[string]string{"Authorization": "Bearer " + r.sur, "X-Custom": "a-" + r.sur + "-b", "Content-Type": "application/json"}, `{"k":"`+r.sur+`"}`)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	g := r.lastSeen()
	if g.Auth != "Bearer "+r.real || g.Custom != "a-"+r.real+"-b" || g.Path != "/v1/"+r.real+"/x" || g.Query != "key="+r.real || g.Body != `{"k":"`+r.real+`"}` {
		t.Fatalf("the bound host must receive the real value everywhere: %+v", g)
	}
	if g.Host != "api.example.test" {
		t.Fatalf("Host = %q", g.Host)
	}
	// a request that carries no surrogate is forwarded untouched and is not called a swap
	before := len(r.audit.Snapshot())
	if _, _, err := r.do("GET", "https://api.example.test/plain", nil, ""); err != nil {
		t.Fatal(err)
	}
	ev := r.audit.Snapshot()
	if len(ev) != before+1 || ev[before].Decision != "allowed" || len(ev[before].Secrets) != 0 {
		t.Fatalf("%+v", ev)
	}
	if ev[0].Decision != "swapped" || len(ev[0].Secrets) != 1 || ev[0].Secrets[0] != "svc/key" || strings.Contains(ev[0].Path, r.sur) {
		t.Fatalf("%+v", ev[0])
	}
}

func TestProxyBlocksSurrogateToWrongHost(t *testing.T) {
	r := newRig(t)
	// other.example.test is allowed for the server, but this secret is not bound to it: the attacker is inside the allowlist
	for name, c := range map[string]struct {
		url  string
		hdr  map[string]string
		body string
	}{
		"header": {"https://other.example.test/", map[string]string{"Authorization": "Bearer " + r.sur}, ""},
		"query":  {"https://other.example.test/?k=" + r.sur, nil, ""},
		"path":   {"https://other.example.test/" + r.sur, nil, ""},
		"body":   {"https://other.example.test/", map[string]string{"Content-Type": "application/json"}, `{"stolen":"` + r.sur + `"}`},
	} {
		method := "GET"
		if c.body != "" {
			method = "POST"
		}
		resp, _, err := r.do(method, c.url, c.hdr, c.body)
		if err != nil || resp.StatusCode != 403 {
			t.Errorf("%s: %v %v", name, resp, err)
		}
	}
	if r.upstreamCalls() != 0 {
		t.Fatal("a blocked request must never reach the upstream")
	}
	for _, e := range r.audit.Snapshot() {
		if e.Decision != "blocked" || !strings.Contains(e.Reason, "svc/key") {
			t.Errorf("%+v", e)
		}
	}
}

func TestProxyBlocksDisallowedHosts(t *testing.T) {
	r := newRig(t)
	_, _, err := r.do("GET", "https://evil.example.test/", nil, "")
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("CONNECT to a host outside network.allow must be refused: %v", err)
	}
	// port matters: the pattern is for :443
	if _, _, err := r.do("GET", "https://api.example.test:8443/", nil, ""); err == nil {
		t.Fatal("another port is another endpoint")
	}
	if r.upstreamCalls() != 0 {
		t.Fatal("nothing may reach the upstream")
	}
	ev := r.audit.Snapshot()
	if len(ev) != 2 || ev[0].Decision != "blocked" || ev[0].Host != "evil.example.test:443" {
		t.Fatalf("%+v", ev)
	}
	// plain HTTP through the proxy is refused: it would carry secrets in the clear
	req, _ := http.NewRequest("GET", "http://api.example.test/", nil)
	pu, _ := url.Parse(r.sess.ProxyURL(r.proxy.Listener.Addr().String()))
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}).Do(req)
	if err != nil || resp.StatusCode != 405 {
		t.Fatalf("%v %v", resp, err)
	}
}

func TestProxyScrubsEchoedSecrets(t *testing.T) {
	r := newRig(t)
	r.handler = func(w http.ResponseWriter, q *http.Request) {
		b, _ := io.ReadAll(q.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo", q.Header.Get("Authorization"))
		w.Header().Set("Set-Cookie", "s="+r.real)
		fmt.Fprintf(w, `{"you_sent":%q,"body":%q}`, q.Header.Get("Authorization"), string(b))
	}
	resp, body, err := r.do("POST", "https://api.example.test/echo", map[string]string{"Authorization": r.sur, "Content-Type": "text/plain"}, "x"+r.sur)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, r.real) || strings.Contains(fmt.Sprint(resp.Header), r.real) {
		t.Fatalf("the real value came back to the child: %s %v", body, resp.Header)
	}
	if !strings.Contains(body, r.sur) || resp.Header.Get("X-Echo") != r.sur {
		t.Fatalf("the echo should carry the surrogate: %s %v", body, resp.Header)
	}
}

func TestProxyBodies(t *testing.T) {
	r := newRig(t)
	// form bodies are swapped, and the length is recomputed
	if _, _, err := r.do("POST", "https://api.example.test/f", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, "k="+r.sur+"&n=1"); err != nil {
		t.Fatal(err)
	}
	if got := r.lastSeen().Body; got != "k="+r.real+"&n=1" {
		t.Fatalf("form: %q", got)
	}
	// binary bodies pass unchanged: the surrogate stays a surrogate and fails closed at the API
	if _, _, err := r.do("POST", "https://api.example.test/b", map[string]string{"Content-Type": "application/octet-stream"}, "bin"+r.sur); err != nil {
		t.Fatal(err)
	}
	if got := r.lastSeen().Body; got != "bin"+r.sur {
		t.Fatalf("binary: %q", got)
	}
	// text bodies above MaxBody are not scanned, so nothing is swapped in them
	big := strings.Repeat("a", 2<<10) + r.sur
	if _, _, err := r.do("POST", "https://api.example.test/big", map[string]string{"Content-Type": "text/plain"}, big); err != nil {
		t.Fatal(err)
	}
	if got := r.lastSeen().Body; got != big {
		t.Fatalf("big body must pass through byte for byte (len %d vs %d)", len(got), len(big))
	}
	// a big body is never sent to an unbound host with the real value either
	if strings.Contains(r.lastSeen().Body, r.real) {
		t.Fatal("real value leaked")
	}
}

func TestProxyAuth(t *testing.T) {
	r := newRig(t)
	addr := r.proxy.Listener.Addr().String()
	for name, u := range map[string]string{
		"none":      "http://" + addr,
		"wrong pw":  "http://" + r.sess.User + ":nope@" + addr,
		"wrong usr": "http://s-unknown:" + r.sess.Password + "@" + addr,
	} {
		_, err := r.client(u).Get("https://api.example.test/")
		if err == nil || !strings.Contains(err.Error(), "Proxy Authentication Required") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// after the session ends the credentials stop working
	r.store.Close(r.sess.ID)
	if _, err := r.sessionClient().Get("https://api.example.test/"); err == nil || !strings.Contains(err.Error(), "Proxy Authentication Required") {
		t.Fatalf("ended session: %v", err)
	}
	if r.upstreamCalls() != 0 {
		t.Fatal("nothing may reach the upstream")
	}
}

// rawTunnel opens a CONNECT tunnel and returns a TLS connection to the intercepting side.
func (r *rig) rawTunnel(target, serverName string) *tls.Conn {
	r.t.Helper()
	c, err := net.Dial("tcp", r.proxy.Listener.Addr().String())
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	auth := basicHeader(r.sess.User, r.sess.Password)
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", target, target, auth)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 200 {
		r.t.Fatalf("CONNECT: %v %v", resp, err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(r.ca.Certificate())
	tc := tls.Client(c, &tls.Config{RootCAs: pool, ServerName: serverName})
	if err := tc.Handshake(); err != nil {
		r.t.Fatal(err)
	}
	return tc
}

func basicHeader(u, p string) string {
	req, _ := http.NewRequest("GET", "http://x", nil)
	req.SetBasicAuth(u, p)
	return req.Header.Get("Authorization")
}

func TestProxyRefusesFrontingAndUpgrades(t *testing.T) {
	r := newRig(t)
	// domain fronting: the tunnel is to other.example.test, the Host header names the bound host
	tc := r.rawTunnel("other.example.test:443", "other.example.test")
	fmt.Fprintf(tc, "GET / HTTP/1.1\r\nHost: api.example.test\r\nAuthorization: %s\r\n\r\n", r.sur)
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil || resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("%v %v", resp, err)
	}
	tc = r.rawTunnel("api.example.test:443", "api.example.test")
	fmt.Fprint(tc, "GET /ws HTTP/1.1\r\nHost: api.example.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	resp, err = http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil || resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("%v %v", resp, err)
	}
	if r.upstreamCalls() != 0 {
		t.Fatal("nothing may reach the upstream")
	}
}

func TestProxyRefusesNonLoopbackPeers(t *testing.T) {
	p := &Proxy{CA: mustCA(t), Sessions: NewSessionStore(nil)}
	req := httptest.NewRequest("CONNECT", "example.test:443", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("%d", rec.Code)
	}
}

func mustCA(t *testing.T) *CA {
	t.Helper()
	ca, err := NewCA(nil)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func TestDefaultDialRefusesInternalAddressesForNames(t *testing.T) {
	// "localhost" is a name that resolves to loopback: an allowlisted name must not reach the user's own machine
	if c, err := safeDial(context.Background(), "tcp", "localhost:9"); err == nil {
		c.Close()
		t.Fatal("a name that resolves to loopback must be refused")
	}
	// an IP literal is an explicit choice: the dial is attempted (and fails only because nothing listens)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	c, err := safeDial(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("an IP literal in the allowlist is allowed: %v", err)
	}
	c.Close()
}

func TestAuditNeverContainsSecrets(t *testing.T) {
	r := newRig(t)
	r.handler = func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, r.real) }
	_, _, _ = r.do("GET", "https://api.example.test/p/"+r.sur+"?k="+r.sur, map[string]string{"Authorization": r.sur}, "")
	_, _, _ = r.do("GET", "https://other.example.test/p/"+r.sur, nil, "")
	_, _, _ = r.do("GET", "https://evil.example.test/"+r.sur, nil, "")
	b, _ := json.Marshal(r.audit.Snapshot())
	if strings.Contains(string(b), r.real) || strings.Contains(string(b), r.sur) {
		t.Fatalf("audit must never hold a secret or a surrogate: %s", b)
	}
	if !strings.Contains(string(b), "surrogate:svc/key") {
		t.Fatalf("paths are redacted, not dropped: %s", b)
	}
}
