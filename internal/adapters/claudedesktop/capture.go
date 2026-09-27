package claudedesktop

import (
	"path/filepath"

	"github.com/rigfile/rigfile/internal/adapters/common"
	"github.com/rigfile/rigfile/internal/capture"
)

// Capture reads Claude Desktop's local MCP servers (read-only).
func Capture(o capture.Options) (*capture.Result, error) {
	b := capture.New(o)
	data, ok, err := common.ReadOptional(filepath.Join(o.Dir, ConfigFile))
	if err != nil {
		return nil, err
	}
	if ok {
		common.CaptureMCPJSON(b, data, ConfigFile)
	}
	return b.Finish()
}
