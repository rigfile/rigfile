package tools

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// Host is what the planner needs to know about the machine; tests inject a fake.
type Host interface {
	Has(command string) bool
	Run(ctx context.Context, argv []string, out io.Writer) error
}

// Step is one install action.
type Step struct {
	Tool    string // logical name or package, for display
	Manager string
	Package string
	Argv    []string
	Manual  bool   // printed, never run by Rigfile (needs root, or the OS is not supported yet)
	Why     string // why it is manual, or a note
	Warn    string // e.g. "unpinned"
	Verify  string // command that should exist afterwards ("" = none)
}

// Command renders the step as a shell line for display.
func (s Step) Command() string { return strings.Join(s.Argv, " ") }

// Plan is what apply would do about tools.
type Plan struct {
	Steps      []Step
	Present    []string // already installed
	Unresolved []string // could not be planned, with the reason
}

// Empty reports whether there is nothing to install or report.
func (p Plan) Empty() bool { return len(p.Steps) == 0 && len(p.Unresolved) == 0 }

// Runnable returns the steps Rigfile will execute after approval.
func (p Plan) Runnable() []Step {
	var out []Step
	for _, s := range p.Steps {
		if !s.Manual {
			out = append(out, s)
		}
	}
	return out
}

// executable managers and how to spell their install command.
var runners = map[string]func(pkg string) []string{
	"brew":  func(p string) []string { return []string{"brew", "install", p} },
	"cask":  func(p string) []string { return []string{"brew", "install", "--cask", p} },
	"npm":   func(p string) []string { return []string{"npm", "install", "-g", p} },
	"pipx":  func(p string) []string { return []string{"pipx", "install", p} },
	"uv":    func(p string) []string { return []string{"uv", "tool", "install", p} },
	"cargo": func(p string) []string { return []string{"cargo", "install", p} },
	"go":    func(p string) []string { return []string{"go", "install", p} },
}

// managers Rigfile prints but never runs: they need root (apt/dnf/pacman/zypper), or Windows (Stage 3).
var printed = map[string]func(pkg string) []string{
	"apt":    func(p string) []string { return []string{"sudo", "apt-get", "install", "-y", p} },
	"dnf":    func(p string) []string { return []string{"sudo", "dnf", "install", "-y", p} },
	"pacman": func(p string) []string { return []string{"sudo", "pacman", "-S", "--needed", p} },
	"zypper": func(p string) []string { return []string{"sudo", "zypper", "install", "-y", p} },
	"winget": func(p string) []string { return []string{"winget", "install", "--id", p} },
	"scoop":  func(p string) []string { return []string{"scoop", "install", p} },
	"choco":  func(p string) []string { return []string{"choco", "install", "-y", p} },
}

// binary that must exist for a manager to be usable at all.
var managerBinary = map[string]string{"apt": "apt-get", "cask": "brew"}

func binaryFor(manager string) string {
	if b, ok := managerBinary[manager]; ok {
		return b
	}
	return manager
}

var pinned = regexp.MustCompile(`@\d[^@/]*$|==\d|@v\d`)

// Input is the merged `tools:` block, keyed as in merge.Merged.Tools:
// common npm pipx uv cargo go macos.brew macos.cask linux.apt linux.dnf linux.pacman linux.zypper linux.brew.
type Input map[string][]string

// Build plans installation for one OS. Nothing is executed.
func Build(cat *Catalog, in Input, osName string, host Host) Plan {
	var p Plan
	seen := map[string]bool{}

	add := func(s Step) {
		k := s.Manager + "|" + s.Package
		if !seen[k] {
			seen[k] = true
			p.Steps = append(p.Steps, s)
		}
	}

	// 1. logical catalog tools
	for _, name := range in["common"] {
		e, ok := cat.Tools[name]
		if !ok {
			p.Unresolved = append(p.Unresolved, fmt.Sprintf("%s: not in the tool catalog; use an explicit package manager list in `tools:` instead", name))
			continue
		}
		if e.Detect.Command != "" && host.Has(e.Detect.Command) {
			p.Present = append(p.Present, name)
			continue
		}
		choice, reason := pickChoice(e.Install[osName], host)
		if choice == nil {
			p.Unresolved = append(p.Unresolved, fmt.Sprintf("%s: %s", name, reason))
			continue
		}
		s := stepFor(name, choice.Manager, choice.Package, e.Detect.Command)
		if choice.Notes != "" && choice.Status != "verified" {
			s.Warn = "catalog entry is unverified"
		}
		add(s)
	}

	// 2. explicit language-package managers (same on every OS)
	for _, g := range []string{"npm", "pipx", "uv", "cargo", "go"} {
		for _, pkg := range in[g] {
			s := stepFor(pkg, g, pkg, "")
			if g != "go" && g != "cargo" && !pinned.MatchString(pkg) {
				s.Warn = "unpinned: installs whatever is newest"
			}
			if !host.Has(binaryFor(g)) {
				s.Manual, s.Why = true, g+" is not installed"
			}
			add(s)
		}
	}

	// 3. per-OS escape hatches
	type esc struct{ key, manager string }
	for _, x := range []esc{{"macos.brew", "brew"}, {"macos.cask", "cask"}, {"linux.brew", "brew"}, {"linux.apt", "apt"}, {"linux.dnf", "dnf"}, {"linux.pacman", "pacman"}, {"linux.zypper", "zypper"},
		{"windows.winget", "winget"}, {"windows.scoop", "scoop"}, {"windows.choco", "choco"}} {
		if !strings.HasPrefix(x.key, osName+".") {
			continue
		}
		for _, pkg := range in[x.key] {
			s := stepFor(pkg, x.manager, pkg, "")
			if _, ok := runners[x.manager]; ok && !host.Has(binaryFor(x.manager)) {
				s.Manual, s.Why = true, x.manager+" is not installed"
			}
			add(s)
		}
	}

	return p
}

func stepFor(name, manager, pkg, verify string) Step {
	if f, ok := runners[manager]; ok {
		return Step{Tool: name, Manager: manager, Package: pkg, Argv: f(pkg), Verify: verify}
	}
	if f, ok := printed[manager]; ok {
		why := "needs root; Rigfile never runs sudo for you"
		if manager == "winget" || manager == "scoop" || manager == "choco" {
			why = "Windows support arrives in Stage 3; run it yourself"
		}
		return Step{Tool: name, Manager: manager, Package: pkg, Argv: f(pkg), Manual: true, Why: why, Verify: verify}
	}
	return Step{Tool: name, Manager: manager, Package: pkg, Argv: []string{manager, "install", pkg}, Manual: true, Why: "unknown package manager"}
}

// pickChoice chooses the first catalog entry whose manager exists on this machine, skipping entries
// that need an extra package repository. If none exists it explains what is missing.
func pickChoice(choices []Choice, host Host) (*Choice, string) {
	if len(choices) == 0 {
		return nil, "no install method in the catalog for this OS"
	}
	var missing []string
	for i := range choices {
		c := &choices[i]
		if c.RepoSetup != "" {
			continue
		}
		if host.Has(binaryFor(c.Manager)) {
			return c, ""
		}
		missing = append(missing, c.Manager)
	}
	if len(missing) == 0 {
		return nil, "only installable after adding a third-party package repository (not automated)"
	}
	return nil, "no supported package manager found (catalog knows: " + strings.Join(uniq(missing), ", ") + ")"
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Result of executing a plan.
type Result struct {
	Ran    []string
	Failed []string
}

// Execute runs every non-manual step in order, streaming the tool's output to out. It stops at the
// first failure. The caller must have the user's approval before calling it.
func Execute(ctx context.Context, p Plan, host Host, out io.Writer) Result {
	var r Result
	for _, s := range p.Runnable() {
		fmt.Fprintf(out, "$ %s\n", s.Command())
		if err := host.Run(ctx, s.Argv, out); err != nil {
			r.Failed = append(r.Failed, fmt.Sprintf("%s (%s): %v", s.Tool, s.Command(), err))
			return r
		}
		if s.Verify != "" && !host.Has(s.Verify) {
			r.Failed = append(r.Failed, fmt.Sprintf("%s: installed, but %q is still not on PATH (a new shell or a PATH change may be needed)", s.Tool, s.Verify))
			return r
		}
		r.Ran = append(r.Ran, s.Tool)
	}
	return r
}

// SystemHost runs real commands.
type SystemHost struct{}

// Has reports whether command is on PATH.
func (SystemHost) Has(command string) bool { _, err := exec.LookPath(command); return err == nil }

// Run executes argv with no shell.
func (SystemHost) Run(ctx context.Context, argv []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}
