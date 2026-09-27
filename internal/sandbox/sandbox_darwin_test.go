//go:build darwin

package sandbox

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestProfileFormat(t *testing.T) {
	got := profile(Policy{AllowLoopbackPorts: []int{8082, 8081}})
	want := "(version 1)\n(allow default)\n(deny network-outbound)\n" +
		"(allow network-outbound (remote ip \"localhost:8081\"))\n" +
		"(allow network-outbound (remote ip \"localhost:8082\"))\n"
	if got != want {
		t.Fatalf("profile:\n%s\nwant:\n%s", got, want)
	}
	if got := profile(Policy{}); got != "(version 1)\n(allow default)\n(deny network-outbound)\n" {
		t.Fatalf("empty policy must still deny all network: %s", got)
	}
}

// TestConfinementActuallyRestrictsNetwork proves the built profile does what it claims, against the real
// sandbox-exec on this machine: a confined child can reach an allowed loopback port and nothing else.
func TestConfinementActuallyRestrictsNetwork(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }
	allowed := httptest.NewServer(http.HandlerFunc(ok))
	defer allowed.Close()
	denied := httptest.NewServer(http.HandlerFunc(ok))
	defer denied.Close()
	allowedPort := mustPort(t, allowed.URL)
	deniedPort := mustPort(t, denied.URL)

	w, err := Wrap("/usr/bin/curl", []string{"-s", "-m", "3", "http://127.0.0.1:" + deniedPort + "/"}, Policy{AllowLoopbackPorts: []int{atoi(t, allowedPort)}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, w.Path, w.Args...).CombinedOutput(); err == nil {
		t.Fatalf("a denied port must not be reachable, got: %q", out)
	}

	w2, err := Wrap("/usr/bin/curl", []string{"-s", "-m", "3", "http://127.0.0.1:" + allowedPort + "/"}, Policy{AllowLoopbackPorts: []int{atoi(t, allowedPort)}})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Cleanup()
	out, err := exec.CommandContext(ctx, w2.Path, w2.Args...).CombinedOutput()
	if err != nil || string(out) != "ok" {
		t.Fatalf("the allowed port must still be reachable: %v %q", err, out)
	}
}

func mustPort(t *testing.T, url string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(url, "http://"), "https://"))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a port: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
