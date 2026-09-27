package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

// startLevel2 decides the level for a launch (docs/rigd.md §7). It returns (nil, "", nil) for Level 1 with an optional
// notice for the person, or a live session for Level 2. An error means "do not start the server".
func startLevel2(dir, server string, allow []string, sec map[string]string) (*level2, string, error) {
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
	// the request names the server and its secrets; the broker takes hosts and the allowlist from what `rigfile apply`
	// approved, so nothing here (or in an attacker's request) can widen them
	req := rigd.SessionRequest{Server: server}
	for _, k := range envs {
		req.Secrets = append(req.Secrets, rigd.RequestedSecret{Env: k, Ref: sec[k]})
	}
	reply, err := c.Open(req)
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
