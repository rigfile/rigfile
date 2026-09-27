// Package zed is the Zed adapter (docs/targets/zed.md). What it writes:
//
//	MCP servers   members of "context_servers" in ~/.config/zed/settings.json (comment-preserving:
//	              settings.json is JSONC), stdio through the rigfile exec shim, remote as url/headers
//
// The path is confirmed on macOS (the owner ran `zed: open settings file` on their own machine, 2026-09-27)
// and matches the vendor docs' Linux path; the Windows path is from the docs only, not run. Instructions/rules,
// skills, subagents, commands, hooks and permissions were not covered by the documentation read (UNVERIFIED,
// `https://zed.dev/docs/ai/rules` returned 404) and are reported, not written.
package zed

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
const StateTarget = "zed"

// Env describes the machine.
type Env struct {
	Plat       *platform.Info
	ConfigDir  string // the directory holding settings.json (~/.config/zed; %APPDATA%\Zed unverified)
	State      *state.TargetState
	Overwrite  bool
	RigfileCmd string
	CheckOnly  bool
}

// ConfigDirFor is confirmed on macOS (~/.config/zed, 2026-09-27) and matches the vendor docs' Linux path.
// Windows (%APPDATA%\Zed) is from docs/targets/zed.md's citation of the uninstall page only, not run.
func ConfigDirFor(pi *platform.Info) (string, error) {
	if pi.OS == platform.Windows {
		a, err := pi.AppData()
		if err != nil {
			return "", err
		}
		return filepath.Join(a, "Zed"), nil
	}
	c, err := pi.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "zed"), nil
}

// Build computes the Zed plan. It reads the machine and changes nothing.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil || env.ConfigDir == "" {
		return nil, errors.New("zed: Env needs Plat and ConfigDir")
	}
	b := common.New(common.Env{Plat: env.Plat, State: env.State, Overwrite: env.Overwrite, CheckOnly: env.CheckOnly}, "Zed")
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
		a.b.Note("instruction %q not installed for Zed: `https://zed.dev/docs/ai/rules` returned 404 when checked, so no rules/instructions location is verified (docs/targets/zed.md)", it.V.ID)
	}
}

func (a *adapter) mcp(p *merge.Projection) {
	var entries []common.JSONEntry
	for _, s := range p.MCPServers {
		if strings.Contains(s.Name, ".") {
			a.b.Note("MCP server %q not installed for Zed: names containing '.' are not supported", s.Name)
			continue
		}
		var m map[string]any
		if s.P.V.IsRemote() {
			if why := common.RemoteUnsupported(s.P.V); why != "" {
				a.b.Note("MCP server %q not installed for Zed: %s", s.Name, why)
				continue
			}
			m = map[string]any{"url": s.P.V.URL}
			if len(s.P.V.Headers) > 0 {
				m["headers"] = s.P.V.Headers
			}
		} else {
			if s.P.V.CWD != "" {
				a.b.Note("MCP server %q not installed for Zed: cwd is not carried over yet", s.Name)
				continue
			}
			cmd, args := common.ExecWrapFor(a.env.RigfileCmd, s.Name, s.P.V, p.SecretHosts)
			m = map[string]any{"command": cmd, "args": args}
		}
		raw, err := json.Marshal(m)
		if err != nil {
			a.b.Fail(err)
			return
		}
		entries = append(entries, common.JSONEntry{Name: s.Name, Raw: string(raw), Layer: s.P.Layer})
	}
	a.b.JSONMembers("mcp", filepath.Join(a.env.ConfigDir, "settings.json"), []string{"context_servers"}, entries)
}

func (a *adapter) unsupported(p *merge.Projection) {
	for _, x := range []struct {
		n    int
		what string
	}{{len(p.Skills), "skill(s)"}, {len(p.Agents), "subagent(s)"}, {len(p.Commands), "command(s)"}, {len(p.Hooks), "hook(s)"}} {
		if x.n > 0 {
			a.b.Note("%d %s not installed for Zed: not covered by the documentation read (UNVERIFIED, docs/targets/zed.md)", x.n, x.what)
		}
	}
	if n := len(p.Deny) + len(p.Ask) + len(p.Allow); n > 0 {
		a.b.Note("%d permission rule(s) not written for Zed: it has no documented permission rules; base-secure applies as instructions only", n)
	}
}
