package rigd

import (
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCAIssuesConstrainedLeafCertificates(t *testing.T) {
	clk := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	ca, err := NewCA(func() time.Time { return clk })
	if err != nil {
		t.Fatal(err)
	}
	c := ca.Certificate()
	if !c.IsCA || !c.BasicConstraintsValid || c.MaxPathLen != 0 || !c.MaxPathLenZero || c.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("the CA must be a path-length-0 CA: %+v", c)
	}
	pool := x509.NewCertPool()
	pool.AddCert(c)
	leaf, err := ca.LeafFor("api.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "api.example.test", CurrentTime: clk, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("leaf must verify against the CA: %v", err)
	}
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "other.example.test", CurrentTime: clk}); err == nil {
		t.Fatal("a leaf is only good for its own host")
	}
	if leaf.Leaf.IsCA || leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore) > LeafValidity+time.Hour {
		t.Fatalf("leaf: %+v", leaf.Leaf)
	}
	// cached, per host, and IP literals get an IP SAN
	again, _ := ca.LeafFor("api.example.test")
	if again != leaf {
		t.Fatal("leaves are cached")
	}
	ipLeaf, err := ca.LeafFor("127.0.0.1")
	if err != nil || len(ipLeaf.Leaf.IPAddresses) != 1 || len(ipLeaf.Leaf.DNSNames) != 0 {
		t.Fatalf("%v %+v", err, ipLeaf)
	}
	// expiry: a leaf close to its end is replaced
	clk = clk.Add(LeafValidity)
	fresh, _ := ca.LeafFor("api.example.test")
	if fresh == leaf {
		t.Fatal("an expiring leaf must be reissued")
	}
	// a different CA does not verify it
	other, _ := NewCA(nil)
	otherPool := x509.NewCertPool()
	otherPool.AddCert(other.Certificate())
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: otherPool, CurrentTime: clk.Add(-LeafValidity)}); err == nil {
		t.Fatal("another CA's pool must not verify it")
	}
	if !strings.Contains(string(ca.PEM()), "BEGIN CERTIFICATE") || strings.Contains(string(ca.PEM()), "PRIVATE") {
		t.Fatal("the PEM is the certificate only")
	}
	if _, err := ca.LeafFor(""); err == nil {
		t.Fatal("empty host")
	}
}

func TestHostPatterns(t *testing.T) {
	type c struct {
		pat, host string
		port      int
		want      bool
	}
	for _, x := range []c{
		{"api.example.com", "api.example.com", 443, true},
		{"api.example.com", "API.Example.COM.", 443, true}, // case and trailing dot
		{"api.example.com", "api.example.com", 8443, false},
		{"api.example.com:8443", "api.example.com", 8443, true},
		{"api.example.com", "evil.api.example.com", 443, false},
		{"api.example.com", "api.example.com.evil.test", 443, false},
		{"*.example.com", "a.example.com", 443, true},
		{"*.example.com", "a.b.example.com", 443, true},
		{"*.example.com", "example.com", 443, false}, // never the apex
		{"*.example.com", "badexample.com", 443, false},
		{"*.example.com", "a.example.com.evil.test", 443, false},
		{"127.0.0.1", "127.0.0.1", 443, true},
		{"127.0.0.1", "localhost", 443, false},
		{"api.example.com", "93.184.216.34", 443, false}, // a name pattern never matches an IP
		{"93.184.216.34", "api.example.com", 443, false},
		{"[::1]:9000", "::1", 9000, true},
		{"bücher.example", "xn--bcher-kva.example", 443, true}, // IDNA
		{"xn--bcher-kva.example", "BÜCHER.example", 443, true},
		{"api.example.com", "api.example.com\x00.evil.test", 443, false},
	} {
		p, err := ParsePattern(x.pat)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", x.pat, err)
		}
		if got := p.Match(x.host, x.port); got != x.want {
			t.Errorf("%q matches %q:%d = %v, want %v", x.pat, x.host, x.port, got, x.want)
		}
	}
	for _, bad := range []string{"", "*", "*.com", "a*.example.com", "*.*.example.com", "exa mple.com", "example.com/path", "user@example.com", "example.com:0", "example.com:99999", "example.com:x", "*.", "http://example.com", "[::1"} {
		if _, err := ParsePattern(bad); err == nil {
			t.Errorf("ParsePattern(%q) should fail", bad)
		}
	}
	if p, _ := ParsePattern("*.Example.COM:8443"); p.String() != "*.example.com:8443" {
		t.Errorf("canonical form: %s", p)
	}
}

func resolver(m map[string]string) Resolver {
	return func(ref string) ([]byte, error) {
		if v, ok := m[ref]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("not found")
	}
}

func TestSurrogatesAreUniqueSessionScopedAndExpire(t *testing.T) {
	clk := time.Now()
	st := NewSessionStore(func() time.Time { return clk })
	res := resolver(map[string]string{"alpaca/key": "REAL-KEY-VALUE-1234", "alpaca/secret": "REAL-SECRET-VALUE-5678"})
	spec := SessionSpec{Server: "alpaca", Allow: []string{"api.alpaca.markets", "*.alpaca.markets"},
		Secrets: []SecretSpec{{Env: "KEY", Ref: "alpaca/key", Hosts: []string{"api.alpaca.markets"}}, {Env: "SECRET", Ref: "alpaca/secret", Hosts: []string{"*.alpaca.markets"}}}}
	s1, err := st.New(spec, res)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := st.New(spec, res)
	sur1, sur2 := s1.Surrogates(spec), s2.Surrogates(spec)
	if !strings.HasPrefix(sur1["KEY"], SurrogatePrefix) || sur1["KEY"] == sur1["SECRET"] || sur1["KEY"] == sur2["KEY"] {
		t.Fatalf("surrogates must be distinct across secrets and sessions: %v %v", sur1, sur2)
	}
	for _, v := range sur1 {
		if strings.Contains(v, "REAL") || len(v) < len(SurrogatePrefix)+30 {
			t.Fatalf("a surrogate carries nothing of the real value: %q", v)
		}
	}
	// credentials authenticate only their own session
	if got, ok := st.Authenticate(s1.User, s1.Password); !ok || got != s1 {
		t.Fatal("authenticate")
	}
	if _, ok := st.Authenticate(s1.User, s2.Password); ok {
		t.Fatal("another session's password must not work")
	}
	if _, ok := st.Authenticate("nobody", "x"); ok {
		t.Fatal("unknown user")
	}
	if !s1.Allowed("api.alpaca.markets", 443) || s1.Allowed("evil.example", 443) {
		t.Fatal("Allowed")
	}
	// close wipes and forgets
	real := s1.bindings[0].real
	if !st.Close(s1.ID) || st.Close(s1.ID) {
		t.Fatal("close once")
	}
	if strings.Contains(string(real), "REAL") {
		t.Fatal("a closed session's values are wiped")
	}
	if _, ok := st.Authenticate(s1.User, s1.Password); ok {
		t.Fatal("closed")
	}
	// expiry
	clk = clk.Add(25 * time.Hour)
	if _, ok := st.Authenticate(s2.User, s2.Password); ok {
		t.Fatal("expired")
	}
	if st.Live() != 0 {
		t.Fatal("expired sessions are swept")
	}
}

func TestSessionsRefuseWhatCannotBeEnforcedSafely(t *testing.T) {
	st := NewSessionStore(nil)
	res := resolver(map[string]string{"a/k": "REAL-KEY-VALUE-1234", "a/short": "abc"})
	ok := SecretSpec{Env: "K", Ref: "a/k", Hosts: []string{"api.example.com"}}
	for name, spec := range map[string]SessionSpec{
		"no server":         {Allow: []string{"x.example.com"}, Secrets: []SecretSpec{ok}},
		"no allowlist":      {Server: "s", Secrets: []SecretSpec{ok}},
		"secret w/o hosts":  {Server: "s", Allow: []string{"x.example.com"}, Secrets: []SecretSpec{{Env: "K", Ref: "a/k"}}},
		"secret too short":  {Server: "s", Allow: []string{"x.example.com"}, Secrets: []SecretSpec{{Env: "K", Ref: "a/short", Hosts: []string{"x.example.com"}}}},
		"unknown secret":    {Server: "s", Allow: []string{"x.example.com"}, Secrets: []SecretSpec{{Env: "K", Ref: "a/none", Hosts: []string{"x.example.com"}}}},
		"bad pattern":       {Server: "s", Allow: []string{"*.com"}, Secrets: []SecretSpec{ok}},
		"duplicate env":     {Server: "s", Allow: []string{"x.example.com"}, Secrets: []SecretSpec{ok, ok}},
		"bad bound pattern": {Server: "s", Allow: []string{"x.example.com"}, Secrets: []SecretSpec{{Env: "K", Ref: "a/k", Hosts: []string{"a*"}}}},
	} {
		if _, err := st.New(spec, res); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	// a server with no secrets is fine (an allowlist alone is still enforceable)
	if _, err := st.New(SessionSpec{Server: "s", Allow: []string{"x.example.com"}}, res); err != nil {
		t.Fatal(err)
	}
	st.MaxLive = 1
	if _, err := st.New(SessionSpec{Server: "s2", Allow: []string{"x.example.com"}}, res); err == nil {
		t.Fatal("the number of live sessions is bounded")
	}
}
