package common

import (
	"encoding/json"
	"sort"

	"github.com/rigfile/rigfile/internal/capture"
)

// CaptureMCPJSON captures the `mcpServers` object of a JSON config file (Cursor, Claude Desktop). Members with a
// url are remote servers, the rest stdio.
func CaptureMCPJSON(b *capture.Builder, data []byte, file string) {
	var doc struct {
		MCP map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		b.Add("skipped", "mcp", file, "is not valid JSON; MCP servers were not captured")
		return
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
		case s.URL != "":
			b.MCPHTTP(n, s.URL, s.Headers)
		default:
			b.MCPStdio(n, s.Command, s.Args, s.Env)
		}
	}
}
