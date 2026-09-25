package manifest

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// PermissionRule is one canonical permission rule (schema $defs/permissionRule). Exactly one of
// Read/Edit/Bash/WebFetch/MCP is set on a valid manifest.
type PermissionRule struct {
	Read     string   `yaml:"read"`
	Edit     string   `yaml:"edit"`
	Bash     string   `yaml:"bash"`
	WebFetch string   `yaml:"web_fetch"`
	MCP      string   `yaml:"mcp"`
	Reason   string   `yaml:"reason"`
	OS       []string `yaml:"os"`
	Targets  []string `yaml:"targets"`
}

// Kind returns which single field is set ("" if none or several).
func (r PermissionRule) Kind() (kind, value string) {
	n := 0
	for k, v := range map[string]string{"read": r.Read, "edit": r.Edit, "bash": r.Bash, "web_fetch": r.WebFetch, "mcp": r.MCP} {
		if v != "" {
			n++
			kind, value = k, v
		}
	}
	if n != 1 {
		return "", ""
	}
	return kind, value
}

// Permissions is the canonical permissions block (spike subset of the manifest).
type Permissions struct {
	Deny  []PermissionRule `yaml:"deny"`
	Ask   []PermissionRule `yaml:"ask"`
	Allow []PermissionRule `yaml:"allow"`
}

// ParsePermissions extracts only the `permissions:` block of a rigfile.yaml. The caller should have
// validated the document with Validator first; this does not re-validate.
func ParsePermissions(data []byte) (Permissions, error) {
	var doc struct {
		Permissions Permissions `yaml:"permissions"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Permissions{}, fmt.Errorf("parse permissions: %w", err)
	}
	return doc.Permissions, nil
}
