package rigd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigfile/rigfile/internal/state"
)

// Policies is what the last `rigfile apply` approved, per MCP server (docs/rigd.md §3a).
type Policies map[string]state.ServerPolicy

// LoadPolicy reads the approved policy from state.json in stateDir. A missing state is an empty policy: nothing is approved,
// so every session request is refused.
func LoadPolicy(stateDir string) (Policies, error) {
	st, err := state.Load(stateDir)
	if err != nil {
		return nil, err
	}
	return Policies(st.Broker), nil
}

// SessionRequest is what a launcher (`rigfile exec`) may ask for: a server by name, and which of that server's declared
// secrets it wants. It carries NO hosts and NO allowlist: those come from the approved policy, so whoever holds the API token
// (a compromised child running as the same user) cannot bind a key to a host of its choosing.
type SessionRequest struct {
	Server  string            `json:"server"`
	Secrets []RequestedSecret `json:"secrets,omitempty"`
}

// RequestedSecret names one environment variable and the secret reference the launcher believes it carries.
type RequestedSecret struct {
	Env string `json:"env"`
	Ref string `json:"ref"`
}

// LauncherHosts are the package registries a launcher such as npx must reach before the server itself starts. No secret is
// bound to them, so allowing them does not widen where a key can go.
func LauncherHosts(command string) []string {
	if i := strings.LastIndexAny(command, `/\`); i >= 0 {
		command = command[i+1:] // either separator: a manifest written on Windows may be read anywhere
	}
	base := strings.ToLower(command)
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		base = strings.TrimSuffix(base, ext)
	}
	switch base {
	case "npx", "npm", "bunx", "bun", "pnpm", "pnpx", "yarn":
		return []string{"registry.npmjs.org"}
	case "uvx", "uv", "pipx", "pip":
		return []string{"pypi.org", "files.pythonhosted.org"}
	}
	return nil
}

// Resolve turns a request into a session spec using the approved policy, or explains the refusal. The message names
// servers, environment variables and references, never values.
func (ps Policies) Resolve(req SessionRequest) (SessionSpec, error) {
	pol, ok := ps[req.Server]
	if !ok {
		return SessionSpec{}, fmt.Errorf("the broker has no approved policy for the server %q: run `rigfile apply` for the rig that defines it (declaring network.allow and the secrets' hosts)", req.Server)
	}
	spec := SessionSpec{Server: req.Server, Allow: append(append([]string(nil), pol.Allow...), LauncherHosts(pol.Command)...)}
	seen := map[string]bool{}
	for _, r := range req.Secrets {
		b, ok := pol.Secrets[r.Env]
		if !ok {
			return SessionSpec{}, fmt.Errorf("the approved policy for %q does not give %s a secret", req.Server, r.Env)
		}
		if b.Ref != r.Ref {
			return SessionSpec{}, fmt.Errorf("the approved policy for %q binds %s to a different secret than the one requested", req.Server, r.Env)
		}
		if seen[r.Env] {
			return SessionSpec{}, fmt.Errorf("%s is requested twice", r.Env)
		}
		seen[r.Env] = true
		spec.Secrets = append(spec.Secrets, SecretSpec{Env: r.Env, Ref: b.Ref, Hosts: append([]string(nil), b.Hosts...)})
	}
	sort.Slice(spec.Secrets, func(i, j int) bool { return spec.Secrets[i].Env < spec.Secrets[j].Env })
	return spec, nil
}

// stateDirOf is the state directory the broker's files live under (<state>/rigd).
func stateDirOf(brokerDir string) string { return filepath.Dir(brokerDir) }
