// Package cursor is the Cursor adapter (docs/targets/cursor.md). What it writes:
//
//	MCP servers   members of "mcpServers" in ~/.cursor/mcp.json (stdio through the rigfile exec shim)
//	rules         project scope only: .cursor/rules/<id>.mdc (alwaysApply) when --project is given
//
// Cursor has NO user-level rules file: User Rules are a setting inside the app. User-scope instructions are
// therefore printed on the plan screen to paste into Customize → Rules, and nothing is written for them.
// Skills, subagents, commands, hooks and permissions are not covered by the documentation read (UNVERIFIED) and
// are reported, not written.
package cursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/common"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// StateTarget is this adapter's key in state.json and manifest `targets:`.
const StateTarget = "cursor"

// Env describes the machine.
type Env struct {
	Plat       *platform.Info
	CursorDir  string // ~/.cursor
	ProjectDir string
	State      *state.TargetState
	Overwrite  bool
	RigfileCmd string
	CheckOnly  bool
}

// CursorDirFor is ~/.cursor (%USERPROFILE%\.cursor on Windows: UNVERIFIED, by convention).
func CursorDirFor(pi *platform.Info) (string, error) {
	h, err := pi.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".cursor"), nil
}

// Build computes the Cursor plan. It reads the machine and changes nothing.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil || env.CursorDir == "" {
		return nil, errors.New("cursor: Env needs Plat and CursorDir")
	}
	b := common.New(common.Env{Plat: env.Plat, State: env.State, Overwrite: env.Overwrite, CheckOnly: env.CheckOnly}, "Cursor")
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
		src, err := common.SrcPath(it.Dir, it.V.File)
		if err != nil {
			a.b.Fail(fmt.Errorf("instruction %s: %w", it.V.ID, err))
			return
		}
		body, err := os.ReadFile(src)
		if err != nil {
			a.b.Fail(err)
			return
		}
		if it.V.EffectiveScope() != "project" {
			a.b.Note("instruction %q is user-scope, and Cursor keeps User Rules only inside the app (Customize → Rules), not in a file. Paste this text there yourself:\n%s",
				it.V.ID, indent(strings.TrimSpace(string(body))))
			continue
		}
		if a.env.ProjectDir == "" {
			a.b.Note("instruction %q has scope: project; pass --project <dir> to install it as .cursor/rules/%s.mdc", it.V.ID, it.V.ID)
			continue
		}
		mdc := fmt.Sprintf("---\ndescription: %s\nalwaysApply: true\n---\n\n%s\n", "Rigfile: "+it.V.ID, strings.TrimSpace(string(body)))
		a.b.FileOp("instruction", it.V.ID, filepath.Join(a.env.ProjectDir, ".cursor", "rules", it.V.ID+".mdc"), []byte(mdc), 0o644, it.Layer, false)
	}
}

func indent(s string) string {
	return "      | " + strings.ReplaceAll(s, "\n", "\n      | ")
}

func (a *adapter) mcp(p *merge.Projection) {
	var entries []common.JSONEntry
	for _, s := range p.MCPServers {
		if strings.Contains(s.Name, ".") {
			a.b.Note("MCP server %q not installed for Cursor: names containing '.' are not supported", s.Name)
			continue
		}
		var m map[string]any
		if s.P.V.IsRemote() {
			if why := common.RemoteUnsupported(s.P.V); why != "" {
				a.b.Note("MCP server %q not installed for Cursor: %s", s.Name, why)
				continue
			}
			m = map[string]any{"url": s.P.V.URL}
			if len(s.P.V.Headers) > 0 {
				m["headers"] = s.P.V.Headers
			}
		} else {
			if s.P.V.CWD != "" {
				a.b.Note("MCP server %q not installed for Cursor: cwd is not carried over yet", s.Name)
				continue
			}
			cmd, args := common.ExecWrapFor(a.env.RigfileCmd, s.Name, s.P.V, p.SecretHosts)
			m = map[string]any{"type": "stdio", "command": cmd, "args": args}
		}
		raw, err := json.Marshal(m)
		if err != nil {
			a.b.Fail(err)
			return
		}
		entries = append(entries, common.JSONEntry{Name: s.Name, Raw: string(raw), Layer: s.P.Layer})
	}
	a.b.JSONMembers("mcp", filepath.Join(a.env.CursorDir, "mcp.json"), []string{"mcpServers"}, entries)
}

func (a *adapter) unsupported(p *merge.Projection) {
	for _, x := range []struct {
		n    int
		what string
	}{{len(p.Skills), "skill(s)"}, {len(p.Agents), "subagent(s)"}, {len(p.Commands), "command(s)"}, {len(p.Hooks), "hook(s)"}} {
		if x.n > 0 {
			a.b.Note("%d %s not installed for Cursor: not covered by the documentation read (UNVERIFIED, docs/targets/cursor.md)", x.n, x.what)
		}
	}
	if n := len(p.Deny) + len(p.Ask) + len(p.Allow); n > 0 {
		a.b.Note("%d permission rule(s) not written for Cursor: it has no documented permission rules; base-secure applies as instructions only", n)
	}
}
