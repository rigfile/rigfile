// Package tools plans and runs installation of the command-line tools a rig needs (`tools:` block),
// using the catalog in catalog/tools.yaml. Policy (RIGFILE_PLAN.md §9.4, §8.2):
//   - nothing runs before the caller has the user's approval;
//   - no implicit sudo/UAC: managers that need root are PRINTED as commands, never executed;
//   - no `curl | sh`; unpinned language-package installs are flagged.
package tools

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/digitaldreamer3462/rigfile/catalog"
)

// Catalog is catalog/tools.yaml.
type Catalog struct {
	Tools map[string]Entry `yaml:"tools"`
}

// Entry is one logical tool.
type Entry struct {
	Description string              `yaml:"description"`
	Detect      Detect              `yaml:"detect"`
	Install     map[string][]Choice `yaml:"install"` // macos | linux | windows
	Notes       string              `yaml:"notes"`
}

// Detect says how to tell the tool is already installed.
type Detect struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

// Choice is one way to install a tool on an OS.
type Choice struct {
	Manager   string `yaml:"manager"`
	Package   string `yaml:"package"`
	Channel   string `yaml:"channel"`
	Status    string `yaml:"status"` // verified | unverified
	RepoSetup string `yaml:"repo_setup"`
	Admin     bool   `yaml:"needs_admin"`
	Notes     string `yaml:"notes"`
}

// LoadCatalog parses the embedded catalog.
func LoadCatalog() (*Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(catalog.ToolsYAML, &c); err != nil {
		return nil, fmt.Errorf("tools: catalog: %w", err)
	}
	return &c, nil
}
