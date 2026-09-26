package codex

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/common"
	"github.com/digitaldreamer3462/rigfile/internal/capture"
	"github.com/digitaldreamer3462/rigfile/internal/splice"
)

// Capture reads a Codex setup (read-only) into a rig: AGENTS.md, skills, agents and MCP servers. Commands do not
// exist as such (they were written as skills). The reverse of Build for the supported fields.
func Capture(o capture.Options, skillsDir string) (*capture.Result, error) {
	b := capture.New(o)
	// instructions
	if data, ok, err := common.ReadOptional(filepath.Join(o.Dir, "AGENTS.md")); err != nil {
		return nil, err
	} else if ok {
		common.CaptureMarkdownRegions(b, data, "AGENTS.md", "agents-md")
	}
	// skills
	if ents, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range ents {
			name := e.Name()
			switch {
			case strings.HasPrefix(name, "."):
			case b.Skipped("skill", name):
			case e.Type()&os.ModeSymlink != 0:
				b.Add("skipped", "skill", name, "is a symlink; symlinks are never followed")
			case !e.IsDir():
			default:
				b.Skill(name, filepath.Join(skillsDir, name))
			}
		}
	}
	// agents
	if ents, err := os.ReadDir(filepath.Join(o.Dir, "agents")); err == nil {
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".toml") || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			key := strings.TrimSuffix(e.Name(), ".toml")
			if b.Skipped("agent", key) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(o.Dir, "agents", e.Name()))
			if err != nil {
				return nil, err
			}
			var a agentTOML
			if err := toml.Unmarshal(data, &a); err != nil || a.Name == "" {
				b.Add("skipped", "agent", key, "not a valid Codex agent file")
				continue
			}
			b.Markdown("agent", key, [][2]string{{"name", a.Name}, {"description", a.Description}}, a.DeveloperInstructions)
		}
	}
	// MCP servers
	if data, ok, err := common.ReadOptional(filepath.Join(o.Dir, "config.toml")); err != nil {
		return nil, err
	} else if ok {
		if !o.IncludeManaged {
			if body, _, err := splice.StripAll(data, splice.Hash); err == nil {
				data = body
			}
		}
		var doc struct {
			MCP map[string]struct {
				Command     string            `toml:"command"`
				Args        []string          `toml:"args"`
				Env         map[string]string `toml:"env"`
				URL         string            `toml:"url"`
				HTTPHeaders map[string]string `toml:"http_headers"`
			} `toml:"mcp_servers"`
		}
		if err := toml.Unmarshal(data, &doc); err != nil {
			b.Add("skipped", "mcp", "config.toml", "is not valid TOML; MCP servers were not captured")
		}
		names := make([]string, 0, len(doc.MCP))
		for n := range doc.MCP {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s := doc.MCP[n]
			if b.Skipped("mcp", n) {
				continue
			}
			if s.URL != "" {
				b.MCPHTTP(n, s.URL, s.HTTPHeaders)
			} else {
				b.MCPStdio(n, s.Command, s.Args, s.Env)
			}
		}
	}
	return b.Finish()
}
