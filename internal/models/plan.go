package models

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/platform"
)

// Default ports per engine (their own defaults; a rig may remap with `serve.port`).
const (
	OllamaPort = 11434
	MLXPort    = 8080
)

// Serve is how the chosen model is served.
type Serve struct {
	Host      string
	Port      int
	Autostart bool
}

// Wiring says how one agent can use the model, and how honest that claim is.
type Wiring struct {
	Target string // codex | claude-code | openai-chat clients | cursor
	Status string // supported | experimental | needs-bridge | unsupported
	How    string // the command to use, or the reason it cannot be used
}

// Plan is what the plan screen shows about one `models:` entry.
type Plan struct {
	Name       string
	Role       string
	Purpose    []string
	Chosen     *Variant
	Considered []Rejected
	Serve      Serve
	Blocked    string // why nothing can be applied ("" when Chosen is set)
	Warnings   []string
	Wiring     []Wiring

	Publisher     string
	License       string
	DownloadBytes int64
	FreeDiskGB    float64
	EnoughDisk    bool
}

// FromManifest converts a rig's explicit variants.
func FromManifest(vs []manifest.ModelVariant) []Variant {
	out := make([]Variant, 0, len(vs))
	for _, v := range vs {
		out = append(out, Variant{When: v.When, Engine: v.Engine, EngineVersion: v.EngineVersion, Model: v.Model, Revision: v.Revision, Digest: v.Digest,
			WeightsFormat: v.WeightsFormat, Args: v.Args, Source: "rig"})
	}
	return out
}

func publisherOf(v Variant) string {
	if v.Publisher != "" {
		return v.Publisher
	}
	if org, _, ok := strings.Cut(v.Model, "/"); ok {
		return org
	}
	if v.Engine == "ollama" {
		return "ollama.com/library"
	}
	return ""
}

// Resolve decides what one `models:` entry means on this machine. It never returns an error for "cannot be applied": that
// is a Plan with Blocked set, which the plan screen shows and apply refuses.
func Resolve(name string, m manifest.Model, cat *Catalog, hw platform.Hardware) *Plan {
	p := &Plan{Name: name, Role: m.Role, Purpose: m.Purpose, FreeDiskGB: hw.FreeDiskGB}
	var variants []Variant
	switch {
	case len(m.Variants) > 0:
		variants = FromManifest(m.Variants)
	case m.Role != "":
		r, ok := cat.Roles[m.Role]
		if !ok {
			p.Blocked = fmt.Sprintf("the catalog has no role %q", m.Role)
			return p
		}
		variants = r.Variants
		if len(p.Purpose) == 0 {
			p.Purpose = r.Purpose
		}
	default:
		p.Blocked = "the entry names neither a role nor variants"
		return p
	}
	p.Chosen, p.Considered = Choose(variants, hw)
	if p.Chosen == nil {
		p.Blocked = "no variant fits this machine and is safe to apply (see what was considered below)"
		return p
	}
	v := p.Chosen
	p.Serve = Serve{Host: firstNonEmpty(m.Serve.Host, v.Serve.Host, "127.0.0.1"), Port: firstInt(m.Serve.Port, v.Serve.Port, defaultPort(v.Engine)), Autostart: m.Serve.Autostart}
	if !IsLoopbackHost(p.Serve.Host) {
		p.Chosen, p.Blocked = nil, fmt.Sprintf("serve.host %s is not loopback: model servers listen on loopback only", p.Serve.Host)
		return p
	}
	p.Publisher = publisherOf(*v)
	p.License = firstNonEmpty(m.LicenseAck, v.License)
	p.DownloadBytes = v.Download.Bytes
	if p.DownloadBytes == 0 && v.Download.ApproxGB > 0 {
		p.DownloadBytes = int64(v.Download.ApproxGB * 1e9)
	}
	// weights need room for themselves plus some headroom
	p.EnoughDisk = hw.FreeDiskGB == 0 || float64(p.DownloadBytes)*1.1/float64(1<<30) <= hw.FreeDiskGB
	if !p.EnoughDisk {
		p.Warnings = append(p.Warnings, fmt.Sprintf("the download (%s) may not fit: %.0f GB free", HumanBytes(p.DownloadBytes), hw.FreeDiskGB))
	}
	switch v.Status {
	case "unverified":
		p.Warnings = append(p.Warnings, "this catalog entry is marked unverified")
	}
	if p.License == "" {
		p.Warnings = append(p.Warnings, "no license is stated for these weights: check the model card before you download")
	}
	p.Warnings = append(p.Warnings, "tool calling is not known until `rigfile doctor` runs a smoke test; until then treat the model as chat-only")
	p.Wiring = WiringFor(*v)
	return p
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstInt(ns ...int) int {
	for _, n := range ns {
		if n != 0 {
			return n
		}
	}
	return 0
}

func defaultPort(engine string) int {
	if engine == "ollama" {
		return OllamaPort
	}
	return MLXPort
}

// WiringFor states what each agent can do with this variant, from the Stage 0 research (docs/models.md §4). It only claims
// what a documented mechanism supports.
func WiringFor(v Variant) []Wiring {
	var out []Wiring
	if v.Engine == "ollama" {
		out = append(out,
			Wiring{"codex", "supported", "`rigfile models run codex` (codex --oss, Ollama's built-in provider)"},
			Wiring{"claude-code", "experimental", "`rigfile models run claude`: Ollama documents an Anthropic-compatible endpoint that names Claude Code as a client; Anthropic does NOT support routing Claude Code to non-Claude models"})
	} else {
		out = append(out,
			Wiring{"codex", "needs-bridge", "Codex speaks the Responses API and this server only offers chat completions; no bridge was verified, so it is not wired"},
			Wiring{"claude-code", "needs-bridge", "Claude Code needs an Anthropic Messages endpoint; this server has none and Anthropic does not support non-Claude models through a gateway, so it is not wired"})
	}
	if v.Engine == "ollama" || v.Offers("openai-chat") || v.Engine == "mlx-lm" {
		out = append(out, Wiring{"openai-chat clients", "supported", "`rigfile models url " + "<name>` prints the endpoint for Aider, OpenCode, Continue, Zed or your own scripts"})
	}
	out = append(out, Wiring{"cursor", "unsupported", "Cursor has limited local-model support (UNVERIFIED); Rigfile does not configure it"})
	return out
}

// HumanBytes formats a size for the plan screen.
func HumanBytes(n int64) string {
	switch {
	case n <= 0:
		return "size unknown"
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9) // decimal, like the Hugging Face and Ollama pages
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}

// ResolveAll plans every model of a merged rig, in name order.
func ResolveAll(ms map[string]manifest.Model, cat *Catalog, hw platform.Hardware) []*Plan {
	names := make([]string, 0, len(ms))
	for n := range ms {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*Plan, 0, len(names))
	for _, n := range names {
		out = append(out, Resolve(n, ms[n], cat, hw))
	}
	return out
}

// Render writes one plan as the MODELS section of the review screen.
func (p *Plan) Render(w interface{ Write([]byte) (int, error) }) {
	pr := func(f string, a ...any) { fmt.Fprintf(w, f+"\n", a...) }
	if p.Chosen == nil {
		pr("  ✘  %s   %s", p.Name, p.Blocked)
	} else {
		v := p.Chosen
		pr("  +  %s   %s via %s %s", p.Name, v.Model, v.Engine, v.EngineVersion)
		pr("       download %s   publisher %s   license %s", HumanBytes(p.DownloadBytes), orNone(p.Publisher), orNone(p.License))
		if v.Revision != "" {
			pr("       pinned to revision %s", v.Revision[:12])
		}
		pr("       served on %s:%d%s", p.Serve.Host, p.Serve.Port, map[bool]string{true: " (per-user service, starts at login)", false: " (started when you run it)"}[p.Serve.Autostart])
		if len(p.Purpose) > 0 {
			pr("       for: %s", strings.Join(p.Purpose, ", "))
		}
		for _, wi := range p.Wiring {
			pr("       %-12s %s: %s", wi.Status, wi.Target, wi.How)
		}
	}
	for _, r := range p.Considered {
		pr("       not chosen: %s: %s", r.Label, r.Reason)
	}
	for _, x := range p.Warnings {
		pr("       ⚠ %s", x)
	}
}

func orNone(s string) string {
	if s == "" {
		return "(not stated)"
	}
	return s
}
