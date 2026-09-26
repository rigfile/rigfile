// Package codex is the Codex CLI adapter (docs/targets/codex.md). What it writes:
//
//	instructions  a marked section in <CODEX_HOME>/AGENTS.md (noting AGENTS.override.md, which would shadow it)
//	skills        ~/.agents/skills/<name>            (open Agent Skills format; NOT under ~/.codex)
//	agents        <CODEX_HOME>/agents/<name>.toml    (Markdown agent → TOML: name, description, developer_instructions)
//	commands      ~/.agents/skills/<name>/SKILL.md   (custom prompts are deprecated: commands become skills)
//	MCP servers   [mcp_servers.<name>] tables in <CODEX_HOME>/config.toml, one marked region per server
//
// Hooks and per-rule permissions are NOT written: Codex requires the user to review and trust hooks (hash-pinned)
// and has no per-rule deny list, so those items are reported on the plan screen (base-secure's Codex mapping is
// S3-M6). The user's config.toml is never re-serialised: only marked regions are inserted, and the result is
// validated, so a table the user already defined is a refused op rather than a broken file.
package codex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/common"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/splice"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// StateTarget is this adapter's key in state.json and manifest `targets:`.
const StateTarget = "codex"

// Env describes the machine.
type Env struct {
	Plat       *platform.Info
	CodexDir   string // ~/.codex or $CODEX_HOME
	ProjectDir string // "" = project-scope items are skipped with a note
	State      *state.TargetState
	Overwrite  bool
	RigfileCmd string
	CheckOnly  bool
}

// CodexDirFor resolves the Codex home: $CODEX_HOME, else ~/.codex.
func CodexDirFor(pi *platform.Info, getenv func(string) string) (string, error) {
	if d := getenv("CODEX_HOME"); d != "" {
		return d, nil
	}
	h, err := pi.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".codex"), nil
}

// SkillsDir is where Codex loads user skills: $HOME/.agents/skills (not under CODEX_HOME).
func SkillsDir(pi *platform.Info) (string, error) {
	h, err := pi.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".agents", "skills"), nil
}

// Build computes the Codex plan. It reads the machine and changes nothing.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil || env.CodexDir == "" {
		return nil, errors.New("codex: Env needs Plat and CodexDir")
	}
	b := common.New(common.Env{Plat: env.Plat, State: env.State, Overwrite: env.Overwrite, CheckOnly: env.CheckOnly}, "Codex")
	b.Plan.Skipped = p.Skipped
	skills, err := SkillsDir(env.Plat)
	if err != nil {
		return nil, err
	}
	a := &adapter{b: b, env: env, skills: skills}
	a.instructions(p)
	a.skills_(p)
	a.agents(p)
	a.commands(p)
	a.mcp(p)
	a.unsupported(p)
	return b.Result()
}

type adapter struct {
	b      *common.Builder
	env    Env
	skills string
}

// ---- instructions -----------------------------------------------------------------------------------

const projectDocMax = 32 << 10 // project_doc_max_bytes default (docs/targets/codex.md §3)

func (a *adapter) instructions(p *merge.Projection) {
	user := filepath.Join(a.env.CodexDir, "AGENTS.md")
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
			dest = filepath.Join(a.env.ProjectDir, "AGENTS.md")
		}
		add(dest, common.Region{ID: it.V.ID, Key: it.V.ID, Body: body, Layer: it.Layer})
	}
	// files that hold sections from an earlier apply but no longer receive any still need cleaning
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
		if dest == user && len(groups[dest]) > 0 {
			a.checkAgentsFile(dest)
		}
	}
}

func (a *adapter) checkAgentsFile(agents string) {
	if ov, err := os.ReadFile(filepath.Join(filepath.Dir(agents), "AGENTS.override.md")); err == nil && len(bytes.TrimSpace(ov)) > 0 {
		a.b.Note("%s exists and is not empty: Codex reads it INSTEAD of AGENTS.md, so the Rigfile section will not be seen until it is removed", a.b.Short(filepath.Join(filepath.Dir(agents), "AGENTS.override.md")))
	}
	if st, err := os.Stat(agents); err == nil && st.Size() > projectDocMax {
		a.b.Note("%s is %d bytes; Codex's project_doc_max_bytes defaults to %d, so the end of it may be dropped", a.b.Short(agents), st.Size(), projectDocMax)
	}
}

// ---- skills, agents, commands ---------------------------------------------------------------------

func (a *adapter) skills_(p *merge.Projection) {
	for _, s := range p.Skills {
		key := s.V.Key()
		if s.V.Path == "" {
			a.b.Note("skill %q is an external reference (%s): fetching arrives with the registry/git sources in Stage 4; skipped", key, s.V.Ref)
			continue
		}
		src, err := common.SrcPath(s.Dir, s.V.Path)
		if err != nil {
			a.b.Fail(fmt.Errorf("skill %s: %w", key, err))
			return
		}
		entries, err := hashing.TreeEntries(src)
		if err != nil {
			a.b.Fail(fmt.Errorf("skill %s: %w", key, err))
			return
		}
		var files []common.TreeFile
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(e.Path)))
			if err != nil {
				a.b.Fail(err)
				return
			}
			files = append(files, common.TreeFile{Path: e.Path, Data: data, Exec: e.Exec})
		}
		a.b.TreeOp("skill", key, filepath.Join(a.skills, key), files, s.Layer)
	}
}

type agentTOML struct {
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	DeveloperInstructions string `toml:"developer_instructions"`
	Model                 string `toml:"model,omitempty"`
}

func (a *adapter) agents(p *merge.Projection) {
	for _, ag := range p.Agents {
		key := ag.V.Key()
		src, err := common.SrcPath(ag.Dir, ag.V.Path)
		if err != nil {
			a.b.Fail(fmt.Errorf("agent %s: %w", key, err))
			return
		}
		data, err := os.ReadFile(src)
		if err != nil {
			a.b.Fail(err)
			return
		}
		fm, body := common.Frontmatter(data)
		out := agentTOML{Name: firstNonEmpty(common.String(fm, "name"), key), Description: common.String(fm, "description"), DeveloperInstructions: strings.TrimSpace(body) + "\n"}
		if out.Description == "" {
			a.b.Note("agent %q has no description; Codex requires one, so it was skipped", key)
			continue
		}
		if m := common.String(fm, "model"); m != "" {
			a.b.Note("agent %q pins model %q: Claude model aliases do not exist in Codex, so the model was not copied", key, m)
		}
		if _, has := fm["tools"]; has {
			a.b.Note("agent %q restricts tools: Codex has no per-agent tool allowlist (only sandbox_mode and per-server tool filters); the restriction was NOT carried over", key)
		}
		enc, err := toml.Marshal(out)
		if err != nil {
			a.b.Fail(err)
			return
		}
		a.b.FileOp("agent", key, filepath.Join(a.env.CodexDir, "agents", key+".toml"), enc, 0o644, ag.Layer, false)
	}
}

func (a *adapter) commands(p *merge.Projection) {
	skillKeys := map[string]bool{}
	for _, s := range p.Skills {
		skillKeys[s.V.Key()] = true
	}
	for _, c := range p.Commands {
		key := c.V.Key()
		if skillKeys[key] {
			a.b.Note("command %q has the same name as a skill; only the skill was written for Codex", key)
			continue
		}
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
		desc := firstNonEmpty(common.String(fm, "description"), "Command "+key+" (from a Rigfile rig)")
		skill := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", key, yamlScalar(desc), strings.TrimSpace(body))
		a.b.TreeOp("command", key, filepath.Join(a.skills, key), []common.TreeFile{{Path: "SKILL.md", Data: []byte(skill)}}, c.Layer)
	}
}

func yamlScalar(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`") || s == "" {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// ---- MCP servers ---------------------------------------------------------------------------------------

type mcpTOML struct {
	Command     string            `toml:"command,omitempty"`
	Args        []string          `toml:"args,omitempty"`
	Env         map[string]string `toml:"env,omitempty"`
	URL         string            `toml:"url,omitempty"`
	HTTPHeaders map[string]string `toml:"http_headers,omitempty"`
}

func (a *adapter) mcp(p *merge.Projection) {
	var regions []common.Region
	for _, s := range p.MCPServers {
		entry := mcpTOML{}
		if s.P.V.IsRemote() {
			if why := common.RemoteUnsupported(s.P.V); why != "" {
				a.b.Note("MCP server %q not installed for Codex: %s", s.Name, why)
				continue
			}
			entry.URL, entry.HTTPHeaders = s.P.V.URL, s.P.V.Headers
		} else {
			if s.P.V.CWD != "" {
				a.b.Note("MCP server %q not installed for Codex: cwd is not carried over yet", s.Name)
				continue
			}
			entry.Command, entry.Args = common.ExecWrap(a.env.RigfileCmd, s.P.V)
		}
		body, err := mcpTable(s.Name, entry)
		if err != nil {
			a.b.Fail(err)
			return
		}
		regions = append(regions, common.Region{ID: "mcp-" + s.Name, Key: s.Name, Body: body, Layer: s.P.Layer})
	}
	dest := filepath.Join(a.env.CodexDir, "config.toml")
	// only regions this adapter owns are touched: state items for MCP live under category "mcp"
	a.regionsFor("mcp", dest, regions)
}

// mcpTable renders one server as `[mcp_servers.<name>]` (plus sub-tables) WITHOUT a bare `[mcp_servers]` header:
// every region defines its own dotted tables, so several regions in one file never define a table twice.
func mcpTable(name string, e mcpTOML) ([]byte, error) {
	raw, err := toml.Marshal(map[string]mcpTOML{name: e})
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "[") && !strings.HasPrefix(l, "[[") {
			lines[i] = "[mcp_servers." + l[1:]
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// regionsFor is RegionSet restricted to the state items of one category (config.toml holds only MCP regions today,
// but later milestones add settings regions to the same file).
func (a *adapter) regionsFor(category, dest string, regions []common.Region) {
	var scoped *state.TargetState
	if a.env.State != nil {
		scoped = &state.TargetState{}
		for _, it := range a.env.State.Items {
			if it.Category == category || it.Path != dest {
				scoped.Items = append(scoped.Items, it)
			}
		}
	}
	saved := a.b.Env.State
	a.b.Env.State = scoped
	a.b.RegionSet(category, dest, splice.Hash, "hash", true, regions)
	a.b.Env.State = saved
}

// ---- what Codex cannot take -------------------------------------------------------------------------------

func (a *adapter) unsupported(p *merge.Projection) {
	if n := len(p.Hooks); n > 0 {
		a.b.Note("%d hook(s) not installed for Codex: Codex only runs hooks the user has reviewed and trusted (pinned by hash), and Rigfile never approves them for you", n)
	}
	if n := len(p.Deny) + len(p.Ask) + len(p.Allow); n > 0 {
		a.b.Note("%d permission rule(s) not written for Codex: it has sandbox_mode and approval_policy rather than per-rule deny/ask lists (see docs/targets/codex.md §8)", n)
	}
}
