//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
)

// TestABIVersionQuery calls the real syscall (read-only: it queries the version, it does not create a ruleset or
// restrict anything) on whatever kernel runs this test. Landlock support varies a lot in practice even among
// kernels new enough in principle: it can be compiled out, or left off the active LSM list. Confirmed here: Docker
// Desktop's linuxkit VM (5.15.49) answers ENOSYS despite being newer than the 5.13 that introduced Landlock. So
// this is a skip, not a failure, when the syscall errors — that is exactly the situation Wrap must fail closed for
// (TestWrapFailsClosedWhenLandlockUnavailable), not a broken test environment.
func TestABIVersionQuery(t *testing.T) {
	abi, err := abiVersion()
	if err != nil {
		t.Skipf("no Landlock on this machine (%v); see TestWrapFailsClosedWhenLandlockUnavailable instead", err)
	}
	if abi < 1 || abi > 64 {
		t.Fatalf("implausible ABI version: %d", abi)
	}
	t.Logf("this machine's Landlock ABI: %d (network confinement needs %d)", abi, requiredABI)
}

// TestWrapFailsClosedWhenLandlockUnavailable covers the case this project's own dev/CI environment actually hits:
// Landlock entirely absent. Wrap must refuse, never return a command that would run unconfined.
func TestWrapFailsClosedWhenLandlockUnavailable(t *testing.T) {
	if _, err := abiVersion(); err == nil {
		t.Skip("this machine has Landlock; see TestWrapFailsClosedBelowRequiredABI / TestConfinementActuallyRestrictsNetwork instead")
	}
	_, err := Wrap("/bin/true", nil, Policy{AllowLoopbackPorts: []int{1234}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("must fail closed when Landlock is unavailable, got: %v", err)
	}
}

// TestWrapFailsClosedBelowRequiredABI proves that on a kernel with Landlock but too old an ABI for network
// restriction (ABI 4 needs Linux 6.7+), Wrap still refuses rather than running unconfined.
func TestWrapFailsClosedBelowRequiredABI(t *testing.T) {
	abi, err := abiVersion()
	if err != nil {
		t.Skip("no Landlock at all on this machine; covered by TestWrapFailsClosedWhenLandlockUnavailable instead")
	}
	if abi >= requiredABI {
		t.Skipf("this machine's kernel already supports ABI %d; nothing to prove here (see TestConfinementActuallyRestrictsNetwork below)", abi)
	}
	_, err = Wrap("/bin/true", nil, Policy{AllowLoopbackPorts: []int{1234}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("must fail closed below ABI %d, got: %v", requiredABI, err)
	}
}

// TestLandlockExecMainRejectsMalformedInvocation covers the parsing main.go's hidden dispatch relies on, without
// needing to actually apply a restriction (which would affect this test binary's own process for the rest of the
// run). args is [ports, canary port, "--", prog, progArgs...].
func TestLandlockExecMainRejectsMalformedInvocation(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"8081"},
		{"8081", "9090"},
		{"8081", "9090", "not-the-separator", "/bin/true"},
		{"not-a-number", "9090", "--", "/bin/true"},
		{"99999999", "9090", "--", "/bin/true"},
		{"8081", "not-a-number", "--", "/bin/true"},
		{"8081", "99999999", "--", "/bin/true"},
	} {
		if got := LandlockExecMain(args); got != 2 {
			t.Errorf("LandlockExecMain(%v) = %d, want 2 (and no restriction applied)", args, got)
		}
	}
}

// TestConfinementActuallyRestrictsNetwork is the Linux counterpart to the darwin test of the same name: on a
// kernel that actually has ABI 4, prove the restriction (and the canary self-check in wrap/LandlockExecMain) is
// for real, not just "the syscalls returned success". Skips everywhere this project's own machines run (no ABI 4
// kernel has ever been available to it -- see TestABIVersionQuery). It also skips while disableLandlockNetConfinement
// is set (sandbox_linux.go): CI's ubuntu-latest does answer ABI >= 4, but Wrap now refuses unconditionally there
// pending investigation of the per-port enforcement gap found live 2026-09-28 (docs/rigd.md §8) -- exercising this
// test against that refusal isn't "no ABI 4 kernel", it's a different, already-known and already-asserted-elsewhere
// condition, so it would just fail here with a misleading message instead of skipping cleanly.
func TestConfinementActuallyRestrictsNetwork(t *testing.T) {
	if disableLandlockNetConfinement {
		t.Skip("Linux network confinement is disabled pending investigation (see disableLandlockNetConfinement in sandbox_linux.go); nothing to prove here until it's re-enabled")
	}
	abi, err := abiVersion()
	if err != nil || abi < requiredABI {
		t.Skip("no ABI 4 Landlock on this machine; see TestWrapFailsClosed* instead")
	}
	denied, derr := net.Listen("tcp", "127.0.0.1:0")
	if derr != nil {
		t.Fatal(derr)
	}
	defer denied.Close()
	allowed, aerr := net.Listen("tcp", "127.0.0.1:0")
	if aerr != nil {
		t.Fatal(aerr)
	}
	defer allowed.Close()
	go acceptAndClose(allowed)
	deniedPort := denied.Addr().(*net.TCPAddr).Port
	allowedPort := allowed.Addr().(*net.TCPAddr).Port

	w, err := Wrap("/bin/sh", []string{"-c", fmt.Sprintf("(echo x >/dev/tcp/127.0.0.1/%d) 2>/dev/null && echo DENIED-PORT-REACHABLE", deniedPort)}, Policy{AllowLoopbackPorts: []int{allowedPort}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cleanup()
	out, err := exec.Command(w.Path, w.Args...).CombinedOutput()
	if err != nil {
		t.Fatalf("wrapped /bin/sh itself failed (want it to run, just unable to reach the denied port): %v: %s", err, out)
	}
	if strings.Contains(string(out), "DENIED-PORT-REACHABLE") {
		t.Fatalf("a port outside the policy must not be reachable: %s", out)
	}

	w2, err := Wrap("/bin/sh", []string{"-c", fmt.Sprintf("echo x | timeout 3 sh -c 'exec 3<>/dev/tcp/127.0.0.1/%d' && echo ALLOWED-PORT-REACHED", allowedPort)}, Policy{AllowLoopbackPorts: []int{allowedPort}})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Cleanup()
	out2, err := exec.Command(w2.Path, w2.Args...).CombinedOutput()
	if err != nil || !strings.Contains(string(out2), "ALLOWED-PORT-REACHED") {
		t.Fatalf("an allowed port must stay reachable (that is how Level 2 works at all): %v: %s", err, out2)
	}
}

func acceptAndClose(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		c.Close()
	}
}
