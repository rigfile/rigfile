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
// Verified enforcement on macOS (sandbox-exec, tested live in internal/sandbox). On Linux this project's own dev
// machines have no usable Landlock (even a 5.15 kernel answers ENOSYS: internal/sandbox_test.go), but CI's
// ubuntu-latest apparently now has ABI 4 (found live, 2026-09-28) — so all three outcomes below are real,
// reachable branches on some machine this project actually runs on, not hypothetical. The Landlock backend
// self-checks before trusting itself (internal/sandbox/sandbox_linux.go: dial a canary port the ruleset
// deliberately excludes; refuse to launch if that dial unexpectedly succeeds), which is what keeps outcome 2
// possible at all: a kernel that answers the Landlock syscalls successfully without actually enforcing anything
// is refused here rather than silently trusted.
func TestExecConfineBlocksTheBrokerControlAPI(t *testing.T) {
	r := newL2(t, true)
	if x := r.m.run("", "broker", "enable"); x.code != 0 {
		t.Fatalf("%+v", x)
	}
	args := append([]string{"exec", "--server", "alpaca", "--secret", "ALPACA_API_KEY=alpaca/api_key", "--confine"}, l2args...)
	args = append(args, "--env", "RIGFILE_TEST_HELPER_DIAL=1", "--env", "RIGFILE_TEST_DIAL_PROXY="+r.b.Info().Proxy, "--env", "RIGFILE_TEST_DIAL_API="+r.b.Info().API)
	args = append(args, "--", os.Args[0], "-test.run=^TestHelperConfineDial$")
	res := r.m.run("", args...)

	assertReallyConfined := func() {
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
	}

	switch runtime.GOOS {
	case "darwin":
		if res.code != 0 {
			t.Fatalf("%+v", res)
		}
		assertReallyConfined()
	default:
		switch {
		case res.code == 0:
			// This machine's kernel has Landlock ABI 4 and the self-check found it genuinely enforcing (a real,
			// verified outcome now, not a hypothetical -- see the function comment). Hold it to the exact same
			// bar as darwin: reporting success is not enough, the control API must actually be unreachable.
			assertReallyConfined()
		case strings.Contains(res.err, "not available"):
			// No Landlock, or an ABI below 4: wrap() refused before ever launching the child (internal/sandbox/
			// sandbox_linux.go, internal/sandbox/sandbox_other.go). Fails closed, as intended.
		case strings.Contains(res.err, "did not actually restrict"):
			// Landlock answered ABI >= 4 and the syscalls returned success, but the canary self-check proved
			// outbound connections were not actually restricted -- refused rather than launched believing itself
			// confined when it was not (internal/sandbox/sandbox_linux.go LandlockExecMain).
		default:
			t.Fatalf("this platform/kernel has no verified confinement backend and gave no recognized fail-closed refusal: %+v", res)
		}
	}
}
