// Package sandbox confines an MCP server child that `rigfile exec` launches under Level 2, so that if the server
// itself is malicious (the threat model in docs/rigd.md §1: "the attacker controls the MCP server process"), it
// cannot also reach the broker's control API and borrow another server's session — the residual risk noted in
// docs/rigd.md §8, closed here for the case that matters most: a server Rigfile itself launched.
//
// The policy is deliberately narrow: network only, restricted to the loopback ports the child legitimately needs
// (the broker's CONNECT proxy). Filesystem access is never restricted here — a well-meaning but incomplete
// allow-list would risk silently breaking a server's own files (npm caches, project directories, and so on), which
// is worse than not sandboxing at all. Owner decision 2026-09-27 (docs/rigd.md §8): opt-in per server, via
// `rigfile exec --confine`.
//
// Every backend fails closed: if the platform or kernel cannot honour the policy, Wrap returns ErrUnsupported
// rather than silently returning an unconfined command. The caller must refuse to launch, not fall back quietly.
package sandbox

import (
	"errors"
	"fmt"
)

// Policy is what a confined child may still reach.
type Policy struct {
	// AllowLoopbackPorts are the 127.0.0.1 TCP ports the child may still connect out to. Nothing else on the
	// network is reachable: not the broker's own control API, not another loopback port, not the wider network.
	AllowLoopbackPorts []int
}

// ErrUnsupported means this platform, or this kernel, has no confinement backend that can honour the policy. The
// caller must not launch unconfined when the person asked for confinement.
var ErrUnsupported = errors.New("sandbox: confinement is not available on this platform or kernel")

// LandlockExecSubcommand is the hidden `rigfile` argv[1] that main.go must route to LandlockExecMain. It never
// appears in --help. On Linux, wrap() re-execs this same binary with this as its first argument: applying a
// Landlock restriction only affects the calling process (and whatever it execs afterwards), so the process that
// restricts itself must be the one that then execs the real target, not the parent that launched it.
const LandlockExecSubcommand = "__rigfile-sandbox-landlock-exec"

// Wrapped is prog/args rewritten to run under confinement, ready to exec.
type Wrapped struct {
	Path    string
	Args    []string // does not include Path; pass to exec.Cmd.Args as [Path, Args...] or exec.Command(Path, Args...)
	Cleanup func()   // removes any temporary files; safe to call once the process has started (or on error)
}

// Wrap prepares prog/args (prog already resolved, e.g. via exec.LookPath) to run confined by policy. It does not
// start the process. An empty policy (no allowed ports) is still a real policy: the child gets no network at all.
func Wrap(prog string, args []string, policy Policy) (*Wrapped, error) {
	for _, p := range policy.AllowLoopbackPorts {
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("sandbox: invalid port %d", p)
		}
	}
	return wrap(prog, args, policy)
}

// noCleanup is used by backends that create no temporary files.
func noCleanup() {}
