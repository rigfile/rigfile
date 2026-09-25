package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/splice"
)

// Capture reads an existing Claude Code setup (read-only) and turns it into a rig: a rigfile.yaml plus
// the files it references. It is the reverse of Build for the categories Rigfile manages.
//
// Safety rules (RIGFILE_PLAN.md §8):
//   - secret VALUES are never copied: credential-like env vars/headers become secret:// references and
//     are reported as "value NOT captured"; anything containing a known secret format is skipped whole;
//   - credential-looking file names are never copied;
//   - symlinks are never followed;
//   - text Rigfile itself wrote (marked regions, owned items) is not captured back.

// CaptureOptions describes what to read.
type CaptureOptions struct {
	ClaudeDir  string // ~/.claude
	ClaudeJSON string // ~/.claude.json (user-scope MCP servers); "" = skip MCP
	Home       string // used to rewrite absolute home paths to ~/ in permission rules
	Name       string // rig name, owner/name
	Version    string
	Skip       func(category, key string) bool // items already managed by Rigfile (from state.json)
}

// Finding is one line of the capture report.
type Finding struct {
	Level    string // captured | skipped | redacted | note
	Category string
	Item     string
	Msg      string
}

// Captured is the result: nothing has been written to disk.
type Captured struct {
	Manifest []byte
	Files    map[string][]byte // rig-relative path (forward slashes) → content
	Report   []Finding
}

const (
	maxFileBytes  = 1 << 20
	maxSkillFiles = 500
)

var (
	credName  = regexp.MustCompile(`(?i)(^|_)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|AUTH|APIKEY)(_|$)`)
	credHdr   = regexp.MustCompile(`(?i)^(authorization|proxy-authorization)$|token|secret|^x-.*key`)
	identRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	refSegRe  = regexp.MustCompile(`[^a-z0-9_-]+`)
	homeAbsRe = regexp.MustCompile(`^(/Users/|/home/|[A-Za-z]:)`)
)

// yaml output shapes (field order = file order; omitempty keeps it small).
type (
	outRig struct {
		APIVersion   string               `yaml:"apiVersion"`
		Name         string               `yaml:"name"`
		Version      string               `yaml:"version"`
		Description  string               `yaml:"description,omitempty"`
		Instructions []outInstruction     `yaml:"instructions,omitempty"`
		Skills       []outPath            `yaml:"skills,omitempty"`
		Agents       []outPath            `yaml:"agents,omitempty"`
		Commands     []outPath            `yaml:"commands,omitempty"`
		MCPServers   map[string]*outMCP   `yaml:"mcp_servers,omitempty"`
		Hooks        []outHook            `yaml:"hooks,omitempty"`
		Permissions  *outPerms            `yaml:"permissions,omitempty"`
		Secrets      map[string]outSecret `yaml:"secrets,omitempty"`
	}
	outInstruction struct {
		ID   string `yaml:"id"`
		File string `yaml:"file"`
	}
	outPath struct {
		Path string `yaml:"path"`
	}
	outMCP struct {
		Transport   string            `yaml:"transport,omitempty"`
		Command     string            `yaml:"command,omitempty"`
		Args        []string          `yaml:"args,omitempty"`
		Env         map[string]string `yaml:"env,omitempty"`
		URL         string            `yaml:"url,omitempty"`
		Auth        string            `yaml:"auth,omitempty"`
		BearerToken string            `yaml:"bearer_token,omitempty"`
		Headers     map[string]string `yaml:"headers,omitempty"`
	}
	outHook struct {
		ID      string    `yaml:"id"`
		Event   string    `yaml:"event"`
		Match   *outMatch `yaml:"match,omitempty"`
		Run     any       `yaml:"run"`
		Timeout int       `yaml:"timeout_seconds,omitempty"`
	}
	outMatch struct {
		Tool    string `yaml:"tool,omitempty"`
		Command string `yaml:"command,omitempty"`
		Path    string `yaml:"path,omitempty"`
	}
	outPerms struct {
		Deny  []map[string]string `yaml:"deny,omitempty"`
		Ask   []map[string]string `yaml:"ask,omitempty"`
		Allow []map[string]string `yaml:"allow,omitempty"`
	}
	outSecret struct {
		Description string `yaml:"description"`
	}
)

type capturer struct {
	o       CaptureOptions
	rig     outRig
	files   map[string][]byte
	report  []Finding
	skipped func(category, key string) bool
}

func (c *capturer) add(level, cat, item, msg string) {
	c.report = append(c.report, Finding{level, cat, item, msg})
}

// Capture builds the rig. Missing files/dirs are fine (an empty category); read errors are returned.
func Capture(o CaptureOptions) (*Captured, error) {
	if o.Name == "" {
		o.Name = "local/my-rig"
	}
	if o.Version == "" {
		o.Version = "0.1.0"
	}
	c := &capturer{o: o, files: map[string][]byte{}, skipped: o.Skip}
	if c.skipped == nil {
		c.skipped = func(string, string) bool { return false }
	}
	c.rig = outRig{APIVersion: "rigfile.dev/v1", Name: o.Name, Version: o.Version, Description: "Captured from an existing Claude Code setup"}

	steps := []func() error{c.instructions, c.tree("skills"), c.flat("agents"), c.flat("commands"), c.settings, c.mcp}
	for _, s := range steps {
		if err := s(); err != nil {
			return nil, err
		}
	}
	mb, err := yaml.Marshal(c.rig)
	if err != nil {
		return nil, err
	}
	header := "# Captured by `rigfile init`. Review before committing: secret VALUES were not captured, but\n# instructions, hooks and skills are your own text and may contain private details.\n"
	return &Captured{Manifest: append([]byte(header), mb...), Files: c.files, Report: c.report}, nil
}

func read(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// --- instructions -----------------------------------------------------------------------------

func (c *capturer) instructions() error {
	b, ok, err := read(filepath.Join(c.o.ClaudeDir, "CLAUDE.md"))
	if err != nil || !ok {
		return err
	}
	body, ids, err := splice.StripAll(b, splice.HTML)
	if err != nil {
		c.add("skipped", "instruction", "CLAUDE.md", "its Rigfile markers are malformed; fix or remove them and re-run ("+err.Error()+")")
		return nil
	}
	if len(ids) > 0 {
		c.add("note", "instruction", "CLAUDE.md", fmt.Sprintf("%d section(s) managed by Rigfile were left out", len(ids)))
	}
	if strings.TrimSpace(string(body)) == "" {
		return nil
	}
	if scan.LooksLikeSecret(string(body)) {
		c.add("skipped", "instruction", "CLAUDE.md", "contains text that looks like a secret; not captured (remove it, then re-run)")
		return nil
	}
	c.files["instructions/claude-md.md"] = append(body, '\n')
	c.rig.Instructions = append(c.rig.Instructions, outInstruction{ID: "claude-md", File: "instructions/claude-md.md"})
	c.add("captured", "instruction", "claude-md", "from CLAUDE.md")
	return nil
}

// --- skills / agents / commands ---------------------------------------------------------------

func (c *capturer) tree(dir string) func() error {
	return func() error {
		root := filepath.Join(c.o.ClaudeDir, dir)
		ents, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range ents {
			name := e.Name()
			switch {
			case strings.HasPrefix(name, "."):
				continue
			case c.skipped("skill", name):
				c.add("note", "skill", name, "already managed by Rigfile; not captured")
			case e.Type()&os.ModeSymlink != 0:
				c.add("skipped", "skill", name, "is a symlink; symlinks are never followed")
			case !e.IsDir():
				c.add("skipped", "skill", name, "not a directory")
			case !identRe.MatchString(name):
				c.add("skipped", "skill", name, "name is not a valid identifier (letters, digits, - and _)")
			default:
				c.skill(name, filepath.Join(root, name))
			}
		}
		return nil
	}
}

func (c *capturer) skill(name, src string) {
	if _, err := os.Lstat(filepath.Join(src, "SKILL.md")); err != nil {
		c.add("skipped", "skill", name, "has no SKILL.md")
		return
	}
	files := map[string][]byte{}
	var problem string
	var walk func(rel string)
	walk = func(rel string) {
		ents, err := os.ReadDir(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			problem = err.Error()
			return
		}
		for _, e := range ents {
			if problem != "" {
				return
			}
			r := strings.TrimPrefix(rel+"/"+e.Name(), "/")
			full := filepath.Join(src, filepath.FromSlash(r))
			switch {
			case e.Type()&os.ModeSymlink != 0:
				problem = r + " is a symlink"
			case scan.IsSensitiveFilename(r):
				problem = r + " looks like a credentials file"
			case e.IsDir():
				walk(r)
			case !e.Type().IsRegular():
				problem = r + " is not a regular file"
			default:
				b, err := os.ReadFile(full)
				switch {
				case err != nil:
					problem = err.Error()
				case len(b) > maxFileBytes:
					problem = r + " is larger than 1 MiB"
				case scan.LooksLikeSecret(string(b)):
					problem = r + " contains text that looks like a secret"
				case len(files) >= maxSkillFiles:
					problem = "more than 500 files"
				default:
					files[r] = b
				}
			}
		}
	}
	walk("")
	if problem != "" {
		c.add("skipped", "skill", name, problem+"; the whole skill was left out")
		return
	}
	for r, b := range files {
		c.files["skills/"+name+"/"+r] = b
	}
	c.rig.Skills = append(c.rig.Skills, outPath{Path: "skills/" + name})
	c.add("captured", "skill", name, fmt.Sprintf("%d file(s)", len(files)))
}

func (c *capturer) flat(dir string) func() error {
	cat := strings.TrimSuffix(dir, "s")
	return func() error {
		root := filepath.Join(c.o.ClaudeDir, dir)
		ents, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range ents {
			name := e.Name()
			key := strings.TrimSuffix(name, ".md")
			switch {
			case strings.HasPrefix(name, "."):
				continue
			case e.Type()&os.ModeSymlink != 0:
				c.add("skipped", cat, name, "is a symlink; symlinks are never followed")
			case e.IsDir():
				c.add("skipped", cat, name, "nested folders are not supported yet")
			case !strings.HasSuffix(name, ".md") || !identRe.MatchString(key):
				c.add("skipped", cat, name, "not a *.md file with a simple name")
			case c.skipped(cat, key):
				c.add("note", cat, key, "already managed by Rigfile; not captured")
			default:
				b, err := os.ReadFile(filepath.Join(root, name))
				switch {
				case err != nil:
					return err
				case len(b) > maxFileBytes:
					c.add("skipped", cat, key, "larger than 1 MiB")
				case scan.LooksLikeSecret(string(b)):
					c.add("skipped", cat, key, "contains text that looks like a secret; not captured")
				default:
					c.files[dir+"/"+name] = b
					p := outPath{Path: dir + "/" + name}
					if dir == "agents" {
						c.rig.Agents = append(c.rig.Agents, p)
					} else {
						c.rig.Commands = append(c.rig.Commands, p)
					}
					c.add("captured", cat, key, "")
				}
			}
		}
		return nil
	}
}

// --- settings.json: permissions and hooks ------------------------------------------------------

var (
	eventCanon = invert(eventNames)
	toolCanon  = invert(toolNames)
	mcpRuleRe  = regexp.MustCompile(`^mcp__([A-Za-z0-9_-]+?)(?:__([A-Za-z0-9_-]+))?$`)
	ruleRe     = regexp.MustCompile(`^([A-Za-z]+)\((.*)\)$`)
)

func invert(m map[string]string) map[string]string {
	o := make(map[string]string, len(m))
	for k, v := range m {
		o[v] = k
	}
	return o
}

func (c *capturer) settings() error {
	b, ok, err := read(filepath.Join(c.o.ClaudeDir, "settings.json"))
	if err != nil || !ok {
		return err
	}
	var doc struct {
		Permissions struct {
			Deny, Ask, Allow []string
		} `json:"permissions"`
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string   `json:"type"`
				If      string   `json:"if"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Timeout int      `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		c.add("skipped", "settings", "settings.json", "is not valid JSON ("+err.Error()+"); permissions and hooks were not captured")
		return nil
	}
	if len(doc.Env) > 0 {
		c.add("note", "settings", "env", fmt.Sprintf("%d variable(s) in settings.json `env` were not captured (they may hold secrets)", len(doc.Env)))
	}

	perms := &outPerms{}
	for _, l := range []struct {
		name  string
		rules []string
		dst   *[]map[string]string
	}{{"deny", doc.Permissions.Deny, &perms.Deny}, {"ask", doc.Permissions.Ask, &perms.Ask}, {"allow", doc.Permissions.Allow, &perms.Allow}} {
		for _, r := range l.rules {
			if c.skipped("permission", l.name+":"+r) {
				c.add("note", "permission", l.name+": "+shorten(r), "already managed by Rigfile; not captured")
				continue
			}
			canon, why := c.reverseRule(r)
			if why != "" {
				c.add("skipped", "permission", l.name+": "+shorten(r), why)
				continue
			}
			*l.dst = append(*l.dst, canon)
			c.add("captured", "permission", l.name+": "+shorten(r), "")
		}
	}
	if len(perms.Deny)+len(perms.Ask)+len(perms.Allow) > 0 {
		c.rig.Permissions = perms
	}

	events := make([]string, 0, len(doc.Hooks))
	for ev := range doc.Hooks {
		events = append(events, ev)
	}
	sort.Strings(events)
	n := 0
	for _, ev := range events {
		canonEv, ok := eventCanon[ev]
		for gi, g := range doc.Hooks[ev] {
			for hi, h := range g.Hooks {
				label := fmt.Sprintf("%s[%d.%d]", ev, gi, hi)
				n++
				id := fmt.Sprintf("%s-%d", strings.ReplaceAll(canonEv, "_", "-"), n)
				switch {
				case !ok:
					c.add("skipped", "hook", label, "event "+ev+" has no canonical name yet")
				case h.Type != "command":
					c.add("skipped", "hook", label, "only command hooks are supported (this one is type "+h.Type+")")
				default:
					c.hook(id, label, canonEv, g.Matcher, h.If, h.Command, h.Args, h.Timeout)
				}
			}
		}
	}
	return nil
}

func shorten(s string) string {
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

// reverseRule maps a native permission rule to the canonical map; why != "" when it cannot.
func (c *capturer) reverseRule(r string) (map[string]string, string) {
	if scan.LooksLikeSecret(r) {
		return nil, "contains text that looks like a secret"
	}
	if m := mcpRuleRe.FindStringSubmatch(r); m != nil {
		v := m[1]
		if m[2] != "" {
			v += ":" + m[2]
		}
		return map[string]string{"mcp": v}, ""
	}
	m := ruleRe.FindStringSubmatch(r)
	if m == nil {
		return nil, "a bare tool rule has no canonical form"
	}
	tool, arg := m[1], m[2]
	switch tool {
	case "Bash":
		return map[string]string{"bash": arg}, ""
	case "WebFetch":
		if d, ok := strings.CutPrefix(arg, "domain:"); ok {
			return map[string]string{"web_fetch": d}, ""
		}
		return nil, "only WebFetch(domain:...) rules are supported"
	case "Read", "Edit":
		p := c.hostPath(arg)
		if p == "" {
			return nil, "the path is tied to this machine's home or a drive letter"
		}
		return map[string]string{strings.ToLower(tool): p}, ""
	}
	return nil, tool + " rules have no canonical form"
}

// hostPath reverses anchorPath: "//abs" → "/abs", home paths → "~/...". "" = not portable.
func (c *capturer) hostPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "//"); ok {
		p = "/" + rest
	}
	if c.o.Home != "" {
		h := strings.TrimRight(filepath.ToSlash(c.o.Home), "/")
		if p == h {
			p = "~"
		} else if rest, ok := strings.CutPrefix(p, h+"/"); ok {
			p = "~/" + rest
		}
	}
	if homeAbsRe.MatchString(p) || strings.Contains(p, `\`) {
		return ""
	}
	return p
}

func (c *capturer) hook(id, label, event, matcher, ifRule, command string, args []string, timeout int) {
	// a hook that Rigfile installed: round-trips as the built-in
	if len(args) >= 3 && args[0] == "hook" && args[1] == "run" && strings.Contains(filepath.Base(command), "rigfile") {
		c.addHook(outHook{ID: id, Event: event, Match: c.match(matcher, ifRule), Run: "builtin:" + args[2], Timeout: timeout})
		c.add("captured", "hook", label, "built-in "+args[2])
		return
	}
	if strings.Contains(filepath.ToSlash(command), "/rigfile/hooks/") || (len(args) > 0 && strings.Contains(filepath.ToSlash(args[len(args)-1]), "/rigfile/hooks/")) {
		c.add("note", "hook", label, "a script installed by Rigfile; not captured")
		return
	}
	if command == "" {
		c.add("skipped", "hook", label, "has no command")
		return
	}
	full := command
	if len(args) > 0 {
		full = shellJoin(append([]string{command}, args...))
	}
	if scan.LooksLikeSecret(full) {
		c.add("skipped", "hook", label, "its command contains text that looks like a secret")
		return
	}
	if strings.Contains(full, c.o.Home) && c.o.Home != "" {
		full = strings.ReplaceAll(full, c.o.Home, "$HOME")
	}
	script := "hooks/" + id + ".sh"
	c.files[script] = []byte("#!/bin/sh\n" + full + "\n")
	c.addHook(outHook{ID: id, Event: event, Match: c.match(matcher, ifRule), Run: map[string]string{"macos": script, "linux": script}, Timeout: timeout})
	c.add("captured", "hook", label, "as "+script+" (macOS/Linux only) which runs: "+shorten(full)+"  — REVIEW: hooks execute code")
}

func (c *capturer) addHook(h outHook) { c.rig.Hooks = append(c.rig.Hooks, h) }

// match reverses matcherFor for the shapes it can produce.
func (c *capturer) match(matcher, ifRule string) *outMatch {
	m := &outMatch{}
	switch {
	case matcher == "" || matcher == "*":
	case strings.HasPrefix(matcher, "mcp__"):
		rest := strings.TrimPrefix(matcher, "mcp__")
		if rest == ".*" {
			m.Tool = "mcp"
		} else {
			m.Tool = "mcp:" + strings.ReplaceAll(rest, "__", ":")
		}
	default:
		if t, ok := toolCanon[matcher]; ok {
			m.Tool = t
		} else {
			c.add("note", "hook", matcher, "matcher is not a single known tool; the hook was captured without a match (it will run for every tool)")
		}
	}
	if r := ruleRe.FindStringSubmatch(ifRule); r != nil {
		switch r[1] {
		case "Bash":
			m.Command = r[2]
		case "Read", "Edit":
			m.Path = c.hostPath(r[2])
		}
	}
	if *m == (outMatch{}) {
		return nil
	}
	return m
}

func shellJoin(parts []string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		if p != "" && !strings.ContainsAny(p, " \t\n\"'\\$`&|;<>()*?[]{}!#~") {
			out[i] = p
		} else {
			out[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return strings.Join(out, " ")
}

// --- MCP servers (~/.claude.json, user scope) ---------------------------------------------------

func (c *capturer) mcp() error {
	if c.o.ClaudeJSON == "" {
		return nil
	}
	b, ok, err := read(c.o.ClaudeJSON)
	if err != nil || !ok {
		return err
	}
	var doc struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		c.add("skipped", "mcp", filepath.Base(c.o.ClaudeJSON), "is not valid JSON; MCP servers were not captured")
		return nil
	}
	names := make([]string, 0, len(doc.MCPServers))
	for n := range doc.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := doc.MCPServers[name]
		if c.skipped("mcp", name) {
			c.add("note", "mcp", name, "already managed by Rigfile; not captured")
			continue
		}
		if !identRe.MatchString(name) {
			c.add("skipped", "mcp", name, "name is not a valid identifier")
			continue
		}
		var out *outMCP
		var why string
		switch s.Type {
		case "", "stdio":
			out, why = c.mcpStdio(name, s.Command, s.Args, s.Env)
		case "http":
			out, why = c.mcpHTTP(name, s.URL, s.Headers)
		default:
			why = "transport " + s.Type + " is not supported (stdio and http only)"
		}
		if why != "" {
			c.add("skipped", "mcp", name, why)
			continue
		}
		if c.rig.MCPServers == nil {
			c.rig.MCPServers = map[string]*outMCP{}
		}
		c.rig.MCPServers[name] = out
		c.add("captured", "mcp", name, "")
	}
	return nil
}

func (c *capturer) secretRef(server, name, what string) string {
	seg := refSegRe.ReplaceAllString(strings.ToLower(name), "_")
	ref := "secret://" + refSegRe.ReplaceAllString(strings.ToLower(server), "_") + "/" + seg
	if c.rig.Secrets == nil {
		c.rig.Secrets = map[string]outSecret{}
	}
	c.rig.Secrets[strings.TrimPrefix(ref, "secret://")] = outSecret{Description: what + " of the " + server + " MCP server"}
	c.add("redacted", "secret", strings.TrimPrefix(ref, "secret://"), "value NOT captured; set it with `rigfile secrets set "+strings.TrimPrefix(ref, "secret://")+"`")
	return ref
}

func (c *capturer) mcpStdio(name, command string, args []string, env map[string]string) (*outMCP, string) {
	out := &outMCP{}
	// already wrapped by Rigfile: unwrap so the rig round-trips
	if strings.Contains(filepath.Base(command), "rigfile") && len(args) > 0 && args[0] == "exec" {
		rest := args[1:]
		env = map[string]string{}
		for len(rest) > 0 && rest[0] != "--" {
			if len(rest) < 2 {
				return nil, "unrecognised rigfile exec wrapper"
			}
			kv := strings.SplitN(rest[1], "=", 2)
			if len(kv) != 2 {
				return nil, "unrecognised rigfile exec wrapper"
			}
			switch rest[0] {
			case "--secret":
				out.Env = ensure(out.Env)
				out.Env[kv[0]] = "secret://" + kv[1]
				if c.rig.Secrets == nil {
					c.rig.Secrets = map[string]outSecret{}
				}
				c.rig.Secrets[kv[1]] = outSecret{Description: kv[0] + " of the " + name + " MCP server"}
			case "--env":
				env[kv[0]] = kv[1]
			default:
				return nil, "unrecognised rigfile exec wrapper"
			}
			rest = rest[2:]
		}
		if len(rest) < 2 {
			return nil, "unrecognised rigfile exec wrapper"
		}
		command, args = rest[1], rest[2:]
	}
	if scan.LooksLikeSecret(command + " " + strings.Join(args, " ")) {
		return nil, "its command line contains text that looks like a secret"
	}
	out.Command, out.Args = command, args
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := env[k]
		if credName.MatchString(k) || scan.LooksLikeSecret(v) {
			out.Env = ensure(out.Env)
			out.Env[k] = c.secretRef(name, k, k)
		} else {
			out.Env = ensure(out.Env)
			out.Env[k] = v
		}
	}
	return out, ""
}

func ensure(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func (c *capturer) mcpHTTP(name, url string, headers map[string]string) (*outMCP, string) {
	if !strings.HasPrefix(url, "https://") {
		return nil, "remote servers must use https"
	}
	if scan.LooksLikeSecret(url) || strings.Contains(strings.SplitN(strings.TrimPrefix(url, "https://"), "/", 2)[0], "@") {
		return nil, "its URL contains credentials"
	}
	out := &outMCP{Transport: "http", URL: url}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := headers[k]
		switch {
		case strings.EqualFold(k, "authorization") && strings.HasPrefix(strings.ToLower(v), "bearer "):
			out.Auth = "bearer"
			out.BearerToken = c.secretRef(name, "bearer_token", "bearer token")
		case credHdr.MatchString(k) || scan.LooksLikeSecret(v):
			out.Headers = ensure(out.Headers)
			out.Headers[k] = c.secretRef(name, "header_"+k, k+" header")
		default:
			out.Headers = ensure(out.Headers)
			out.Headers[k] = v
		}
	}
	return out, ""
}

// Problems validates a captured manifest against the schema (the caller runs Check after writing it).
func (cp *Captured) Problems() error {
	_, err := manifest.Parse(cp.Manifest)
	return err
}
