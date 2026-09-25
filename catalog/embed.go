// Package catalog embeds the tool and model catalogs so the CLI needs nothing on disk.
package catalog

import _ "embed"

// ToolsYAML is catalog/tools.yaml.
//
//go:embed tools.yaml
var ToolsYAML []byte
