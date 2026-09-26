package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/common"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

type mcpStdio struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type mcpHTTP struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// mcpEntry renders the JSON handed to `claude mcp add-json`. Stdio servers are ALWAYS wrapped in
// `rigfile exec` so the server gets only its declared environment and its secrets come from the keychain
// at launch, never from a config file (plan §7.2 Level 1, §8.3). ok=false with a reason means the server
// cannot be expressed in Stage 1.
func mcpEntry(env Env, name string, s manifest.MCPServer, secretHosts map[string][]string) (doc string, reason string, ok bool) {
	if s.IsRemote() {
		if s.Auth == "bearer" || s.BearerToken != "" {
			return "", "bearer-token servers need a header helper or the Level-2 broker (not in Stage 1)", false
		}
		for k, v := range s.Headers {
			if _, isRef := manifest.SecretRef(v); isRef {
				return "", fmt.Sprintf("header %s references a secret; injecting secrets into headers is not supported in Stage 1", k), false
			}
		}
		b, _ := json.Marshal(mcpHTTP{Type: "http", URL: s.URL, Headers: s.Headers})
		return string(b), "", true
	}
	if s.CWD != "" {
		return "", "cwd is not expressible in Claude Code's MCP config", false
	}
	_, args := common.ExecWrapFor("", name, s, secretHosts)
	b, _ := json.Marshal(mcpStdio{Type: "stdio", Command: env.rigfile(), Args: args})
	return string(b), "", true
}

func (b *builder) mcp(p *merge.Projection) {
	env := b.env
	defer func() { b.keepOwnedOnConflict(b.plan.Ops) }()
	// servers Rigfile registered earlier that the rig no longer has: removed if the CLI is available
	if env.State != nil && !env.CheckOnly {
		current := map[string]bool{}
		for _, s := range p.MCPServers {
			current[s.Name] = true
		}
		for _, prev := range env.State.Items {
			if prev.Kind != state.KindMCP || current[prev.Key] {
				continue
			}
			if env.MCP == nil || !env.MCP.Available() {
				b.note("mcp server %q is no longer in the rig but the claude CLI is unavailable; not removed", prev.Key)
				continue
			}
			name := prev.Key
			b.plan.Ops = append(b.plan.Ops, engine.Op{Category: "mcp", Key: name, Symbol: engine.Removal, Runs: true,
				Summary: name + "   (no longer in the rig)", Do: func(x *engine.Exec) error { return env.MCP.Remove(name) }})
		}
	}
	if len(p.MCPServers) == 0 {
		return
	}
	if env.MCP == nil || !env.MCP.Available() {
		for _, s := range p.MCPServers {
			b.plan.Ops = append(b.plan.Ops, engine.Op{Category: "mcp", Key: s.Name, Symbol: engine.Conflict,
				Summary: s.Name + "   skipped: the `claude` CLI is not installed or not on PATH (ADR 0002)"})
		}
		return
	}
	for _, s := range p.MCPServers {
		name := s.Name
		doc, reason, ok := mcpEntry(env, name, s.P.V, p.SecretHosts)
		if !ok {
			b.plan.Ops = append(b.plan.Ops, engine.Op{Category: "mcp", Key: name, Symbol: engine.Conflict,
				Summary: name + "   not installed: " + reason})
			continue
		}
		hash := hashing.Bytes([]byte(doc))
		item := state.Item{Category: "mcp", Key: name, Kind: state.KindMCP, Hash: hash, Detail: map[string]string{"name": name, "scope": "user", "value": doc}}
		present, err := env.MCP.Present(name)
		if err != nil {
			b.fail(fmt.Errorf("mcp %s: %w", name, err))
			return
		}
		prev, mine := env.owned("mcp", name, "")
		what := "runs " + describeServer(env, s.P.V)
		op := engine.Op{Category: "mcp", Key: name, Runs: true, Items: []state.Item{item}, Detail: []string{what}}
		add := func(x *engine.Exec) error { return env.MCP.AddJSON(name, doc) }
		switch {
		case !present:
			op.Symbol, op.Summary, op.Do = engine.New, name+"   (user scope, via claude mcp add-json)", add
		case mine && prev.Hash == hash:
			op.Symbol, op.Summary = engine.Unchanged, name+"   (up to date)"
		case mine || env.Overwrite:
			op.Symbol, op.Summary = engine.Update, name+"   (definition changed)"
			op.Do = func(x *engine.Exec) error {
				if err := env.MCP.Remove(name); err != nil {
					return err
				}
				return env.MCP.AddJSON(name, doc)
			}
		default:
			op.Symbol, op.Summary, op.Items = engine.Conflict, name+"   a server with this name exists and is not managed by Rigfile; not touched (use --overwrite)", nil
		}
		b.plan.Ops = append(b.plan.Ops, op)
	}
}

func describeServer(env Env, s manifest.MCPServer) string {
	if s.IsRemote() {
		return "remote " + s.URL
	}
	return strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
}

// MCPProbe is the drift probe for state.KindMCP items: present in Claude Code or not. (Whether the
// entry's content was edited cannot be read back: the vendor CLI's output format is undocumented.)
func MCPProbe(c MCPClient) state.Probe {
	return func(it state.Item) (state.Status, string) {
		if c == nil || !c.Available() {
			return state.Unknown, "the claude CLI is not available"
		}
		ok, err := c.Present(it.Detail["name"])
		switch {
		case err != nil:
			return state.Unknown, err.Error()
		case !ok:
			return state.Missing, "not registered in Claude Code"
		}
		return state.OK, ""
	}
}

// CLIClient talks to the real `claude` binary. UNVERIFIED (ADR 0002): the exit status of
// `claude mcp get` for a missing server, and `--scope` on `remove`; both are isolated here.
type CLIClient struct {
	Bin     string        // default "claude"
	Timeout time.Duration // default 30s
}

func (c CLIClient) bin() string {
	if c.Bin == "" {
		return "claude"
	}
	return c.Bin
}

func (c CLIClient) run(args ...string) error {
	t := c.Timeout
	if t == 0 {
		t = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), t)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.bin(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude %s: %w: %s", strings.Join(args[:min(3, len(args))], " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Available reports whether the CLI is on PATH.
func (c CLIClient) Available() bool { _, err := exec.LookPath(c.bin()); return err == nil }

// Present is true when `claude mcp get <name>` succeeds.
func (c CLIClient) Present(name string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, c.bin(), "mcp", "get", name).Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if asExit(err, &ee) {
		return false, nil // a non-zero exit is taken as "not found"
	}
	return false, err
}

// AddJSON registers the server in user scope.
func (c CLIClient) AddJSON(name, jsonDoc string) error {
	return c.run("mcp", "add-json", name, jsonDoc, "--scope", "user")
}

// Remove unregisters the user-scope server.
func (c CLIClient) Remove(name string) error { return c.run("mcp", "remove", name, "--scope", "user") }

func asExit(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}
