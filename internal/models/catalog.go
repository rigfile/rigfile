// Package models chooses, validates and describes the local model a rig asks for (RIGFILE_PLAN.md §9.5, docs/models.md):
// which variant fits this machine, whether it is safe to apply, what it will download, and how it is served and wired.
package models

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/digitaldreamer3462/rigfile/catalog"
)

// Variant is one hardware-specific choice, from a rig's `models:` entry or from the catalog. The two share the fields a
// decision needs; the catalog adds research facts (publisher, license, sizes, how to serve, what each agent can use).
type Variant struct {
	ID            string         `yaml:"id"`
	Status        string         `yaml:"status"` // catalog: verified | unverified | stub
	When          map[string]any `yaml:"when"`
	Engine        string         `yaml:"engine"`
	EngineVersion string         `yaml:"engine_version"`
	Model         string         `yaml:"model"`
	Revision      string         `yaml:"revision"`
	Digest        string         `yaml:"digest"` // Ollama: the model id (first 12 hex) a pull is compared against
	WeightsFormat string         `yaml:"weights_format"`
	Publisher     string         `yaml:"publisher"`
	License       string         `yaml:"license"`
	Args          map[string]any `yaml:"args"`
	Download      struct {
		Bytes    int64   `yaml:"bytes"`
		ApproxGB float64 `yaml:"approx_gb"`
	} `yaml:"download"`
	Serve struct {
		Host          string `yaml:"host"`
		Port          int    `yaml:"port"`
		API           any    `yaml:"api"` // a string, or a list of the APIs the engine offers
		CommandPinned struct {
			Exe  string   `yaml:"exe"`
			Args []string `yaml:"args"`
		} `yaml:"command_pinned"`
	} `yaml:"serve"`
	Wiring     map[string]map[string]any `yaml:"wiring"`
	MemoryNote string                    `yaml:"memory_note"`

	// Source says where the variant came from: "rig" or "catalog:<role>".
	Source string `yaml:"-"`
}

// Role is one catalog role.
type Role struct {
	Description string    `yaml:"description"`
	Purpose     []string  `yaml:"purpose"`
	Variants    []Variant `yaml:"variants"`
}

// Catalog is catalog/models.yaml.
type Catalog struct {
	Checked string          `yaml:"checked"`
	Roles   map[string]Role `yaml:"roles"`
}

// LoadCatalog parses the embedded catalog.
func LoadCatalog() (*Catalog, error) { return ParseCatalog(catalog.ModelsYAML) }

// ParseCatalog parses catalog bytes (tests use their own).
func ParseCatalog(b []byte) (*Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("models catalog: %w", err)
	}
	for name, r := range c.Roles {
		for i := range r.Variants {
			r.Variants[i].Source = "catalog:" + name
		}
		c.Roles[name] = r
	}
	return &c, nil
}

// placeholder reports a value the catalog marks as "not researched yet".
func placeholder(s string) bool { return strings.HasPrefix(s, "TODO") }

// APIs lists the APIs a variant's server offers ("openai-chat", "openai-responses", "anthropic-messages").
func (v Variant) APIs() []string {
	switch a := v.Serve.API.(type) {
	case string:
		return []string{a}
	case []any:
		var out []string
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Offers reports whether the engine serves the API.
func (v Variant) Offers(api string) bool {
	for _, a := range v.APIs() {
		if a == api {
			return true
		}
	}
	return false
}
