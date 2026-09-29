//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain lets this package's own test binary double as the target of its own self-reexec: wrap() (sandbox_linux.go)
// launches os.Executable() (this test binary, when running under `go test`) through the hidden landlock-exec
// subcommand to apply the Landlock restriction to itself before exec-ing the real target -- exactly what a real,
// go-build-compiled rigfile's main() already handles via a plain switch (cmd/rigfile/main.go), no test binary
// involved there. Without this, the re-exec fell through to go test's own default m.Run(), which (unlike
// cmd/rigfile's self-reexec, which passes an explicit -test.run filter) runs the WHOLE test suite unfiltered --
// including TestConfinementActuallyRestrictsNetwork itself, which calls Wrap() again, re-execs again, and recurses
// until something kills it. Found live exactly that way (Oracle Cloud Ampere A1, Ubuntu 24.04, 2026-09-28): nested
// "malformed sandbox invocation" failures several levels deep, ~23 minutes, before the process was finally killed.
// cmd/rigfile hit the identical gap and was fixed the same way (githooks_e2e_test.go); this package has no
// equivalent of that fix yet because it has no TestMain at all.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == LandlockExecSubcommand {
		os.Exit(LandlockExecMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}

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
// kernel has ever been available to it -- see TestABIVersionQuery). Verified live on real ABI 4+ hardware (Oracle
// Cloud Ampere A1, Ubuntu 24.04, kernel 6.17, 2026-09-28): passes, 5/5 clean runs.
func TestConfinementActuallyRestrictsNetwork(t *testing.T) {
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
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// dial wraps and execs THIS SAME test binary (via the hidden landlock-exec subcommand, exactly as production
	// code does) back into TestHelperDialPort, rather than shelling out to /bin/sh with bash's /dev/tcp/HOST/PORT
	// trick: Ubuntu's default /bin/sh is dash, which does not support that extension at all, so a wrapped /bin/sh
	// command using it fails before ever reaching the network -- found live (2026-09-28) once real ABI 4+
	// hardware was available to actually exercise this path; a portable Go dial avoids depending on which shell
	// or interpreter happens to be installed.
	dial := func(targetPort int) string {
		w, err := Wrap(self, []string{"-test.run=^TestHelperDialPort$"}, Policy{AllowLoopbackPorts: []int{allowedPort}})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Cleanup()
		cmd := exec.Command(w.Path, w.Args...)
		cmd.Env = append(os.Environ(), fmt.Sprintf("RIGFILE_SANDBOX_TEST_DIAL_TARGET=127.0.0.1:%d", targetPort))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("wrapped self-exec failed: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if got := dial(deniedPort); got != "DIAL_FAIL" {
		t.Fatalf("a port outside the policy must not be reachable: got %q", got)
	}
	if got := dial(allowedPort); got != "DIAL_OK" {
		t.Fatalf("an allowed port must stay reachable (that is how Level 2 works at all): got %q", got)
	}
}

// TestHelperDialPort is the target TestConfinementActuallyRestrictsNetwork execs into (see dial, above): it
// tries to dial the address named by RIGFILE_SANDBOX_TEST_DIAL_TARGET and reports whether that succeeded. A no-op
// under a normal `go test` run, like cmd/rigfile's equivalent TestHelperConfineDial.
func TestHelperDialPort(t *testing.T) {
	addr := os.Getenv("RIGFILE_SANDBOX_TEST_DIAL_TARGET")
	if addr == "" {
		return
	}
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		fmt.Println("DIAL_FAIL")
		os.Exit(0)
	}
	c.Close()
	fmt.Println("DIAL_OK")
	os.Exit(0)
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
