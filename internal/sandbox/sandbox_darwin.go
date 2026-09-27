//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// wrap runs prog under /usr/bin/sandbox-exec with a generated Seatbelt (SBPL) profile: everything is allowed
// (`allow default`) except outbound network, which is denied and then re-allowed only for the given loopback
// ports. sandbox-exec execve()s straight into the target (verified: the sandboxed process keeps the same PID, so
// the exec.Cmd's Process, signal forwarding and exit code all work exactly as for an unwrapped launch.
//
// SBPL syntax note: `(remote ip "127.0.0.1:PORT")` is refused ("host must be * or localhost in network address");
// the accepted form is `(remote ip "localhost:PORT")`, verified against the sandbox-exec on this machine.
func wrap(prog string, args []string, policy Policy) (*Wrapped, error) {
	f, err := os.CreateTemp("", "rigfile-sandbox-*.sb")
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if _, err := f.WriteString(profile(policy)); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	path := f.Name()
	full := append([]string{"-f", path, prog}, args...)
	return &Wrapped{Path: "/usr/bin/sandbox-exec", Args: full, Cleanup: func() { _ = os.Remove(path) }}, nil
}

func profile(policy Policy) string {
	ports := append([]int(nil), policy.AllowLoopbackPorts...)
	sort.Ints(ports)
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny network-outbound)\n")
	for _, p := range ports {
		fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%d\"))\n", p)
	}
	return b.String()
}
