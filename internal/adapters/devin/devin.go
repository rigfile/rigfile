// Package devin is the Devin Desktop adapter (docs/targets/devin.md). The product was called Windsurf; the
// owner confirmed on 2026-09-27, on their own machine, that it now writes to Devin's path. What it writes:
//
//	MCP servers   members of "mcpServers" in ~/.config/devin/mcp_config.json (stdio only, through the rigfile exec shim)
//
// The path and the top-level "mcpServers" key are both confirmed against a real, running app. The per-server
// field names (command/args) match the vendor docs, which also agree on this shape, but that has not been
// re-checked against a live populated entry (the owner's file was empty). Remote servers are skipped: the docs
// disagree on the field name (`serverUrl` in one place, `url` in another) and picking wrong would silently
// configure nothing (working agreement 2). Instructions/rules, skills, subagents, commands, hooks and
// permissions were not covered by the documentation read (UNVERIFIED) and are reported, not written.
package devin

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/rigfile/rigfile/internal/adapters/common"
	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/merge"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/state"
)

// StateTarget is this adapter's key in state.json and manifest `targets:`.
const StateTarget = "devin"

// Env describes the machine.
type Env struct {
	Plat       *platform.Info
	ConfigDir  string // the directory holding mcp_config.json (~/.config/devin, %APPDATA%\devin)
	State      *state.TargetState
	Overwrite  bool
	RigfileCmd string
	CheckOnly  bool
}

// ConfigDirFor is ${CONFIG_DIR}/devin: confirmed on macOS 2026-09-27 (~/.config/devin); Linux matches by the
// same vendor doc; Windows (%APPDATA%\devin) is from the docs only, not run (docs/targets/devin.md).
func ConfigDirFor(pi *platform.Info) (string, error) {
	c, err := pi.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "devin"), nil
}

// Build computes the Devin plan. It reads the machine and changes nothing.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil || env.ConfigDir == "" {
		return nil, errors.New("devin: Env needs Plat and ConfigDir")
	}
	b := common.New(common.Env{Plat: env.Plat, State: env.State, Overwrite: env.Overwrite, CheckOnly: env.CheckOnly}, "Devin")
	b.Plan.Skipped = p.Skipped
	a := &adapter{b: b, env: env}
	a.instructions(p)
	a.mcp(p)
	a.unsupported(p)
	return b.Result()
}

type adapter struct {
	b   *common.Builder
	env Env
}

func (a *adapter) instructions(p *merge.Projection) {
	for _, it := range p.Instructions {
		a.b.Note("instruction %q not installed for Devin: no rules/instructions location was covered by the documentation read (UNVERIFIED, docs/targets/devin.md)", it.V.ID)
	}
}

func (a *adapter) mcp(p *merge.Projection) {
	var entries []common.JSONEntry
	for _, s := range p.MCPServers {
		if strings.Contains(s.Name, ".") {
			a.b.Note("MCP server %q not installed for Devin: names containing '.' are not supported", s.Name)
			continue
		}
		if s.P.V.IsRemote() {
			a.b.Note("MCP server %q not installed for Devin: the vendor docs disagree on the remote-server field name (serverUrl vs url), so writing one was not verified", s.Name)
			continue
		}
		if s.P.V.CWD != "" {
			a.b.Note("MCP server %q not installed for Devin: cwd is not carried over yet", s.Name)
			continue
		}
		cmd, args := common.ExecWrapFor(a.env.RigfileCmd, s.Name, s.P.V, p.SecretHosts)
		m := map[string]any{"command": cmd, "args": args}
		raw, err := json.Marshal(m)
		if err != nil {
			a.b.Fail(err)
			return
		}
		entries = append(entries, common.JSONEntry{Name: s.Name, Raw: string(raw), Layer: s.P.Layer})
	}
	a.b.JSONMembers("mcp", filepath.Join(a.env.ConfigDir, "mcp_config.json"), []string{"mcpServers"}, entries)
}

func (a *adapter) unsupported(p *merge.Projection) {
	for _, x := range []struct {
		n    int
		what string
	}{{len(p.Skills), "skill(s)"}, {len(p.Agents), "subagent(s)"}, {len(p.Commands), "command(s)"}, {len(p.Hooks), "hook(s)"}} {
		if x.n > 0 {
			a.b.Note("%d %s not installed for Devin: not covered by the documentation read (UNVERIFIED, docs/targets/devin.md)", x.n, x.what)
		}
	}
	if n := len(p.Deny) + len(p.Ask) + len(p.Allow); n > 0 {
		a.b.Note("%d permission rule(s) not written for Devin: it has no documented permission rules; base-secure applies as instructions only", n)
	}
}
