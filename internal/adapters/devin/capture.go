package devin

import (
	"path/filepath"

	"github.com/rigfile/rigfile/internal/adapters/common"
	"github.com/rigfile/rigfile/internal/capture"
)

// Capture reads Devin's MCP servers (read-only). No verified rules/instructions location exists to read from
// (docs/targets/devin.md).
func Capture(o capture.Options) (*capture.Result, error) {
	b := capture.New(o)
	data, ok, err := common.ReadOptional(filepath.Join(o.Dir, "mcp_config.json"))
	if err != nil {
		return nil, err
	}
	if ok {
		common.CaptureMCPJSON(b, data, "mcp_config.json")
	}
	return b.Finish()
}
