// Package execshim implements `rigfile exec`: run a command with secrets injected into ITS environment
// only (plan §7.2 Level 1). MCP server entries are rewritten to
//
//	command: rigfile, args: [exec, --secret ENV=ref ..., --, npx, -y, pkg@1.2.3]
//
// so no secret is ever written to a config file, and the agent never sees one.
//
// Threat notes:
//   - The child gets a minimal environment (platform.BaseEnvKeys) plus what the rig declares; the
//     parent's other variables (which may hold unrelated secrets) are not inherited (plan §8.3).
//   - A missing secret aborts BEFORE the child starts, naming only the ref.
//   - Secret values are never logged or included in returned errors.
//   - Secrets are in the child's environment, so the child (and its own children) can read them:
//     this is the documented Level-1 limit; Level 2 (rigd surrogates) closes it.
package execshim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"syscall"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/secrets"
)

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Spec describes one launch.
type Spec struct {
	Command []string          // argv; Command[0] is looked up on the (filtered) PATH
	Secrets map[string]string // ENV_NAME -> secret ref (e.g. "alpaca/api_key")
	Literal map[string]string // ENV_NAME -> non-secret value from the manifest
	Store   secrets.Store
	Plat    *platform.Info
	Getenv  func(string) string // parent environment; default os.Getenv
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// BuildEnv returns the child's environment as KEY=VALUE strings, sorted by key for determinism.
func BuildEnv(s Spec) ([]string, error) {
	getenv := s.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	env := map[string]string{}
	for _, k := range s.Plat.BaseEnvKeys() {
		if v := getenv(k); v != "" {
			env[k] = v
		}
	}
	for k, v := range s.Literal {
		if !envNameRe.MatchString(k) {
			return nil, fmt.Errorf("execshim: invalid environment variable name %q", k)
		}
		env[k] = v
	}
	for k, ref := range s.Secrets {
		if !envNameRe.MatchString(k) {
			return nil, fmt.Errorf("execshim: invalid environment variable name %q", k)
		}
		if s.Store == nil {
			return nil, errors.New("execshim: no secret store")
		}
		v, err := s.Store.Get(ref)
		if err != nil {
			if secrets.IsNotFound(err) {
				return nil, fmt.Errorf("secret %q (for %s) is not set: run `rigfile secrets set %s`", ref, k, ref)
			}
			return nil, fmt.Errorf("read secret %q: %w", ref, err)
		}
		env[k] = string(v)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out, nil
}

// Run starts the command and returns its exit code. It forwards SIGINT/SIGTERM to the child.
func Run(ctx context.Context, s Spec) (int, error) {
	if len(s.Command) == 0 {
		return 2, errors.New("execshim: no command given")
	}
	env, err := BuildEnv(s)
	if err != nil {
		return 2, err
	}
	// Resolve through PATH/PATHEXT so that on Windows `npx` finds npx.cmd; Go runs .cmd/.bat files through
	// cmd.exe with its own argument escaping and refuses arguments it cannot escape safely (no shell injection).
	prog, err := exec.LookPath(s.Command[0])
	if err != nil {
		return 127, fmt.Errorf("%s was not found on PATH (install it, or fix the server's command): %w", s.Command[0], err)
	}
	cmd := exec.CommandContext(ctx, prog, s.Command[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s.Stdin, s.Stdout, s.Stderr

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("start %s: %w", s.Command[0], err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-sigs:
			_ = platform.ForwardSignal(cmd.Process, sig)
		case err := <-done:
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return ee.ExitCode(), nil
			}
			if err != nil {
				return 1, err
			}
			return 0, nil
		}
	}
}
