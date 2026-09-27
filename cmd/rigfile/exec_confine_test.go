package main

import (
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestHelperConfineDial is the child launched by the confinement test: it tries to dial two addresses (the
// broker's proxy, which a confined server must still reach, and the broker's own control API, which it must not)
// and reports which succeeded.
func TestHelperConfineDial(t *testing.T) {
	if os.Getenv("RIGFILE_TEST_HELPER_DIAL") != "1" {
		return
	}
	dial := func(addr string) bool {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
	out, _ := json.Marshal(map[string]bool{
		"proxy_ok":   dial(os.Getenv("RIGFILE_TEST_DIAL_PROXY")),
		"api_denied": !dial(os.Getenv("RIGFILE_TEST_DIAL_API")),
	})
	os.Stdout.Write(append(out, '\n'))
	os.Exit(0)
}

// TestExecConfineBlocksTheBrokerControlAPI is the direct test of the owner decision (2026-09-27, docs/rigd.md §8):
// a server launched under Level 2 with --confine can still reach the broker's proxy, but not its control API —
// closing the "a same-user process can borrow another approved server's session" gap for a server Rigfile itself
// launched (the threat model in docs/rigd.md §1: the attacker controls the MCP server process).
//
// Verified enforcement on macOS (sandbox-exec, tested live in internal/sandbox). On Linux, this project's own dev
// and CI environments have no usable Landlock (see internal/sandbox: even a 5.15 kernel answered ENOSYS), so
// --confine fails closed there and this test asserts exactly that refusal, not the enforcement itself — real
// Linux network confinement needs Landlock ABI 4 (Linux 6.7+) and remains UNVERIFIED on such a kernel.
func TestExecConfineBlocksTheBrokerControlAPI(t *testing.T) {
	r := newL2(t, true)
	if x := r.m.run("", "broker", "enable"); x.code != 0 {
		t.Fatalf("%+v", x)
	}
	args := append([]string{"exec", "--server", "alpaca", "--secret", "ALPACA_API_KEY=alpaca/api_key", "--confine"}, l2args...)
	args = append(args, "--env", "RIGFILE_TEST_HELPER_DIAL=1", "--env", "RIGFILE_TEST_DIAL_PROXY="+r.b.Info().Proxy, "--env", "RIGFILE_TEST_DIAL_API="+r.b.Info().API)
	args = append(args, "--", os.Args[0], "-test.run=^TestHelperConfineDial$")
	res := r.m.run("", args...)

	switch runtime.GOOS {
	case "darwin":
		if res.code != 0 {
			t.Fatalf("%+v", res)
		}
		var got map[string]bool
		if err := json.Unmarshal([]byte(strings.TrimSpace(res.out)), &got); err != nil {
			t.Fatalf("%v: %q", err, res.out)
		}
		if !got["proxy_ok"] {
			t.Fatal("a confined server must still reach the broker's proxy (that is how Level 2 works at all)")
		}
		if !got["api_denied"] {
			t.Fatal("a confined server must NOT reach the broker's control API: this is the whole point of --confine")
		}
	default:
		if res.code == 0 {
			t.Fatalf("this platform/kernel has no verified confinement backend; --confine must fail closed rather than launch unconfined: %+v", res)
		}
		if !strings.Contains(res.err, "not available") {
			t.Fatalf("the refusal should say confinement is unavailable: %q", res.err)
		}
	}
}
