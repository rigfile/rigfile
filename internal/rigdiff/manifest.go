// Package rigdiff explains what changes between two versions of a rig: which items the manifest gains, loses or changes
// (MCP servers, hooks, permissions, secrets, tools ...), which files change, and which of those deserve a person's attention.
// The same engine serves the registry page, the registry API and `rigfile update`.
//
// It never prints a secret value: a rig cannot hold one (manifest schema and scan), and text diffs are shown only for
// files the scanner already accepted.
package rigdiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

// Change is what happened to an item.
type Change string

const (
	Added   Change = "added"
	Removed Change = "removed"
	Changed Change = "changed"
)

// Level ranks a note: Review means "look at this before you accept the update".
type Level string

const (
	Info   Level = "info"
	Review Level = "review"
)

// Note explains why a change matters.
type Note struct {
	Level Level  `json:"level"`
	Text  string `json:"text"`
}

// Entry is one manifest-level change.
type Entry struct {
	Category string `json:"category"`
	Key      string `json:"key"`
	Change   Change `json:"change"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
	Notes    []Note `json:"notes,omitempty"`
}

type item struct {
	desc string
	raw  any
}

type items map[string]map[string]item // category -> key -> item

func canon(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func collect(m *manifest.Manifest) items {
	out := items{}
	put := func(cat, key, desc string, raw any) {
		if out[cat] == nil {
			out[cat] = map[string]item{}
		}
		out[cat][key] = item{desc, raw}
	}
	for _, x := range m.Instructions {
		put("instructions", x.Key(), x.File, x)
	}
	for _, x := range m.Skills {
		d := x.Path
		if d == "" {
			d = x.Ref
		}
		put("skills", x.Key(), d, x)
	}
	for _, x := range m.Agents {
		put("agents", x.Key(), x.Path, x)
	}
	for _, x := range m.Commands {
		put("commands", x.Key(), x.Path, x)
	}
	for n, s := range m.MCPServers {
		put("mcp_servers", n, serverDesc(s), s)
	}
	for _, h := range m.Hooks {
		run := h.Run.Single
		if run == "" {
			var parts []string
			for k, v := range h.Run.PerOS {
				parts = append(parts, k+": "+v)
			}
			sort.Strings(parts)
			run = strings.Join(parts, "; ")
		}
		put("hooks", h.Key(), h.Event+": "+run, h)
	}
	for cat, rules := range map[string][]manifest.PermissionRule{"permissions.deny": m.Permissions.Deny, "permissions.ask": m.Permissions.Ask, "permissions.allow": m.Permissions.Allow} {
		for _, r := range rules {
			k, v := r.Kind()
			put(cat, k+": "+v, "", r)
		}
	}
	tools := map[string][]string{"common": m.Tools.Common, "npm": m.Tools.NPM, "pipx": m.Tools.Pipx, "uv": m.Tools.UV, "cargo": m.Tools.Cargo, "go": m.Tools.Go,
		"macos.brew": m.Tools.MacOS.Brew, "macos.cask": m.Tools.MacOS.Cask, "linux.apt": m.Tools.Linux.Apt, "linux.dnf": m.Tools.Linux.Dnf,
		"linux.pacman": m.Tools.Linux.Pacman, "linux.zypper": m.Tools.Linux.Zypper, "linux.brew": m.Tools.Linux.Brew,
		"windows.winget": m.Tools.Windows.Winget, "windows.scoop": m.Tools.Windows.Scoop, "windows.choco": m.Tools.Windows.Choco}
	for mgr, list := range tools {
		for _, p := range list {
			put("tools."+mgr, p, "", p)
		}
	}
	for ref, d := range m.Secrets {
		put("secrets", ref, strings.Join(d.Hosts, ", "), d)
	}
	for _, l := range m.Logins {
		put("logins", l.Provider, l.Method, l)
	}
	for n, v := range m.Models {
		put("models", n, v.Role, v)
	}
	for n, v := range m.Gateways {
		put("gateways", n, v.Engine, v)
	}
	for _, f := range m.From {
		put("from", f, "", f)
	}
	for _, t := range m.Targets.Include {
		put("targets.include", t, "", t)
	}
	for _, t := range m.Targets.Exclude {
		put("targets.exclude", t, "", t)
	}
	for _, p := range m.Private {
		put("private", p, "", p)
	}
	for t, o := range m.Overrides {
		put("overrides", t, "", o)
	}
	if canon(m.Routing) != canon(manifest.Routing{}) {
		put("routing", "routing", "", m.Routing)
	}
	put("meta", "version", m.Version, m.Version)
	put("meta", "description", m.Description, m.Description)
	put("meta", "license", m.License, m.License)
	return out
}

func serverDesc(s manifest.MCPServer) string {
	if s.IsRemote() {
		return "http " + s.URL
	}
	d := strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
	if len(s.Network.Allow) > 0 {
		d += "  [network: " + strings.Join(s.Network.Allow, ", ") + "]"
	}
	return d
}

// Manifests compares two manifests. The result is ordered by category, then key.
func Manifests(a, b *manifest.Manifest) []Entry {
	ia, ib := collect(a), collect(b)
	cats := map[string]bool{}
	for c := range ia {
		cats[c] = true
	}
	for c := range ib {
		cats[c] = true
	}
	names := make([]string, 0, len(cats))
	for c := range cats {
		names = append(names, c)
	}
	sort.Strings(names)
	var out []Entry
	for _, c := range names {
		keys := map[string]bool{}
		for k := range ia[c] {
			keys[k] = true
		}
		for k := range ib[c] {
			keys[k] = true
		}
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			x, inA := ia[c][k]
			y, inB := ib[c][k]
			e := Entry{Category: c, Key: k}
			switch {
			case inA && !inB:
				e.Change, e.Before = Removed, x.desc
			case !inA && inB:
				e.Change, e.After = Added, y.desc
			case canon(x.raw) != canon(y.raw):
				e.Change, e.Before, e.After = Changed, x.desc, y.desc
			default:
				continue
			}
			e.Notes = notes(e, x, y)
			out = append(out, e)
		}
	}
	return out
}

func added(a, b []string) []string {
	have := map[string]bool{}
	for _, x := range a {
		have[x] = true
	}
	var out []string
	for _, x := range b {
		if !have[x] {
			out = append(out, x)
		}
	}
	return out
}

func note(l Level, f string, a ...any) Note { return Note{l, fmt.Sprintf(f, a...)} }

// notes says why an entry matters. The rules are deliberately few and plain: what the update can RUN, what it can REACH,
// what it can ALLOW, what it ASKS FOR, and what it makes the agent FOLLOW.
func notes(e Entry, before, after item) []Note {
	var ns []Note
	switch {
	case e.Category == "mcp_servers":
		var s0, s1 manifest.MCPServer
		if before.raw != nil {
			s0 = before.raw.(manifest.MCPServer)
		}
		if after.raw != nil {
			s1 = after.raw.(manifest.MCPServer)
		}
		switch e.Change {
		case Added:
			ns = append(ns, note(Review, "adds an MCP server that runs: %s", serverDesc(s1)))
		case Changed:
			if s0.Command != s1.Command || strings.Join(s0.Args, "\x00") != strings.Join(s1.Args, "\x00") || s0.URL != s1.URL || s0.Transport != s1.Transport {
				ns = append(ns, note(Review, "changes what the server runs or connects to: %s  ->  %s", serverDesc(s0), serverDesc(s1)))
			}
			for _, h := range added(s0.Network.Allow, s1.Network.Allow) {
				ns = append(ns, note(Review, "may now reach %s", h))
			}
			if len(s0.Network.Allow) > 0 && len(s1.Network.Allow) == 0 {
				ns = append(ns, note(Review, "no longer declares a network allowlist"))
			}
			for k, v := range s1.Env {
				if ref, ok := manifest.SecretRef(v); ok {
					if old, had := s0.Env[k]; !had || old != v {
						ns = append(ns, note(Review, "reads secret %s into %s", ref, k))
					}
				}
			}
		case Removed:
			ns = append(ns, note(Info, "removes the MCP server"))
		}
	case e.Category == "hooks":
		if e.Change != Removed {
			ns = append(ns, note(Review, "runs a command on %s: %s", after.raw.(manifest.Hook).Event, strings.TrimPrefix(e.After, after.raw.(manifest.Hook).Event+": ")))
		}
	case e.Category == "permissions.allow" && e.Change == Added:
		ns = append(ns, note(Review, "allows this without asking"))
	case e.Category == "permissions.deny" && e.Change == Removed:
		ns = append(ns, note(Review, "removes this protection"))
	case e.Category == "permissions.ask" && e.Change == Removed:
		ns = append(ns, note(Review, "no longer asks before this"))
	case e.Category == "secrets":
		switch e.Change {
		case Added:
			ns = append(ns, note(Info, "asks for a new secret, bound to: %s", orNone(e.After)))
		case Changed:
			ns = append(ns, note(Review, "changes where the secret may be sent: %s  ->  %s", orNone(e.Before), orNone(e.After)))
		}
	case strings.HasPrefix(e.Category, "tools.") && e.Change == Added:
		ns = append(ns, note(Review, "installs this package with %s", strings.TrimPrefix(e.Category, "tools.")))
	case e.Category == "from" && e.Change != Changed:
		ns = append(ns, note(Review, "%s a base rig", map[Change]string{Added: "adds", Removed: "removes"}[e.Change]))
	case e.Category == "logins" && e.Change == Added:
		ns = append(ns, note(Info, "asks you to sign in"))
	case (e.Category == "skills" || e.Category == "agents" || e.Category == "commands" || e.Category == "instructions") && e.Change != Removed:
		ns = append(ns, note(Review, "%s an item whose text the agent will follow", map[Change]string{Added: "adds", Changed: "changes"}[e.Change]))
	}
	return ns
}

func orNone(s string) string {
	if s == "" {
		return "(nothing)"
	}
	return s
}
