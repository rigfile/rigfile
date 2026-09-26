// Package gemini is the Gemini CLI adapter (docs/targets/gemini-cli.md). What it writes:
//
//	instructions  a marked section in <gemini home>/GEMINI.md (and a note if context.fileName excludes GEMINI.md)
//	commands      <gemini home>/commands/<name>.toml (Markdown command → TOML with prompt/description)
//	MCP servers   members of "mcpServers" in <gemini home>/settings.json, layout-preserving edits
//
// Skills and subagents do not exist for Gemini CLI. Hooks exist but their settings contract is UNVERIFIED, and
// per-rule permissions use tool-exclusion syntax that is UNVERIFIED, so both are reported, not written.
package gemini

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/common"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/jsonedit"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/splice"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// StateTarget is this adapter's key in state.json and manifest `targets:`.
const StateTarget = "gemini-cli"

// Env describes the machine.
type Env struct {
	Plat       *platform.Info
	GeminiDir  string // ~/.gemini
	ProjectDir string
	State      *state.TargetState
	Overwrite  bool
	RigfileCmd string
	CheckOnly  bool
}

// GeminiDirFor resolves the user-level directory: $GEMINI_CLI_HOME/.gemini when set (the docs call it the "root
// directory for user-level configuration": whether the .gemini folder sits inside it is UNVERIFIED), else ~/.gemini.
func GeminiDirFor(pi *platform.Info, getenv func(string) string) (string, error) {
	if d := getenv("GEMINI_CLI_HOME"); d != "" {
		return filepath.Join(d, ".gemini"), nil
	}
	h, err := pi.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".gemini"), nil
}

// Build computes the Gemini CLI plan. It reads the machine and changes nothing.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil || env.GeminiDir == "" {
		return nil, errors.New("gemini: Env needs Plat and GeminiDir")
	}
	b := common.New(common.Env{Plat: env.Plat, State: env.State, Overwrite: env.Overwrite, CheckOnly: env.CheckOnly}, "Gemini CLI")
	b.Plan.Skipped = p.Skipped
	a := &adapter{b: b, env: env}
	a.instructions(p)
	a.commands(p)
	a.mcp(p)
	a.unsupported(p)
	return b.Result()
}

type adapter struct {
	b   *common.Builder
	env Env
}

func (a *adapter) instructions(p *merge.Projection) {
	user := filepath.Join(a.env.GeminiDir, "GEMINI.md")
	groups := map[string][]common.Region{}
	var order []string
	add := func(dest string, r common.Region) {
		if _, ok := groups[dest]; !ok {
			order = append(order, dest)
		}
		groups[dest] = append(groups[dest], r)
	}
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
		dest := user
		if it.V.EffectiveScope() == "project" {
			if a.env.ProjectDir == "" {
				a.b.Note("instruction %q has scope: project; pass --project <dir> to install it", it.V.ID)
				continue
			}
			dest = filepath.Join(a.env.ProjectDir, "GEMINI.md")
		}
		add(dest, common.Region{ID: it.V.ID, Key: it.V.ID, Body: body, Layer: it.Layer})
	}
	if a.env.State != nil {
		for _, it := range a.env.State.Items {
			if it.Kind == state.KindRegion && it.Category == "instruction" {
				if _, ok := groups[it.Path]; !ok {
					groups[it.Path] = nil
					order = append(order, it.Path)
				}
			}
		}
	}
	for _, dest := range order {
		a.b.RegionSet("instruction", dest, splice.HTML, "html", false, groups[dest])
	}
	if len(groups[user]) > 0 {
		a.checkContextFileName()
	}
}

// checkContextFileName warns when settings.json changes which context files Gemini CLI reads so that GEMINI.md
// is no longer one of them.
func (a *adapter) checkContextFileName() {
	doc, _, _ := common.ReadOptional(filepath.Join(a.env.GeminiDir, "settings.json"))
	raw, ok := jsonedit.ReadValueRaw(doc, []string{"context", "fileName"})
	if !ok {
		return
	}
	if !strings.Contains(raw, "GEMINI.md") {
		a.b.Note("settings.json sets context.fileName to %s, which does not include GEMINI.md: Gemini CLI will not read the Rigfile section until you add GEMINI.md", raw)
	}
}

type commandTOML struct {
	Description string `toml:"description,omitempty"`
	Prompt      string `toml:"prompt"`
}

func (a *adapter) commands(p *merge.Projection) {
	for _, c := range p.Commands {
		key := c.V.Key()
		src, err := common.SrcPath(c.Dir, c.V.Path)
		if err != nil {
			a.b.Fail(fmt.Errorf("command %s: %w", key, err))
			return
		}
		data, err := os.ReadFile(src)
		if err != nil {
			a.b.Fail(err)
			return
		}
		fm, body := common.Frontmatter(data)
		prompt := strings.TrimSpace(body)
		if strings.Contains(prompt, "$ARGUMENTS") {
			prompt = strings.ReplaceAll(prompt, "$ARGUMENTS", "{{args}}")
		}
		if strings.Contains(prompt, "$1") || strings.Contains(prompt, "!`") {
			a.b.Note("command %q uses positional arguments or inline shell that Gemini CLI writes differently ({{args}}, !{…}); check it after applying", key)
		}
		enc, err := toml.Marshal(commandTOML{Description: common.String(fm, "description"), Prompt: prompt + "\n"})
		if err != nil {
			a.b.Fail(err)
			return
		}
		a.b.FileOp("command", key, filepath.Join(a.env.GeminiDir, "commands", key+".toml"), enc, 0o644, c.Layer, false)
	}
}

func (a *adapter) mcp(p *merge.Projection) {
	var entries []common.JSONEntry
	for _, s := range p.MCPServers {
		if strings.Contains(s.Name, ".") {
			a.b.Note("MCP server %q not installed for Gemini CLI: names containing '.' are not supported", s.Name)
			continue
		}
		var raw string
		if s.P.V.IsRemote() {
			if why := common.RemoteUnsupported(s.P.V); why != "" {
				a.b.Note("MCP server %q not installed for Gemini CLI: %s", s.Name, why)
				continue
			}
			m := map[string]any{"httpUrl": s.P.V.URL}
			if len(s.P.V.Headers) > 0 {
				m["headers"] = s.P.V.Headers
			}
			raw = mustJSON(m)
			a.b.Note("remote MCP server %q is written as httpUrl (streamable HTTP); if it speaks SSE, change the key to url by hand. This mapping is UNVERIFIED against a real Gemini CLI", s.Name)
		} else {
			if s.P.V.CWD != "" {
				a.b.Note("MCP server %q not installed for Gemini CLI: cwd is not carried over yet", s.Name)
				continue
			}
			cmd, args := common.ExecWrapFor(a.env.RigfileCmd, s.Name, s.P.V, p.SecretHosts)
			raw = mustJSON(map[string]any{"command": cmd, "args": args})
		}
		entries = append(entries, common.JSONEntry{Name: s.Name, Raw: raw, Layer: s.P.Layer})
	}
	a.b.JSONMembers("mcp", filepath.Join(a.env.GeminiDir, "settings.json"), []string{"mcpServers"}, entries)
}

func (a *adapter) unsupported(p *merge.Projection) {
	if n := len(p.Skills); n > 0 {
		a.b.Note("%d skill(s) not installed for Gemini CLI: it has no skills", n)
	}
	if n := len(p.Agents); n > 0 {
		a.b.Note("%d subagent(s) not installed for Gemini CLI: it has no subagents", n)
	}
	if n := len(p.Hooks); n > 0 {
		a.b.Note("%d hook(s) not installed for Gemini CLI: its hooks settings contract is UNVERIFIED (docs/targets/gemini-cli.md)", n)
	}
	if n := len(p.Deny) + len(p.Ask) + len(p.Allow); n > 0 {
		a.b.Note("%d permission rule(s) not written for Gemini CLI: its tool-exclusion syntax is UNVERIFIED; base-secure applies as instructions only", n)
	}
}
