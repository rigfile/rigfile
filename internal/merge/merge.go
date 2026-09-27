// Package merge combines layers (`from:` chain + the rig itself + per-target overrides) into one
// canonical model, exactly as specified in docs/merge-semantics.md. It is a PURE function: no file
// system, no clock, no environment, no OS facts. The result therefore hashes identically on every
// machine (the lockfile depends on that). OS/target filtering happens later, in Project.
package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rigfile/rigfile/internal/manifest"
)

// BaseSecure is the reserved name of the always-on safety layer (plan §8). Items from it are locked.
const BaseSecure = "rigfile/base-secure"

// Layer is one manifest in the linearised layer order (lowest priority first).
type Layer struct {
	Name    string // owner/name
	Version string
	Locked  bool // true only for rigfile/base-secure (verified by the resolver, never by manifest content)
	M       *manifest.Manifest
	Dir     string // where the layer's files live (not part of the canonical hash)
}

// Prov is a value with the layer it came from.
type Prov[T any] struct {
	V     T
	Layer string
	Dir   string `json:"-"`
}

// Replacement records that a later layer replaced an item (shown on the plan screen).
type Replacement struct {
	Category, Key, From, By string
}

// Merged is the canonical, machine-independent result for one target.
type Merged struct {
	Target string

	Instructions []Prov[manifest.Instruction]
	Skills       []Prov[manifest.Skill]
	Agents       []Prov[manifest.Agent]
	Commands     []Prov[manifest.Command]
	MCPServers   map[string]Prov[manifest.MCPServer]
	Hooks        []Prov[manifest.Hook]

	Deny, Ask, Allow []Prov[manifest.PermissionRule]

	// Tools keys: common npm pipx uv cargo go macos.brew macos.cask linux.apt linux.dnf linux.pacman
	// linux.zypper linux.brew windows.winget windows.scoop windows.choco
	Tools map[string][]Prov[string]

	Secrets  map[string]Prov[manifest.SecretDecl]
	Logins   []Prov[manifest.Login]
	Models   map[string]Prov[manifest.Model]
	Gateways map[string]Prov[manifest.Gateway]
	Routing  manifest.Routing

	Layers   []LayerRef
	Replaced []Replacement `json:"-"`
	Warnings []string      `json:"-"`

	locked map[string]string // category|key -> locked layer
}

// LayerRef identifies a layer in the result.
type LayerRef struct{ Name, Version string }

// LockedError: a layer tried to replace or override an item owned by rigfile/base-secure (§3).
type LockedError struct{ Category, Key, LockedBy, By string }

func (e *LockedError) Error() string {
	return fmt.Sprintf("%s %q is locked by %s and cannot be replaced by %s; add a new id instead", e.Category, e.Key, e.LockedBy, e.By)
}

// WideningError: a later layer tried to widen a secret's owner-host binding (§4.7).
type WideningError struct{ Secret, Host, By string }

func (e *WideningError) Error() string {
	return fmt.Sprintf("secret %q: layer %s adds host %q, which the existing binding does not cover; bindings may only narrow", e.Secret, e.By, e.Host)
}

// ConflictError covers post-merge consistency failures (port clashes, dangling references).
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }

// Merge applies layers in order; after each layer, that layer's overrides.<target> block (if any).
func Merge(layers []Layer, target string) (*Merged, error) {
	m := &Merged{
		Target:     target,
		MCPServers: map[string]Prov[manifest.MCPServer]{},
		Tools:      map[string][]Prov[string]{},
		Secrets:    map[string]Prov[manifest.SecretDecl]{},
		Models:     map[string]Prov[manifest.Model]{},
		Gateways:   map[string]Prov[manifest.Gateway]{},
		locked:     map[string]string{},
	}
	for _, l := range layers {
		if l.M == nil {
			return nil, fmt.Errorf("layer %s has no manifest", l.Name)
		}
		m.Layers = append(m.Layers, LayerRef{l.Name, l.Version})
		if err := m.apply(l, contentFromManifest(l.M), true); err != nil {
			return nil, err
		}
		if ov, ok := l.M.Overrides[target]; ok && target != "" {
			ovl := l
			ovl.Name = l.Name + "#overrides." + target
			if err := m.apply(ovl, contentFromOverride(ov), false); err != nil {
				return nil, err
			}
		}
	}
	if err := m.check(); err != nil {
		return nil, err
	}
	return m, nil
}

// content is what one layer (or override block) contributes.
type content struct {
	instructions []manifest.Instruction
	skills       []manifest.Skill
	agents       []manifest.Agent
	commands     []manifest.Command
	mcp          map[string]manifest.MCPServer
	hooks        []manifest.Hook
	perms        manifest.Permissions
	// only for full manifests
	full *manifest.Manifest
}

func contentFromManifest(m *manifest.Manifest) content {
	return content{m.Instructions, m.Skills, m.Agents, m.Commands, m.MCPServers, m.Hooks, m.Permissions, m}
}

func contentFromOverride(o manifest.Override) content {
	return content{o.Instructions, o.Skills, o.Agents, o.Commands, o.MCPServers, o.Hooks, o.Permissions, nil}
}

// slot places an item: new -> appended; existing -> replaced in place (position kept), unless locked.
func slot[T any](m *Merged, list *[]Prov[T], category, key string, keyOf func(T) string, l Layer, v T) error {
	lockKey := category + "|" + key
	for i, e := range *list {
		if keyOf(e.V) != key {
			continue
		}
		if by, ok := m.locked[lockKey]; ok && by != l.Name {
			return &LockedError{category, key, by, l.Name}
		}
		if e.Layer == l.Name {
			return fmt.Errorf("layer %s defines %s %q twice", l.Name, category, key)
		}
		m.Replaced = append(m.Replaced, Replacement{category, key, e.Layer, l.Name})
		(*list)[i] = Prov[T]{V: v, Layer: l.Name, Dir: l.Dir}
		if l.Locked {
			m.locked[lockKey] = l.Name
		}
		return nil
	}
	*list = append(*list, Prov[T]{V: v, Layer: l.Name, Dir: l.Dir})
	if l.Locked {
		m.locked[lockKey] = l.Name
	}
	return nil
}

func (m *Merged) apply(l Layer, c content, full bool) error {
	for _, x := range c.instructions {
		if err := slot(m, &m.Instructions, "instruction", x.Key(), manifest.Instruction.Key, l, x); err != nil {
			return err
		}
	}
	for _, x := range c.skills {
		if err := slot(m, &m.Skills, "skill", x.Key(), manifest.Skill.Key, l, x); err != nil {
			return err
		}
	}
	for _, x := range c.agents {
		if err := slot(m, &m.Agents, "agent", x.Key(), manifest.Agent.Key, l, x); err != nil {
			return err
		}
	}
	for _, x := range c.commands {
		if err := slot(m, &m.Commands, "command", x.Key(), manifest.Command.Key, l, x); err != nil {
			return err
		}
	}
	for _, x := range c.hooks {
		if err := slot(m, &m.Hooks, "hook", x.Key(), manifest.Hook.Key, l, x); err != nil {
			return err
		}
	}
	// MCP servers: whole-entry replace by name (§4.3).
	names := sortedKeys(c.mcp)
	for _, n := range names {
		lockKey := "mcp|" + n
		if old, ok := m.MCPServers[n]; ok {
			if by, locked := m.locked[lockKey]; locked && by != l.Name {
				return &LockedError{"mcp server", n, by, l.Name}
			}
			m.Replaced = append(m.Replaced, Replacement{"mcp server", n, old.Layer, l.Name})
		}
		m.MCPServers[n] = Prov[manifest.MCPServer]{V: c.mcp[n], Layer: l.Name, Dir: l.Dir}
		if l.Locked {
			m.locked[lockKey] = l.Name
		}
	}
	m.addPerms("deny", c.perms.Deny, l)
	m.addPerms("ask", c.perms.Ask, l)
	m.addPerms("allow", c.perms.Allow, l)

	if !full || c.full == nil {
		return nil
	}
	f := c.full
	m.addTools(f.Tools, l)
	for _, k := range sortedKeys(f.Secrets) {
		if err := m.addSecret(k, f.Secrets[k], l); err != nil {
			return err
		}
	}
	for _, lg := range f.Logins {
		replaced := false
		for i, e := range m.Logins {
			if e.V.Provider == lg.Provider {
				m.Logins[i] = Prov[manifest.Login]{V: lg, Layer: l.Name, Dir: l.Dir}
				replaced = true
			}
		}
		if !replaced {
			m.Logins = append(m.Logins, Prov[manifest.Login]{V: lg, Layer: l.Name, Dir: l.Dir})
		}
	}
	for _, k := range sortedKeys(f.Models) {
		if old, ok := m.Models[k]; ok {
			m.Replaced = append(m.Replaced, Replacement{"model", k, old.Layer, l.Name})
		}
		m.Models[k] = Prov[manifest.Model]{V: f.Models[k], Layer: l.Name, Dir: l.Dir}
	}
	for _, k := range sortedKeys(f.Gateways) {
		if old, ok := m.Gateways[k]; ok {
			m.Replaced = append(m.Replaced, Replacement{"gateway", k, old.Layer, l.Name})
		}
		m.Gateways[k] = Prov[manifest.Gateway]{V: f.Gateways[k], Layer: l.Name, Dir: l.Dir}
	}
	if f.Routing.Default != "" {
		m.Routing.Default = f.Routing.Default
	}
	for _, t := range f.Routing.LocalFor {
		if !contains(m.Routing.LocalFor, t) {
			m.Routing.LocalFor = append(m.Routing.LocalFor, t)
		}
	}
	if f.Routing.FallbackOnLimit != "" {
		m.Routing.FallbackOnLimit = f.Routing.FallbackOnLimit
	}
	return nil
}

// addPerms unions rules into a list. A layer can never remove a rule: there is no removal path here.
func (m *Merged) addPerms(list string, rules []manifest.PermissionRule, l Layer) {
	dst := map[string]*[]Prov[manifest.PermissionRule]{"deny": &m.Deny, "ask": &m.Ask, "allow": &m.Allow}[list]
	for _, r := range rules {
		k := permKey(r)
		dup := false
		for _, e := range *dst {
			if permKey(e.V) == k {
				dup = true
				break
			}
		}
		if !dup {
			*dst = append(*dst, Prov[manifest.PermissionRule]{V: r, Layer: l.Name, Dir: l.Dir})
		}
	}
}

func permKey(r manifest.PermissionRule) string {
	kind, val := r.Kind()
	return kind + "\x00" + strings.TrimSpace(val) + "\x00" + strings.Join(r.OS, ",") + "\x00" + strings.Join(r.Targets, ",")
}

func (m *Merged) addTools(t manifest.Tools, l Layer) {
	groups := map[string][]string{
		"common": t.Common, "npm": t.NPM, "pipx": t.Pipx, "uv": t.UV, "cargo": t.Cargo, "go": t.Go,
		"macos.brew": t.MacOS.Brew, "macos.cask": t.MacOS.Cask,
		"linux.apt": t.Linux.Apt, "linux.dnf": t.Linux.Dnf, "linux.pacman": t.Linux.Pacman, "linux.zypper": t.Linux.Zypper, "linux.brew": t.Linux.Brew,
		"windows.winget": t.Windows.Winget, "windows.scoop": t.Windows.Scoop, "windows.choco": t.Windows.Choco,
	}
	for _, g := range sortedKeys(groups) {
		for _, entry := range groups[g] {
			name, ver := splitPin(entry)
			found := false
			for i, e := range m.Tools[g] {
				en, ev := splitPin(e.V)
				if en != name {
					continue
				}
				found = true
				switch {
				case ver == "" || ver == ev:
					// keep the existing (pinned or identical) entry
				case ev == "":
					m.Tools[g][i] = Prov[string]{V: entry, Layer: l.Name, Dir: l.Dir} // a pin beats unpinned
				default:
					m.Warnings = append(m.Warnings, fmt.Sprintf("tool %s (%s): %s pins %s, replacing %s from %s", name, g, l.Name, ver, ev, e.Layer))
					m.Tools[g][i] = Prov[string]{V: entry, Layer: l.Name, Dir: l.Dir}
				}
			}
			if !found {
				m.Tools[g] = append(m.Tools[g], Prov[string]{V: entry, Layer: l.Name, Dir: l.Dir})
			}
		}
	}
}

// splitPin splits "name@1.2.3" (also scoped "@scope/name@1.2.3") into name and version.
func splitPin(s string) (name, ver string) {
	start := 0
	if strings.HasPrefix(s, "@") {
		if i := strings.Index(s, "/"); i > 0 {
			start = i
		}
	}
	if i := strings.LastIndex(s[start:], "@"); i >= 0 {
		return s[:start+i], s[start+i+1:]
	}
	return s, ""
}

// addSecret unions declarations; `hosts` may only narrow (§4.7).
func (m *Merged) addSecret(ref string, d manifest.SecretDecl, l Layer) error {
	old, ok := m.Secrets[ref]
	if !ok {
		m.Secrets[ref] = Prov[manifest.SecretDecl]{V: d, Layer: l.Name, Dir: l.Dir}
		return nil
	}
	merged := old.V
	if d.Description != "" {
		merged.Description = d.Description
	}
	if d.ObtainURL != "" {
		merged.ObtainURL = d.ObtainURL
	}
	merged.Optional = d.Optional
	if len(d.Hosts) > 0 {
		if len(old.V.Hosts) > 0 {
			for _, nh := range d.Hosts {
				covered := false
				for _, oh := range old.V.Hosts {
					if manifest.HostCovers(oh, nh) {
						covered = true
						break
					}
				}
				if !covered {
					return &WideningError{ref, nh, l.Name}
				}
			}
		}
		merged.Hosts = d.Hosts
	} // no hosts in the later layer: keep the earlier binding
	m.Secrets[ref] = Prov[manifest.SecretDecl]{V: merged, Layer: l.Name, Dir: l.Dir}
	return nil
}

// check enforces post-merge consistency.
func (m *Merged) check() error {
	ports := map[string]string{}
	claim := func(port, who string) error {
		if port == "" || port == "0" {
			return nil
		}
		if other, ok := ports[port]; ok {
			return &ConflictError{fmt.Sprintf("port %s is used by both %s and %s", port, other, who)}
		}
		ports[port] = who
		return nil
	}
	for _, k := range sortedKeys(m.Models) {
		if p := m.Models[k].V.Serve.Port; p != 0 {
			if err := claim(fmt.Sprint(p), "model "+k); err != nil {
				return err
			}
		}
	}
	for _, k := range sortedKeys(m.Gateways) {
		g := m.Gateways[k].V
		if i := strings.LastIndex(g.Listen, ":"); i >= 0 {
			if err := claim(g.Listen[i+1:], "gateway "+k); err != nil {
				return err
			}
		}
		for model := range g.Routes {
			if _, ok := m.Models[model]; !ok {
				return &ConflictError{fmt.Sprintf("gateway %s routes to model %q which no layer defines", k, model)}
			}
		}
	}
	if f := m.Routing.FallbackOnLimit; f != "" {
		if _, ok := m.Models[f]; !ok {
			return &ConflictError{fmt.Sprintf("routing.fallback_on_limit names model %q which no layer defines", f)}
		}
	}
	return nil
}

// Hash is the sha256 of the canonical JSON of the merged model. Provenance directories are excluded,
// so the same rig hashes identically on every machine (lockfile input, §6).
func (m *Merged) Hash() (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
