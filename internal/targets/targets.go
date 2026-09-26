// Package targets is the registry of things Rigfile can configure (RIGFILE_PLAN.md §9.1): Claude Code, Codex,
// Cursor, Gemini CLI and Claude Desktop. Each target knows how to detect itself, whether it exists on this OS,
// and how to turn a merged, projected rig into a plan; the session runs one plan per selected target through
// the same journaled writer, state file and lockfile.
package targets

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// Ctx is everything a target needs to detect itself and build a plan. Zero values mean "the real machine".
type Ctx struct {
	Plat       *platform.Info
	Getenv     func(string) string
	ProjectDir string
	State      *state.TargetState // what a previous apply recorded for THIS target
	Overwrite  bool
	BaseSecure bool
	Sandbox    bool
	Dir        string               // config directory override for this target ("" = default)
	MCP        claudecode.MCPClient // Claude Code only: the vendor CLI seam
	Have       func(string) bool    // is this command on PATH? nil = exec.LookPath
	Rigfile    string               // rigfile executable the generated entries call ("" = "rigfile")
}

// Detection says whether a tool appears to be installed.
type Detection struct {
	Installed bool
	Why       string
}

// Target is one configurable tool.
type Target struct {
	Name, Title string
	// Available reports whether the tool exists on this OS at all (Claude Desktop has no Linux build).
	Available func(*platform.Info) (bool, string)
	Detect    func(Ctx) Detection
	Plan      func(Ctx, *merge.Projection) (*engine.Plan, error)
	// Always is true for the target that is configured even when it is not detected (Claude Code: a fresh
	// machine gets its config before the tool is installed).
	Always bool
}

var registry = []Target{claudeCode()}

// Register adds a target (adapters call it from init in their own packages; tests add fakes).
func Register(t Target) { registry = append(registry, t) }

// Reset restores the registry to exactly the given targets (tests).
func Reset(ts ...Target) func() {
	old := registry
	registry = append([]Target(nil), ts...)
	return func() { registry = old }
}

// All returns the registered targets in a stable order (Claude Code first).
func All() []Target {
	out := append([]Target(nil), registry...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name == "claude-code" || out[j].Name == "claude-code" {
			return out[i].Name == "claude-code" && out[j].Name != "claude-code"
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get finds a target by name.
func Get(name string) (Target, bool) {
	for _, t := range registry {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// Names lists registered target names.
func Names() []string {
	var out []string
	for _, t := range All() {
		out = append(out, t.Name)
	}
	return out
}

func claudeCode() Target {
	return Target{
		Name: "claude-code", Title: "Claude Code", Always: true,
		Available: func(*platform.Info) (bool, string) { return true, "" },
		Detect: func(c Ctx) Detection {
			dir, err := claudeDir(c)
			if err == nil && exists(dir) {
				return Detection{true, dir + " exists"}
			}
			if have(c, "claude") {
				return Detection{true, "`claude` is on PATH"}
			}
			return Detection{false, "no ~/.claude and no `claude` on PATH"}
		},
		Plan: func(c Ctx, proj *merge.Projection) (*engine.Plan, error) {
			dir, err := claudeDir(c)
			if err != nil {
				return nil, err
			}
			return claudecode.Build(claudecode.Env{Plat: c.Plat, ClaudeDir: dir, ProjectDir: c.ProjectDir, State: c.State, MCP: c.MCP,
				Overwrite: c.Overwrite, BaseSecure: c.BaseSecure, Sandbox: c.Sandbox, Have: c.Have, RigfileCmd: c.Rigfile}, proj)
		},
	}
}

func claudeDir(c Ctx) (string, error) {
	if c.Dir != "" {
		return c.Dir, nil
	}
	return claudecode.ClaudeDirFor(c.Plat, c.Getenv)
}

// ---- helpers shared by adapters ---------------------------------------------------------------------

func have(c Ctx, cmd string) bool {
	if c.Have != nil {
		return c.Have(cmd)
	}
	return lookPath(cmd)
}

// ---- capabilities (data) ------------------------------------------------------------------------------

//go:embed capabilities/*.yaml
var capFS embed.FS

// Capability is one cell of the support matrix.
type Capability struct {
	Support string `yaml:"support"` // yes | partial | no
	Note    string `yaml:"note"`
}

// Capabilities describes what a target supports, per category and OS (`capabilities/<target>.yaml`).
type Capabilities struct {
	Target      string                `yaml:"target"`
	Title       string                `yaml:"title"`
	OS          []string              `yaml:"os"`
	ConfigPaths map[string]string     `yaml:"config_paths"`
	Categories  map[string]Capability `yaml:"categories"`
	BaseSecure  string                `yaml:"base_secure"` // enforced | partial | instructions_only | none
}

// CategoryOrder is the row order of the matrix.
var CategoryOrder = []string{"instructions", "skills", "agents", "commands", "mcp_servers", "hooks", "permissions"}

// LoadCapabilities reads every embedded capabilities file.
func LoadCapabilities() (map[string]Capabilities, error) {
	out := map[string]Capabilities{}
	err := fs.WalkDir(capFS, "capabilities", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := capFS.ReadFile(p)
		if err != nil {
			return err
		}
		var c Capabilities
		if err := yaml.Unmarshal(b, &c); err != nil {
			return fmt.Errorf("targets: %s: %w", filepath.Base(p), err)
		}
		if c.Target == "" || c.Target != strings.TrimSuffix(filepath.Base(p), ".yaml") {
			return fmt.Errorf("targets: %s: target %q does not match the file name", filepath.Base(p), c.Target)
		}
		out[c.Target] = c
		return nil
	})
	return out, err
}

// Matrix renders the support matrix as Markdown (docs/targets/matrix.md is generated from it).
func Matrix() (string, error) {
	caps, err := LoadCapabilities()
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(caps))
	for n := range caps {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "claude-code" || names[j] == "claude-code" {
			return names[i] == "claude-code"
		}
		return names[i] < names[j]
	})
	var b strings.Builder
	b.WriteString("# Target support matrix (generated)\n\nGenerated from `internal/targets/capabilities/*.yaml` by `go test ./internal/targets -update`; do not edit by hand. ✔ = supported, ◐ = partial (see note), ✘ = not supported (the plan screen says so; nothing is dropped silently).\n\n| Category |")
	for _, n := range names {
		b.WriteString(" " + caps[n].Title + " |")
	}
	b.WriteString("\n|---|")
	for range names {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	mark := map[string]string{"yes": "✔", "partial": "◐", "no": "✘"}
	for _, cat := range CategoryOrder {
		b.WriteString("| " + strings.ReplaceAll(cat, "_", " ") + " |")
		for _, n := range names {
			c := caps[n].Categories[cat]
			b.WriteString(" " + mark[c.Support] + " " + c.Note + " |")
		}
		b.WriteString("\n")
	}
	b.WriteString("| runs on |")
	for _, n := range names {
		b.WriteString(" " + strings.Join(caps[n].OS, ", ") + " |")
	}
	b.WriteString("\n| config dir |")
	for _, n := range names {
		var ps []string
		for _, o := range []string{"macos", "linux", "windows"} {
			if p, ok := caps[n].ConfigPaths[o]; ok {
				ps = append(ps, o+": `"+p+"`")
			}
		}
		b.WriteString(" " + strings.Join(ps, "<br>") + " |")
	}
	b.WriteString("\n| base-secure |")
	label := map[string]string{"enforced": "enforced (permissions + hooks)", "partial": "partly enforced, rest instructions only", "instructions_only": "instructions only, not enforced", "none": "not applicable"}
	for _, n := range names {
		b.WriteString(" " + label[caps[n].BaseSecure] + " |")
	}
	b.WriteString("\n")
	return b.String(), nil
}
