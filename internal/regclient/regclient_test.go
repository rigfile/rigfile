package regclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateBase(t *testing.T) {
	for in, want := range map[string]string{"https://registry.example.org": "https://registry.example.org", "https://registry.example.org/": "https://registry.example.org",
		"http://localhost:8080": "http://localhost:8080", "http://127.0.0.1:9": "http://127.0.0.1:9"} {
		if got, err := ValidateBase(in); err != nil || got != want {
			t.Errorf("ValidateBase(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "registry.example.org", "http://registry.example.org", "ftp://x", "https://u:p@x.example", "https://x.example/path", "https://x.example?q=1", "file:///etc"} {
		if _, err := ValidateBase(in); err == nil {
			t.Errorf("ValidateBase(%q) should fail", in)
		}
	}
}

func TestTokenGoesOnlyToTheRegistryAndRedirectsStayOnHost(t *testing.T) {
	var seen []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "other:"+r.Header.Get("Authorization"))
	}))
	defer other.Close()
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "reg:"+r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v1/me":
			_, _ = io.WriteString(w, `{"login":"jia"}`)
		case "/v1/rigs/o/n":
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
		case "/v1/rigs/o/same":
			http.Redirect(w, r, "/v1/me", http.StatusFound)
		}
	}))
	defer reg.Close()
	c := &Client{Base: reg.URL, Token: func() string { return "rgf_secret" }}
	if login, err := c.Me(context.Background()); err != nil || login != "jia" {
		t.Fatalf("%v", err)
	}
	if _, err := c.Rig(context.Background(), "o", "n"); err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("a redirect to another host must be refused: %v", err)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "other:") {
			t.Fatalf("the other host was contacted: %v", seen)
		}
	}
}

func TestErrorsCarryTheRegistrysMessageAndProblems(t *testing.T) {
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = io.WriteString(w, `{"error":"the rig has errors","problems":["skills[0]: missing"]}`)
	}))
	defer reg.Close()
	c := &Client{Base: reg.URL}
	_, err := c.Upload(context.Background(), "o", "n", []byte("x"))
	var ae *APIError
	if err == nil || !strings.Contains(err.Error(), "skills[0]: missing") {
		t.Fatalf("%v", err)
	}
	ae, _ = err.(*APIError)
	if ae == nil || ae.Status != 422 {
		t.Fatalf("%+v", ae)
	}
}

func TestDownloadIsCapped(t *testing.T) {
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Rigfile-SHA256", strings.Repeat("a", 64))
		for i := 0; i < 3; i++ {
			_, _ = w.Write(make([]byte, 1<<20))
		}
	}))
	defer reg.Close()
	c := &Client{Base: reg.URL}
	rc, sha, err := c.Download(context.Background(), "o", "n", "1.0.0")
	if err != nil || len(sha) != 64 {
		t.Fatal(err)
	}
	defer rc.Close()
	if n, err := io.Copy(io.Discard, io.LimitReader(rc, 10<<20)); err != nil || n != 3<<20 {
		t.Fatalf("%d %v", n, err)
	}
	l := &limited{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", 100))), left: 10}
	if _, err := io.ReadAll(l); err == nil {
		t.Fatal("the cap must stop a larger body")
	}
}
