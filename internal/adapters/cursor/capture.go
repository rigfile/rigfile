package cursor

import (
	"path/filepath"

	"github.com/rigfile/rigfile/internal/adapters/common"
	"github.com/rigfile/rigfile/internal/capture"
)

// Capture reads Cursor's MCP servers (read-only). User rules live in the app and cannot be read from a file.
func Capture(o capture.Options) (*capture.Result, error) {
	b := capture.New(o)
	data, ok, err := common.ReadOptional(filepath.Join(o.Dir, "mcp.json"))
	if err != nil {
		return nil, err
	}
	if ok {
		common.CaptureMCPJSON(b, data, "mcp.json")
	}
	b.Add("note", "instruction", "user rules", "Cursor keeps User Rules inside the app; copy them into a file by hand if you want them in the rig")
	return b.Finish()
}
