package zed

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/rigfile/rigfile/internal/adapters/common"
	"github.com/rigfile/rigfile/internal/capture"
	"github.com/rigfile/rigfile/internal/jsonedit"
)

// Capture reads Zed's MCP servers (read-only) from the "context_servers" member of settings.json. Real Zed
// settings.json files are JSONC (comments, trailing commas: Zed ships one with extensive comments by default),
// so this masks them out first (jsonedit.Mask) rather than the plain encoding/json.Unmarshal that would fail
// on a real file. No verified rules/instructions location exists to read from (docs/targets/zed.md).
func Capture(o capture.Options) (*capture.Result, error) {
	b := capture.New(o)
	data, ok, err := common.ReadOptional(filepath.Join(o.Dir, "settings.json"))
	if err != nil {
		return nil, err
	}
	if ok {
		captureContextServers(b, data)
	}
	return b.Finish()
}

func captureContextServers(b *capture.Builder, data []byte) {
	masked, err := jsonedit.Mask(data)
	if err != nil {
		b.Add("skipped", "mcp", "settings.json", "could not be read (an unterminated string or block comment); MCP servers were not captured")
		return
	}
	var doc struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"context_servers"`
	}
	if err := json.Unmarshal(masked, &doc); err != nil {
		b.Add("skipped", "mcp", "settings.json", "is not valid JSON; MCP servers were not captured")
		return
	}
	names := make([]string, 0, len(doc.Servers))
	for n := range doc.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := doc.Servers[n]
		switch {
		case b.Skipped("mcp", n):
		case s.URL != "":
			b.MCPHTTP(n, s.URL, s.Headers)
		default:
			b.MCPStdio(n, s.Command, s.Args, s.Env)
		}
	}
}
