// Package claudedesktop is the Claude Desktop adapter (docs/targets/claude-desktop.md): MCP only. It edits
// "mcpServers" in claude_desktop_config.json (macOS: ~/Library/Application Support/Claude, Windows:
// %APPDATA%\Claude); there is no Linux build. Only local stdio servers live in that file (remote connectors are
// added in the app), and Claude Desktop must be fully restarted to pick changes up.
package claudedesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/common"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// StateTarget is this adapter's key in state.json and manifest `targets:`.
const StateTarget = "claude-desktop"

// Env describes the machine.
type Env struct {
	Plat       *platform.Info
	DesktopDir string // the Claude app-data directory
	State      *state.TargetState
	Overwrite  bool
	RigfileCmd string
	CheckOnly  bool
}

// Available reports whether Claude Desktop exists on this OS (macOS and Windows only).
func Available(pi *platform.Info) (bool, string) {
	if pi.OS == platform.Linux {
		return false, "Claude Desktop has no Linux build (macOS and Windows only)"
	}
	return true, ""
}

// DesktopDirFor resolves the Claude app-data directory: macOS ~/Library/Application Support/Claude, Windows
// %APPDATA%\Claude (the platform layer's ${APP_DATA}).
func DesktopDirFor(pi *platform.Info) (string, error) {
	a, err := pi.AppData()
	if err != nil {
		return "", err
	}
	return filepath.Join(a, "Claude"), nil
}

// ConfigFile is the config file inside DesktopDir.
const ConfigFile = "claude_desktop_config.json"

// Build computes the Claude Desktop plan. It reads the machine and changes nothing.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil || env.DesktopDir == "" {
		return nil, errors.New("claudedesktop: Env needs Plat and DesktopDir")
	}
	b := common.New(common.Env{Plat: env.Plat, State: env.State, Overwrite: env.Overwrite, CheckOnly: env.CheckOnly}, "Claude Desktop")
	b.Plan.Skipped = p.Skipped
	if ok, why := Available(env.Plat); !ok {
		b.Note("%s", why)
		return b.Result()
	}
	var entries []common.JSONEntry
	for _, s := range p.MCPServers {
		switch {
		case strings.Contains(s.Name, "."):
			b.Note("MCP server %q not installed for Claude Desktop: names containing '.' are not supported", s.Name)
		case s.P.V.IsRemote():
			b.Note("remote MCP server %q is not written: Claude Desktop manages remote connectors in the app (Connectors → Add custom connector), not in its config file", s.Name)
		case s.P.V.CWD != "":
			b.Note("MCP server %q not installed for Claude Desktop: cwd is not carried over yet", s.Name)
		default:
			cmd, args := common.ExecWrapFor(env.RigfileCmd, s.Name, s.P.V, p.SecretHosts)
			raw, err := json.Marshal(map[string]any{"command": cmd, "args": args})
			if err != nil {
				b.Fail(err)
				return nil, err
			}
			entries = append(entries, common.JSONEntry{Name: s.Name, Raw: string(raw), Layer: s.P.Layer})
		}
	}
	b.JSONMembers("mcp", filepath.Join(env.DesktopDir, ConfigFile), []string{"mcpServers"}, entries)
	if n := len(p.Instructions) + len(p.Skills) + len(p.Agents) + len(p.Commands) + len(p.Hooks) + len(p.Deny) + len(p.Ask) + len(p.Allow); n > 0 {
		b.Note("%d instruction/skill/agent/command/hook/permission item(s) skipped: Claude Desktop only supports MCP servers", n)
	}
	if len(entries) > 0 {
		b.Note("restart Claude Desktop completely (quit, then reopen) for MCP changes to take effect")
	}
	if plan, err := b.Result(); err == nil {
		return plan, nil
	} else {
		return nil, fmt.Errorf("claudedesktop: %w", err)
	}
}
