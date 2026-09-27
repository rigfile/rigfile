// Package vscodecopilot is the GitHub Copilot in VS Code adapter (docs/targets/vscode-copilot.md). It is PROJECT-SCOPED:
// everything it writes lives in a project directory (`rigfile apply --project <dir> --target vscode-copilot`), because the
// user-level locations (the user-profile mcp.json, personal instructions) were never verified. What it writes:
//
//	MCP servers   members of "servers" in <project>/.vscode/mcp.json (stdio through the rigfile exec shim)
//	instructions  scope: project only: a marked section in <project>/.github/copilot-instructions.md
//
// Both files are meant to be COMMITTED, so what goes into them is deliberately narrow: no secret value ever (stdio servers
// call `rigfile exec`, which teammates need installed), no personal (user-scope) instructions, and nothing without --project.
// Skills, subagents, commands, hooks and permissions have no verified Copilot equivalent and are reported, not written.
package vscodecopilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigfile/rigfile/internal/adapters/common"
	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/jsonedit"
	"github.com/rigfile/rigfile/internal/merge"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/splice"
	"github.com/rigfile/rigfile/internal/state"
)

// StateTarget is this adapter's key in state.json and manifest `targets:`.
const StateTarget = "vscode-copilot"

// Env describes the machine and the project.
type Env struct {
	Plat       *platform.Info
	ProjectDir string
	State      *state.TargetState
	Overwrite  bool
	RigfileCmd string
	CheckOnly  bool
}

// Build computes the plan. It reads the project and changes nothing.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil {
		return nil, errors.New("vscode-copilot: Env needs Plat")
	}
	b := common.New(common.Env{Plat: env.Plat, State: env.State, Overwrite: env.Overwrite, CheckOnly: env.CheckOnly}, "GitHub Copilot in VS Code")
	b.Plan.Skipped = p.Skipped
	a := &adapter{b: b, env: env}
	if env.ProjectDir == "" {
		b.Note("VS Code Copilot is configured per PROJECT: pass --project <dir> to write .vscode/mcp.json and .github/copilot-instructions.md there (its user-level locations were not verified)")
		a.unsupported(p)
		return b.Result()
	}
	b.Note("these files are meant to be committed: they hold no secret values (servers call `rigfile exec`, which your teammates need installed). Use `targets: [claude-code]` on an item to keep a personal server or instruction out of this project")
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
	dest := filepath.Join(a.env.ProjectDir, ".github", "copilot-instructions.md")
	var regions []common.Region
	for _, it := range p.Instructions {
		if it.V.EffectiveScope() != "project" {
			a.b.Note("instruction %q is user-scope: not written. Copilot's personal-instruction location was not verified, and a personal instruction must not end up in a committed project file. Mark it `scope: project` to install it here", it.V.ID)
			continue
		}
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
		regions = append(regions, common.Region{ID: it.V.ID, Key: it.V.ID, Body: body, Layer: it.Layer})
	}
	tracked := false
	if a.env.State != nil {
		for _, it := range a.env.State.Items {
			if it.Kind == state.KindRegion && it.Category == "instruction" && it.Path == dest {
				tracked = true
			}
		}
	}
	if len(regions) > 0 || tracked {
		a.b.RegionSet("instruction", dest, splice.HTML, "html", false, regions)
	}
}

func (a *adapter) mcp(p *merge.Projection) {
	var entries []common.JSONEntry
	for _, s := range p.MCPServers {
		var m map[string]any
		if s.P.V.IsRemote() {
			if why := common.RemoteUnsupported(s.P.V); why != "" {
				a.b.Note("MCP server %q not written for VS Code: %s", s.Name, why)
				continue
			}
			m = map[string]any{"type": "http", "url": s.P.V.URL}
			if len(s.P.V.Headers) > 0 {
				m["headers"] = s.P.V.Headers
			}
		} else {
			if s.P.V.CWD != "" {
				a.b.Note("MCP server %q not written for VS Code: cwd is not carried over yet", s.Name)
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
	dest := filepath.Join(a.env.ProjectDir, ".vscode", "mcp.json")
	// VS Code accepts comments in mcp.json and so does the editor; only a file that is not JSON at all is left alone.
	if doc, _, _ := common.ReadOptional(dest); len(doc) > 0 && !jsonedit.Valid(doc) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		if len(names) > 0 {
			a.b.Note("%s is not valid JSON (comments and trailing commas are fine, but something else is wrong), so it was left untouched. Fix it, or add these servers under \"servers\" by hand: %s", dest, strings.Join(names, ", "))
		}
		return
	}
	a.b.JSONMembers("mcp", dest, []string{"servers"}, entries)
}

func (a *adapter) unsupported(p *merge.Projection) {
	for _, x := range []struct {
		n    int
		what string
	}{{len(p.Skills), "skill(s)"}, {len(p.Agents), "subagent(s)"}, {len(p.Commands), "command(s)"}, {len(p.Hooks), "hook(s)"}} {
		if x.n > 0 {
			a.b.Note("%d %s not written for VS Code Copilot: no verified equivalent (docs/targets/vscode-copilot.md)", x.n, x.what)
		}
	}
	if n := len(p.Deny) + len(p.Ask) + len(p.Allow); n > 0 {
		a.b.Note("%d permission rule(s) not written for VS Code Copilot: no documented per-rule permissions; base-secure applies as instructions only", n)
	}
}
