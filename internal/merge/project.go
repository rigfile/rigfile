package merge

import (
	"fmt"

	"github.com/rigfile/rigfile/internal/manifest"
)

// Skipped explains why an item is not applied on this OS/target (shown collapsed on the plan screen:
// visible, never silent; docs/merge-semantics.md §5).
type Skipped struct {
	Category, Key, Reason string
}

// Projection is the merged model filtered to one (OS, target). Items are never dropped silently.
type Projection struct {
	OS, Target string

	Instructions     []Prov[manifest.Instruction]
	Skills           []Prov[manifest.Skill]
	Agents           []Prov[manifest.Agent]
	Commands         []Prov[manifest.Command]
	MCPServers       []NamedServer
	Hooks            []Prov[manifest.Hook]
	Deny, Ask, Allow []Prov[manifest.PermissionRule]
	Logins           []Prov[manifest.Login]
	// SecretHosts maps a secret ref to the hosts its value may be sent to (secrets.<ref>.hosts); the Level 2 broker
	// binds a surrogate to them.
	SecretHosts map[string][]string

	Skipped []Skipped
	// Unrunnable lists hooks that apply here but have no per-OS command for this OS (plan §6.2).
	Unrunnable []string
}

// NamedServer is an MCP server with its name, in sorted order.
type NamedServer struct {
	Name string
	P    Prov[manifest.MCPServer]
}

// Project filters by os:/targets: with AND semantics (omitted = all).
func (m *Merged) Project(osName, target string) *Projection {
	p := &Projection{OS: osName, Target: target}
	skip := func(cat, key string, meta manifest.ItemMeta) bool {
		if meta.AppliesTo(osName, target) {
			return false
		}
		reason := ""
		switch {
		case len(meta.OS) > 0 && !has(meta.OS, osName):
			reason = fmt.Sprintf("os: %v", meta.OS)
		default:
			reason = fmt.Sprintf("targets: %v", meta.Targets)
		}
		p.Skipped = append(p.Skipped, Skipped{cat, key, reason})
		return true
	}
	for _, x := range m.Instructions {
		if !skip("instruction", x.V.Key(), x.V.ItemMeta) {
			p.Instructions = append(p.Instructions, x)
		}
	}
	for _, x := range m.Skills {
		if !skip("skill", x.V.Key(), x.V.ItemMeta) {
			p.Skills = append(p.Skills, x)
		}
	}
	for _, x := range m.Agents {
		if !skip("agent", x.V.Key(), x.V.ItemMeta) {
			p.Agents = append(p.Agents, x)
		}
	}
	for _, x := range m.Commands {
		if !skip("command", x.V.Key(), x.V.ItemMeta) {
			p.Commands = append(p.Commands, x)
		}
	}
	for _, x := range m.Hooks {
		if skip("hook", x.V.Key(), x.V.ItemMeta) {
			continue
		}
		if x.V.Run.For(osName) == "" {
			p.Unrunnable = append(p.Unrunnable, x.V.Key())
		}
		p.Hooks = append(p.Hooks, x)
	}
	for ref, d := range m.Secrets {
		if len(d.V.Hosts) > 0 {
			if p.SecretHosts == nil {
				p.SecretHosts = map[string][]string{}
			}
			p.SecretHosts[ref] = d.V.Hosts
		}
	}
	for _, n := range sortedKeys(m.MCPServers) {
		s := m.MCPServers[n]
		if skip("mcp server", n, s.V.Meta()) {
			continue
		}
		p.MCPServers = append(p.MCPServers, NamedServer{n, s})
	}
	perm := func(list []Prov[manifest.PermissionRule], dst *[]Prov[manifest.PermissionRule], name string) {
		for _, r := range list {
			_, val := r.V.Kind()
			if skip("permission "+name, val, manifest.ItemMeta{OS: r.V.OS, Targets: r.V.Targets}) {
				continue
			}
			*dst = append(*dst, r)
		}
	}
	perm(m.Deny, &p.Deny, "deny")
	perm(m.Ask, &p.Ask, "ask")
	perm(m.Allow, &p.Allow, "allow")
	for _, x := range m.Logins {
		if !skip("login", x.V.Provider, manifest.ItemMeta{OS: x.V.OS}) {
			p.Logins = append(p.Logins, x)
		}
	}
	return p
}

// Permissions returns the projected permissions in the manifest's own type, ready for an adapter.
func (p *Projection) Permissions() manifest.Permissions {
	strip := func(in []Prov[manifest.PermissionRule]) []manifest.PermissionRule {
		out := make([]manifest.PermissionRule, len(in))
		for i, x := range in {
			out[i] = x.V
		}
		return out
	}
	return manifest.Permissions{Deny: strip(p.Deny), Ask: strip(p.Ask), Allow: strip(p.Allow)}
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
