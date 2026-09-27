//go:build linux

package sandbox

import (
	"errors"
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
// run).
func TestLandlockExecMainRejectsMalformedInvocation(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"8081"},
		{"8081", "not-the-separator", "/bin/true"},
		{"not-a-number", "--", "/bin/true"},
		{"99999999", "--", "/bin/true"},
	} {
		if got := LandlockExecMain(args); got != 2 {
			t.Errorf("LandlockExecMain(%v) = %d, want 2 (and no restriction applied)", args, got)
		}
	}
}
