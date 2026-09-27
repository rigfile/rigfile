package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/rigfile/rigfile/internal/adapters/common"
	"github.com/rigfile/rigfile/internal/capture"
)

// Capture reads a Gemini CLI setup (read-only): GEMINI.md, custom commands and MCP servers.
func Capture(o capture.Options) (*capture.Result, error) {
	b := capture.New(o)
	if data, ok, err := common.ReadOptional(filepath.Join(o.Dir, "GEMINI.md")); err != nil {
		return nil, err
	} else if ok {
		common.CaptureMarkdownRegions(b, data, "GEMINI.md", "gemini-md")
	}
	if ents, err := os.ReadDir(filepath.Join(o.Dir, "commands")); err == nil {
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".toml") || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			key := strings.TrimSuffix(e.Name(), ".toml")
			if b.Skipped("command", key) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(o.Dir, "commands", e.Name()))
			if err != nil {
				return nil, err
			}
			var c commandTOML
			if err := toml.Unmarshal(data, &c); err != nil || c.Prompt == "" {
				b.Add("skipped", "command", key, "not a valid Gemini custom command")
				continue
			}
			var front [][2]string
			if c.Description != "" {
				front = append(front, [2]string{"description", c.Description})
			}
			b.Markdown("command", key, front, strings.ReplaceAll(c.Prompt, "{{args}}", "$ARGUMENTS"))
		}
	}
	if data, ok, err := common.ReadOptional(filepath.Join(o.Dir, "settings.json")); err != nil {
		return nil, err
	} else if ok {
		var doc struct {
			MCP map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
				URL     string            `json:"url"`
				HTTPURL string            `json:"httpUrl"`
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			b.Add("skipped", "mcp", "settings.json", "is not valid JSON; MCP servers were not captured")
		}
		names := make([]string, 0, len(doc.MCP))
		for n := range doc.MCP {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s := doc.MCP[n]
			switch {
			case b.Skipped("mcp", n):
			case s.HTTPURL != "":
				b.MCPHTTP(n, s.HTTPURL, s.Headers)
			case s.URL != "":
				b.Add("skipped", "mcp", n, "SSE (url) servers are not supported; only streamable HTTP (httpUrl) and stdio")
			default:
				b.MCPStdio(n, s.Command, s.Args, s.Env)
			}
		}
	}
	return b.Finish()
}
