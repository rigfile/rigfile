package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/rigd"
)

// level2 is a live broker session for one `rigfile exec` launch.
type level2 struct {
	client  *rigd.Client
	reply   *rigd.OpenReply
	caDir   string
	env     map[string]string // what the child gets instead of the real secrets
	surEnvs map[string]string
}

// end closes the session and removes the CA file. Safe to call on nil.
func (l *level2) end() {
	if l == nil {
		return
	}
	_ = l.client.Close(l.reply.SessionID)
	_ = os.RemoveAll(l.caDir)
}

// launcherHosts are the package registries a launcher such as npx must reach before the server itself starts. They carry
// no secret (no surrogate is bound to them), so allowing them does not widen where a key can go.
func launcherHosts(argv0 string) []string {
	if i := strings.LastIndexAny(argv0, `/\`); i >= 0 {
		argv0 = argv0[i+1:] // either separator: a manifest written on Windows may be read anywhere
	}
	base := strings.ToLower(argv0)
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

// startLevel2 decides the level for a launch (docs/rigd.md §7). It returns (nil, "", nil) for Level 1 with an optional
// notice for the person, or a live session for Level 2. An error means "do not start the server".
func startLevel2(dir, server string, argv []string, allow []string, binds map[string][]string, sec map[string]string) (*level2, string, error) {
	if len(sec) == 0 {
		return nil, "", nil
	}
	cfg, err := rigd.LoadConfig(dir)
	if err != nil {
		return nil, "", fmt.Errorf("reading the broker settings: %w", err)
	}
	if !cfg.Enabled {
		return nil, "", nil
	}
	if cfg.IsExcluded(server) {
		return nil, "rigfile: " + server + " runs at Level 1 (excluded from the broker): its real key is in the process", nil
	}
	if len(allow) == 0 {
		return nil, "rigfile: " + server + " declares no network.allow, so the broker has nothing to enforce; running at Level 1 (real key in the process)", nil
	}
	c, err := rigd.ClientFromDir(dir)
	if err != nil {
		if errors.Is(err, rigd.ErrNotRunning) {
			return nil, "", fmt.Errorf("%s declares network.allow but the broker is not running. Start it (`rigfile broker run`, or `rigfile broker install`), or run this server at Level 1 with `rigfile broker exclude %s`", server, server)
		}
		return nil, "", err
	}
	envs := make([]string, 0, len(sec))
	for k := range sec {
		envs = append(envs, k)
	}
	sort.Strings(envs)
	spec := rigd.SessionSpec{Server: server, Allow: append(append([]string(nil), allow...), launcherHosts(argv[0])...)}
	for _, k := range envs {
		hosts := binds[sec[k]]
		if len(hosts) == 0 {
			return nil, "", fmt.Errorf("secret %s (for %s) has no hosts: add secrets.%s.hosts to the rig so the broker knows where it may be sent, or `rigfile broker exclude %s`", sec[k], k, sec[k], server)
		}
		spec.Secrets = append(spec.Secrets, rigd.SecretSpec{Env: k, Ref: sec[k], Hosts: hosts})
	}
	reply, err := c.Open(spec)
	if err != nil {
		var ae *rigd.APIError
		if errors.As(err, &ae) || errors.Is(err, rigd.ErrNotRunning) {
			return nil, "", fmt.Errorf("the broker could not protect %s: %v (fix that, or `rigfile broker exclude %s` to run it at Level 1)", server, err, server)
		}
		return nil, "", err
	}
	l := &level2{client: c, reply: reply, env: map[string]string{}, surEnvs: reply.Surrogates}
	if l.caDir, err = os.MkdirTemp("", "rigfile-ca-"); err != nil {
		l.end()
		return nil, "", err
	}
	caPath := filepath.Join(l.caDir, "ca.pem")
	if err := os.WriteFile(caPath, []byte(reply.CAPEM), 0o600); err != nil {
		l.end()
		return nil, "", err
	}
	l.env = rigd.ChildEnv(reply, caPath)
	return l, "", nil
}
