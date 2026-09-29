//go:build linux

package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock network restriction (LANDLOCK_ACCESS_NET_CONNECT_TCP) needs ABI 4, added in Linux 6.7. Filesystem-only
// Landlock (ABI 1) has existed since Linux 5.13, but this package never restricts the filesystem (see sandbox.go),
// so ABI 1-3 buys nothing here and is refused: better to fail closed and say why than to silently grant no
// protection. `golang.org/x/sys/unix` (already a dependency) has the three syscall numbers; it has no Go wrappers
// or struct types for Landlock yet, so the raw syscalls and kernel UAPI structs are reproduced below.
const requiredABI = 4

const (
	landlockAccessNetConnectTCP  = 1 << 1
	landlockRuleNetPort          = 2
	landlockCreateRulesetVersion = 1 << 0 // flag: return the supported ABI version instead of creating a ruleset
)

// rulesetAttr mirrors struct landlock_ruleset_attr (include/uapi/linux/landlock.h).
type rulesetAttr struct {
	HandledAccessFS  uint64
	HandledAccessNet uint64
}

// netPortAttr mirrors struct landlock_net_port_attr.
type netPortAttr struct {
	AllowedAccess uint64
	Port          uint64
}

// abiVersion queries the kernel's supported Landlock ABI without creating a ruleset or restricting anything; safe
// to call from any process at any time.
func abiVersion() (int, error) {
	r0, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return 0, errno
	}
	return int(r0), nil
}

// wrap re-execs this same binary under the hidden landlock-exec subcommand, which applies the restriction to
// itself and then execve()s into prog. The preflight ABI check here means a person sees a clear, immediate refusal
// rather than a confusing failure from deep inside the helper.
//
// A successful Landlock syscall sequence is not, by itself, proof that anything is actually restricted, so rather
// than trust the syscalls' return codes alone, wrap opens a canary TCP listener on a random loopback port that is
// deliberately left OUT of the ruleset, and the restricted child dials it before ever exec-ing the real target
// (LandlockExecMain below): if that dial succeeds, the restriction plainly is not taking effect, and the child
// refuses to proceed rather than launch the real command believing itself confined when it is not.
//
// Verified live on real Landlock ABI 4+ hardware (Oracle Cloud Ampere A1, Ubuntu 24.04, kernel 6.17,
// 2026-09-28): a real confined child correctly reaches the broker's proxy and correctly cannot reach its control
// API (TestExecConfineBlocksTheBrokerControlAPI), 5/5 clean runs, same OS thread throughout restrict -> self-check
// -> exec. An earlier real CI failure on GitHub Actions' ubuntu-latest that looked exactly like broken per-port
// enforcement (a canary port correctly denied, the control-API port not) turned out to be a test-harness gap, not
// a problem here: TestMain (cmd/rigfile/githooks_e2e_test.go) wasn't dispatching the re-exec'd hidden subcommand
// to LandlockExecMain at all when running under `go test`, so the "confined" child in that failure was never
// actually restricted in the first place. Fixed there; this file's own logic needed no change.
func wrap(prog string, args []string, policy Policy) (*Wrapped, error) {
	abi, err := abiVersion()
	if err != nil {
		return nil, fmt.Errorf("%w: Landlock is not available (%v)", ErrUnsupported, err)
	}
	if abi < requiredABI {
		return nil, fmt.Errorf("%w: this kernel's Landlock is ABI %d; network confinement needs ABI %d (Linux 6.7 or newer)", ErrUnsupported, abi, requiredABI)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	canary, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("sandbox: could not open the self-check listener: %w", err)
	}
	go func() { // accept and drop connections for as long as the listener is open; canaryPort below is the point
		for {
			c, err := canary.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	canaryPort := canary.Addr().(*net.TCPAddr).Port
	ports := make([]string, len(policy.AllowLoopbackPorts))
	for i, p := range policy.AllowLoopbackPorts {
		ports[i] = strconv.Itoa(p)
	}
	full := append([]string{LandlockExecSubcommand, strings.Join(ports, ","), strconv.Itoa(canaryPort), "--", prog}, args...)
	return &Wrapped{Path: self, Args: full, Cleanup: func() { canary.Close() }}, nil
}

// createNetRuleset builds a Landlock ruleset that only permits outbound TCP connect to the given ports (all other
// LANDLOCK_ACCESS_NET_CONNECT_TCP-governed connects, i.e. every outbound TCP connect, are denied by omission —
// Landlock is allow-list only, so anything not explicitly added is refused once handled_access_net is set).
func createNetRuleset(ports []int) (fd int, err error) {
	attr := rulesetAttr{HandledAccessNet: landlockAccessNetConnectTCP}
	r0, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return -1, errno
	}
	rfd := int(r0)
	for _, port := range ports {
		rule := netPortAttr{AllowedAccess: landlockAccessNetConnectTCP, Port: uint64(port)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rfd), landlockRuleNetPort, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			unix.Close(rfd)
			return -1, errno
		}
	}
	return rfd, nil
}

// restrictSelf applies the ruleset to the calling process. Unprivileged use requires PR_SET_NO_NEW_PRIVS first;
// once applied, the restriction cannot be lifted and is inherited by every process this one execs or forks.
func restrictSelf(fd int) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(fd), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// LandlockExecMain is the hidden subcommand's entry point (main.go routes landlockExecSubcommand here). args is
// ["<comma-separated ports, may be empty>", "<canary port>", "--", prog, progArgs...]. It never returns on
// success: unix.Exec replaces this process image with prog, still carrying the Landlock restriction. It returns
// an exit code only on failure, always before any restriction could have leaked into a process it did not intend
// to confine, and always before prog is exec'd.
func LandlockExecMain(args []string) int {
	if len(args) < 4 || args[2] != "--" {
		fmt.Fprintln(os.Stderr, "rigfile: internal: malformed sandbox invocation")
		return 2
	}
	var ports []int
	if args[0] != "" {
		for _, s := range strings.Split(args[0], ",") {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > 65535 {
				fmt.Fprintln(os.Stderr, "rigfile: internal: bad sandbox port", s)
				return 2
			}
			ports = append(ports, n)
		}
	}
	canaryPort, err := strconv.Atoi(args[1])
	if err != nil || canaryPort < 1 || canaryPort > 65535 {
		fmt.Fprintln(os.Stderr, "rigfile: internal: bad sandbox canary port", args[1])
		return 2
	}
	fd, err := createNetRuleset(ports)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rigfile: sandbox: could not create the network ruleset:", err)
		return 1
	}
	if err := restrictSelf(fd); err != nil {
		unix.Close(fd)
		fmt.Fprintln(os.Stderr, "rigfile: sandbox: could not apply the restriction:", err)
		return 1
	}
	unix.Close(fd)
	// Self-check before trusting the restriction: dial a port that is deliberately not in the ruleset. If this
	// succeeds, the syscalls above returned success without actually restricting outbound connects on this
	// kernel -- refuse rather than exec prog believing it is confined when it is not (see wrap, above).
	if c, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", canaryPort), 2*time.Second); dialErr == nil {
		c.Close()
		fmt.Fprintln(os.Stderr, "rigfile: sandbox: the restriction was applied but did not actually restrict outbound connections on this kernel; refusing to launch unconfined")
		return 1
	}
	prog, progArgs := args[3], args[4:]
	path := prog
	if !strings.Contains(prog, "/") {
		if p, err := exec.LookPath(prog); err == nil {
			path = p
		}
	}
	// once restricted, the process can still read/exec normally (only outbound TCP connect is governed); the
	// restriction stays in effect across this exec, which is the whole point.
	if err := unix.Exec(path, append([]string{prog}, progArgs...), os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "rigfile: sandbox: exec:", prog, err)
		return 127
	}
	return 0 // unreachable: unix.Exec only returns on error
}
