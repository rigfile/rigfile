package manifest

import (
	"fmt"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"
)

// This file is the Go mirror of schema/rigfile.v1.json. The schema is the contract; these types are
// what the rest of the program consumes after a manifest has been validated (Load does both).

// Manifest is a parsed rigfile.yaml.
type Manifest struct {
	APIVersion  string `yaml:"apiVersion"`
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	License     string `yaml:"license"`

	From    []string  `yaml:"from"`
	Targets TargetSel `yaml:"targets"`

	Instructions []Instruction         `yaml:"instructions"`
	Skills       []Skill               `yaml:"skills"`
	Agents       []Agent               `yaml:"agents"`
	Commands     []Command             `yaml:"commands"`
	MCPServers   map[string]MCPServer  `yaml:"mcp_servers"`
	Hooks        []Hook                `yaml:"hooks"`
	Permissions  Permissions           `yaml:"permissions"`
	Tools        Tools                 `yaml:"tools"`
	Secrets      map[string]SecretDecl `yaml:"secrets"`
	Logins       []Login               `yaml:"logins"`
	Models       map[string]Model      `yaml:"models"`
	Gateways     map[string]Gateway    `yaml:"gateways"`
	Routing      Routing               `yaml:"routing"`
	Overrides    map[string]Override   `yaml:"overrides"`
	Private      []string              `yaml:"private"`
}

// TargetSel is the `targets:` block.
type TargetSel struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

// ItemMeta is what every restrictable item shares.
type ItemMeta struct {
	ID      string   `yaml:"id"`
	OS      []string `yaml:"os"`
	Targets []string `yaml:"targets"`
}

// AppliesTo reports whether the item is enabled for this OS and target (AND semantics, omitted = all).
func (m ItemMeta) AppliesTo(osName, target string) bool {
	return listAllows(m.OS, osName) && listAllows(m.Targets, target)
}

func listAllows(list []string, v string) bool {
	if len(list) == 0 || v == "" {
		return true
	}
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Instruction is a Markdown section to install (docs/merge-semantics.md §4.1).
type Instruction struct {
	ItemMeta `yaml:",inline"`
	File     string `yaml:"file"`
	Scope    string `yaml:"scope"` // user | project (default user)
	Merge    string `yaml:"merge"`
}

// Key is the merge identity: scope and id are separate namespaces.
func (i Instruction) Key() string { return i.EffectiveScope() + "/" + i.ID }

// EffectiveScope applies the default.
func (i Instruction) EffectiveScope() string {
	if i.Scope == "" {
		return "user"
	}
	return i.Scope
}

// Skill is a SKILL.md folder inside the rig (Path) or an external pinned skill (Ref).
type Skill struct {
	ItemMeta `yaml:",inline"`
	Path     string `yaml:"path"`
	Ref      string `yaml:"ref"`
}

// Key is the merge identity (docs/merge-semantics.md §2.4): explicit id, else the folder name, else
// the last segment of the external reference.
func (s Skill) Key() string {
	switch {
	case s.ID != "":
		return s.ID
	case s.Path != "":
		return path.Base(strings.TrimRight(s.Path, "/"))
	default:
		ref := s.Ref
		if i := strings.LastIndex(ref, "@"); i > 0 {
			ref = ref[:i]
		}
		return path.Base(ref)
	}
}

// Agent is a subagent definition file.
type Agent struct {
	ItemMeta `yaml:",inline"`
	Path     string `yaml:"path"`
}

// Key is the id, else the file name without extension.
func (a Agent) Key() string { return fileKey(a.ID, a.Path) }

// Command is a slash-command definition file.
type Command struct {
	ItemMeta `yaml:",inline"`
	Path     string `yaml:"path"`
}

// Key is the id, else the file name without extension.
func (c Command) Key() string { return fileKey(c.ID, c.Path) }

func fileKey(id, p string) string {
	if id != "" {
		return id
	}
	b := path.Base(p)
	return strings.TrimSuffix(b, path.Ext(b))
}

// MCPServer is a stdio or remote (http) server entry. Env values are literals or secret://ref.
type MCPServer struct {
	OS          []string          `yaml:"os"`
	Targets     []string          `yaml:"targets"`
	Transport   string            `yaml:"transport"`
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	Env         map[string]string `yaml:"env"`
	CWD         string            `yaml:"cwd"`
	Network     Network           `yaml:"network"`
	URL         string            `yaml:"url"`
	Auth        string            `yaml:"auth"`
	BearerToken string            `yaml:"bearer_token"`
	Headers     map[string]string `yaml:"headers"`
}

// Meta lets an MCP server use the same os/targets filtering as other items.
func (s MCPServer) Meta() ItemMeta { return ItemMeta{OS: s.OS, Targets: s.Targets} }

// IsRemote reports whether this is an http server.
func (s MCPServer) IsRemote() bool { return s.Transport == "http" }

// Network is a server's declared egress.
type Network struct {
	Allow []string `yaml:"allow"`
}

// SecretRef extracts the ref path from a "secret://a/b" value.
func SecretRef(v string) (ref string, ok bool) {
	const p = "secret://"
	if strings.HasPrefix(v, p) {
		return strings.TrimPrefix(v, p), true
	}
	return "", false
}

// HookRun is `run:`: a single builtin/script string, or a per-OS map.
type HookRun struct {
	Single string
	PerOS  map[string]string
}

// UnmarshalYAML accepts a scalar or a mapping.
func (h *HookRun) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Decode(&h.Single)
	case yaml.MappingNode:
		return n.Decode(&h.PerOS)
	}
	return fmt.Errorf("hook run: want a string or a per-OS mapping (line %d)", n.Line)
}

// For returns the command for an OS ("" if the hook has no version for it).
func (h HookRun) For(osName string) string {
	if h.Single != "" {
		return h.Single
	}
	return h.PerOS[osName]
}

// Builtin returns the built-in name if the run is `builtin:<name>`.
func Builtin(run string) (name string, ok bool) {
	const p = "builtin:"
	if strings.HasPrefix(run, p) {
		return strings.TrimPrefix(run, p), true
	}
	return "", false
}

// HookMatch narrows when a hook fires.
type HookMatch struct {
	Tool    string `yaml:"tool"`
	Command string `yaml:"command"`
	Path    string `yaml:"path"`
}

// Hook is a lifecycle hook.
type Hook struct {
	ItemMeta       `yaml:",inline"`
	Event          string    `yaml:"event"`
	Match          HookMatch `yaml:"match"`
	Run            HookRun   `yaml:"run"`
	TimeoutSeconds int       `yaml:"timeout_seconds"`
}

// Key is the id (required by the schema).
func (h Hook) Key() string { return h.ID }

// Tools is the `tools:` block.
type Tools struct {
	Common  []string     `yaml:"common"`
	NPM     []string     `yaml:"npm"`
	Pipx    []string     `yaml:"pipx"`
	UV      []string     `yaml:"uv"`
	Cargo   []string     `yaml:"cargo"`
	Go      []string     `yaml:"go"`
	MacOS   ToolsMacOS   `yaml:"macos"`
	Linux   ToolsLinux   `yaml:"linux"`
	Windows ToolsWindows `yaml:"windows"`
}

// ToolsMacOS is the macOS escape hatch.
type ToolsMacOS struct {
	Brew []string `yaml:"brew"`
	Cask []string `yaml:"cask"`
}

// ToolsLinux is the Linux escape hatch.
type ToolsLinux struct {
	Apt    []string `yaml:"apt"`
	Dnf    []string `yaml:"dnf"`
	Pacman []string `yaml:"pacman"`
	Zypper []string `yaml:"zypper"`
	Brew   []string `yaml:"brew"`
}

// ToolsWindows is the Windows escape hatch.
type ToolsWindows struct {
	Winget []string `yaml:"winget"`
	Scoop  []string `yaml:"scoop"`
	Choco  []string `yaml:"choco"`
}

// SecretDecl declares (never carries) a secret.
type SecretDecl struct {
	Description string   `yaml:"description"`
	ObtainURL   string   `yaml:"obtain_url"`
	Hosts       []string `yaml:"hosts"`
	Optional    bool     `yaml:"optional"`
}

// Login declares a login the rig needs.
type Login struct {
	Provider string   `yaml:"provider"`
	Reason   string   `yaml:"reason"`
	Method   string   `yaml:"method"`
	OS       []string `yaml:"os"`
}

// Model is a local model entry (Stage 3b consumes it; parsed now so rigs validate and merge).
type Model struct {
	Role       string         `yaml:"role"`
	Purpose    []string       `yaml:"purpose"`
	Serve      ModelServe     `yaml:"serve"`
	Variants   []ModelVariant `yaml:"variants"`
	LicenseAck string         `yaml:"license_ack"`
}

// ModelServe is how the model is served.
type ModelServe struct {
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	API       string `yaml:"api"`
	Autostart bool   `yaml:"autostart"`
}

// ModelVariant is one hardware-specific choice.
type ModelVariant struct {
	When          map[string]any `yaml:"when"`
	Engine        string         `yaml:"engine"`
	EngineVersion string         `yaml:"engine_version"`
	Model         string         `yaml:"model"`
	Revision      string         `yaml:"revision"`
	Digest        string         `yaml:"digest"`
	WeightsFormat string         `yaml:"weights_format"`
	Args          map[string]any `yaml:"args"`
}

// Gateway is a local protocol bridge.
type Gateway struct {
	Engine string            `yaml:"engine"`
	Expose string            `yaml:"expose"`
	Listen string            `yaml:"listen"`
	Routes map[string]string `yaml:"routes"`
}

// Routing is the local/cloud routing policy.
type Routing struct {
	Default         string   `yaml:"default"`
	LocalFor        []string `yaml:"local_for"`
	FallbackOnLimit string   `yaml:"fallback_on_limit"`
}

// Override is an `overrides.<target>` block.
type Override struct {
	Instructions []Instruction        `yaml:"instructions"`
	Skills       []Skill              `yaml:"skills"`
	Agents       []Agent              `yaml:"agents"`
	Commands     []Command            `yaml:"commands"`
	MCPServers   map[string]MCPServer `yaml:"mcp_servers"`
	Hooks        []Hook               `yaml:"hooks"`
	Permissions  Permissions          `yaml:"permissions"`
}
