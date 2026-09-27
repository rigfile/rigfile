// Package capture turns an existing tool setup back into a rig (`rigfile init --from <target>`, RIGFILE_PLAN.md
// §9.1). Adapters read their own files and feed this builder; the builder owns the safety rules (Stage 1's
// `init`): secret VALUES are never copied (credential-like env vars and headers become secret:// references and
// are reported), text with a known secret format is skipped whole, symlinks and credential-named files are never
// followed, and machine-specific absolute paths keep a server out of the rig.
package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rigfile/rigfile/internal/scan"
)

// Finding is one line of the capture report.
type Finding struct {
	Level    string // captured | skipped | redacted | note
	Category string
	Item     string
	Msg      string
}

// Result is what a capture produced; nothing has been written to disk.
type Result struct {
	Manifest []byte
	Files    map[string][]byte // rig-relative path (forward slashes) -> content
	Report   []Finding
}

// Options are common to every target's capture.
type Options struct {
	Dir     string // the target's config directory
	Home    string // rewrites nothing; used to spot machine-specific absolute paths
	Name    string
	Version string
	// IncludeManaged captures content Rigfile itself wrote (marked regions, wrapped MCP servers) too. Off for
	// `init` (a rig should not swallow what an earlier rig applied); on for round-trip tests.
	IncludeManaged bool
	Skip           func(category, key string) bool // items already managed by Rigfile (from state.json)
}

// Rig output shapes (field order = file order).
type (
	rigOut struct {
		APIVersion   string               `yaml:"apiVersion"`
		Name         string               `yaml:"name"`
		Version      string               `yaml:"version"`
		Description  string               `yaml:"description,omitempty"`
		Instructions []instructionOut     `yaml:"instructions,omitempty"`
		Skills       []pathOut            `yaml:"skills,omitempty"`
		Agents       []pathOut            `yaml:"agents,omitempty"`
		Commands     []pathOut            `yaml:"commands,omitempty"`
		MCPServers   map[string]*MCPOut   `yaml:"mcp_servers,omitempty"`
		Secrets      map[string]secretOut `yaml:"secrets,omitempty"`
	}
	instructionOut struct {
		ID   string `yaml:"id"`
		File string `yaml:"file"`
	}
	pathOut struct {
		Path string `yaml:"path"`
	}
	secretOut struct {
		Description string `yaml:"description"`
	}
	// MCPOut is one captured MCP server.
	MCPOut struct {
		Transport   string            `yaml:"transport,omitempty"`
		Command     string            `yaml:"command,omitempty"`
		Args        []string          `yaml:"args,omitempty"`
		Env         map[string]string `yaml:"env,omitempty"`
		URL         string            `yaml:"url,omitempty"`
		Auth        string            `yaml:"auth,omitempty"`
		BearerToken string            `yaml:"bearer_token,omitempty"`
		Headers     map[string]string `yaml:"headers,omitempty"`
	}
)

// Builder accumulates a capture.
type Builder struct {
	O      Options
	rig    rigOut
	files  map[string][]byte
	report []Finding
}

// New starts a capture.
func New(o Options) *Builder {
	if o.Name == "" {
		o.Name = "local/my-rig"
	}
	if o.Version == "" {
		o.Version = "0.1.0"
	}
	if o.Skip == nil {
		o.Skip = func(string, string) bool { return false }
	}
	return &Builder{O: o, files: map[string][]byte{},
		rig: rigOut{APIVersion: "rigfile.dev/v1", Name: o.Name, Version: o.Version, Description: "Captured from an existing tool setup"}}
}

// Add records a report line.
func (b *Builder) Add(level, cat, item, msg string) {
	b.report = append(b.report, Finding{level, cat, item, msg})
}

// Skipped reports whether Rigfile already manages the item (and says so).
func (b *Builder) Skipped(cat, key string) bool {
	if b.O.Skip(cat, key) {
		b.Add("note", cat, key, "already managed by Rigfile; not captured")
		return true
	}
	return false
}

const maxFileBytes = 1 << 20

var (
	identRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	credName  = regexp.MustCompile(`(?i)(^|_)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|AUTH|APIKEY)(_|$)`)
	credHdr   = regexp.MustCompile(`(?i)^(authorization|proxy-authorization)$|token|secret|^x-.*key`)
	refSegRe  = regexp.MustCompile(`[^a-z0-9_-]+`)
	homeAbsRe = regexp.MustCompile(`^(/Users/|/home/|[A-Za-z]:)`)
)

// Instruction adds a text file as instructions/<id>.md unless it holds a secret.
func (b *Builder) Instruction(id string, body []byte, from string) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return
	}
	if scan.LooksLikeSecret(text) {
		b.Add("skipped", "instruction", id, from+" contains text that looks like a secret; not captured (remove it, then re-run)")
		return
	}
	if !identRe.MatchString(id) {
		b.Add("skipped", "instruction", id, "the id is not a valid identifier")
		return
	}
	path := "instructions/" + id + ".md"
	b.files[path] = []byte(text + "\n")
	b.rig.Instructions = append(b.rig.Instructions, instructionOut{ID: id, File: path})
	b.Add("captured", "instruction", id, "from "+from)
}

// Skill copies a skill directory (safely) as skills/<name>.
func (b *Builder) Skill(name, src string) {
	if !identRe.MatchString(name) {
		b.Add("skipped", "skill", name, "name is not a valid identifier")
		return
	}
	if _, err := os.Lstat(filepath.Join(src, "SKILL.md")); err != nil {
		b.Add("skipped", "skill", name, "has no SKILL.md")
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
				data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(r)))
				switch {
				case err != nil:
					problem = err.Error()
				case len(data) > maxFileBytes:
					problem = r + " is larger than 1 MiB"
				case scan.LooksLikeSecret(string(data)):
					problem = r + " contains text that looks like a secret"
				case len(files) >= 500:
					problem = "more than 500 files"
				default:
					files[r] = data
				}
			}
		}
	}
	walk("")
	if problem != "" {
		b.Add("skipped", "skill", name, problem+"; the whole skill was left out")
		return
	}
	for r, data := range files {
		b.files["skills/"+name+"/"+r] = data
	}
	b.rig.Skills = append(b.rig.Skills, pathOut{Path: "skills/" + name})
	b.Add("captured", "skill", name, fmt.Sprintf("%d file(s)", len(files)))
}

// Markdown adds an agent or command as <dir>/<key>.md with the given frontmatter fields and body.
func (b *Builder) Markdown(category, key string, front [][2]string, body string) {
	if !identRe.MatchString(key) {
		b.Add("skipped", category, key, "the name is not a simple identifier")
		return
	}
	var sb strings.Builder
	if len(front) > 0 {
		sb.WriteString("---\n")
		for _, kv := range front {
			sb.WriteString(kv[0] + ": " + yamlScalar(kv[1]) + "\n")
		}
		sb.WriteString("---\n")
	}
	sb.WriteString(strings.TrimSpace(body) + "\n")
	text := sb.String()
	if scan.LooksLikeSecret(text) || len(text) > maxFileBytes {
		b.Add("skipped", category, key, "contains text that looks like a secret (or is too large); not captured")
		return
	}
	dir := category + "s"
	path := dir + "/" + key + ".md"
	b.files[path] = []byte(text)
	out := pathOut{Path: path}
	if category == "agent" {
		b.rig.Agents = append(b.rig.Agents, out)
	} else {
		b.rig.Commands = append(b.rig.Commands, out)
	}
	b.Add("captured", category, key, "")
}

func yamlScalar(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if s == "" || strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`") {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}

func (b *Builder) secretRef(server, name, what string) string {
	seg := refSegRe.ReplaceAllString(strings.ToLower(name), "_")
	id := refSegRe.ReplaceAllString(strings.ToLower(server), "_") + "/" + seg
	b.declare(id, what+" of the "+server+" MCP server")
	b.Add("redacted", "secret", id, "value NOT captured; set it with `rigfile secrets set "+id+"`")
	return "secret://" + id
}

func (b *Builder) declare(id, desc string) {
	if b.rig.Secrets == nil {
		b.rig.Secrets = map[string]secretOut{}
	}
	b.rig.Secrets[id] = secretOut{Description: desc}
}

// MCPStdio adds a stdio server. A server already wrapped in `rigfile exec` is unwrapped (its secret refs are
// kept), unless IncludeManaged is off, in which case it is left out as Rigfile-managed.
func (b *Builder) MCPStdio(name, command string, args []string, env map[string]string) {
	if !identRe.MatchString(name) {
		b.Add("skipped", "mcp", name, "name is not a valid identifier")
		return
	}
	out := &MCPOut{}
	if strings.Contains(filepath.Base(command), "rigfile") && len(args) > 0 && args[0] == "exec" {
		if !b.O.IncludeManaged {
			b.Add("note", "mcp", name, "wrapped by Rigfile already (managed); not captured")
			return
		}
		rest := args[1:]
		env = map[string]string{}
		for len(rest) > 0 && rest[0] != "--" {
			if len(rest) < 2 {
				b.Add("skipped", "mcp", name, "unrecognised rigfile exec wrapper")
				return
			}
			kv := strings.SplitN(rest[1], "=", 2)
			if len(kv) != 2 {
				b.Add("skipped", "mcp", name, "unrecognised rigfile exec wrapper")
				return
			}
			switch rest[0] {
			case "--secret":
				if out.Env == nil {
					out.Env = map[string]string{}
				}
				out.Env[kv[0]] = "secret://" + kv[1]
				b.declare(kv[1], kv[0]+" of the "+name+" MCP server")
			case "--env":
				env[kv[0]] = kv[1]
			default:
				b.Add("skipped", "mcp", name, "unrecognised rigfile exec wrapper")
				return
			}
			rest = rest[2:]
		}
		if len(rest) < 2 {
			b.Add("skipped", "mcp", name, "unrecognised rigfile exec wrapper")
			return
		}
		command, args = rest[1], rest[2:]
	}
	if scan.LooksLikeSecret(command + " " + strings.Join(args, " ")) {
		b.Add("skipped", "mcp", name, "its command line contains text that looks like a secret")
		return
	}
	if h := b.O.Home; h != "" {
		all := append([]string{command}, args...)
		for _, v := range env {
			all = append(all, v)
		}
		for _, a := range all {
			if strings.Contains(a, h) || homeAbsRe.MatchString(a) {
				b.Add("skipped", "mcp", name, "an argument or env value is an absolute path on this machine; not portable, so the server was left out. Add it by hand with a path that works everywhere")
				return
			}
		}
	}
	out.Command, out.Args = command, args
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if out.Env == nil {
			out.Env = map[string]string{}
		}
		if credName.MatchString(k) || scan.LooksLikeSecret(env[k]) {
			out.Env[k] = b.secretRef(name, k, k)
		} else {
			out.Env[k] = env[k]
		}
	}
	b.addMCP(name, out)
}

// MCPHTTP adds a remote server.
func (b *Builder) MCPHTTP(name, url string, headers map[string]string) {
	if !identRe.MatchString(name) {
		b.Add("skipped", "mcp", name, "name is not a valid identifier")
		return
	}
	if !strings.HasPrefix(url, "https://") {
		b.Add("skipped", "mcp", name, "remote servers must use https")
		return
	}
	if scan.LooksLikeSecret(url) || strings.Contains(strings.SplitN(strings.TrimPrefix(url, "https://"), "/", 2)[0], "@") {
		b.Add("skipped", "mcp", name, "its URL contains credentials")
		return
	}
	out := &MCPOut{Transport: "http", URL: url}
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
			out.BearerToken = b.secretRef(name, "bearer_token", "bearer token")
		case credHdr.MatchString(k) || scan.LooksLikeSecret(v):
			if out.Headers == nil {
				out.Headers = map[string]string{}
			}
			out.Headers[k] = b.secretRef(name, "header_"+k, k+" header")
		default:
			if out.Headers == nil {
				out.Headers = map[string]string{}
			}
			out.Headers[k] = v
		}
	}
	b.addMCP(name, out)
}

func (b *Builder) addMCP(name string, out *MCPOut) {
	if b.rig.MCPServers == nil {
		b.rig.MCPServers = map[string]*MCPOut{}
	}
	b.rig.MCPServers[name] = out
	b.Add("captured", "mcp", name, "")
}

// Finish renders rigfile.yaml.
func (b *Builder) Finish() (*Result, error) {
	mb, err := yaml.Marshal(b.rig)
	if err != nil {
		return nil, err
	}
	header := "# Captured by `rigfile init`. Review before committing: secret VALUES were not captured, but\n# instructions and skills are your own text and may contain private details.\n"
	return &Result{Manifest: append([]byte(header), mb...), Files: b.files, Report: b.report}, nil
}
