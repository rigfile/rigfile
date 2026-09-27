package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Level of a Problem. Errors block plan/apply; warnings are shown. Some warnings become errors when
// publishing publicly (docs/merge-semantics.md Appendix A rule 2, plan §6.2).
type Level string

const (
	Error Level = "error"
	Warn  Level = "warning"
)

// Problem is one finding from Check.
type Problem struct {
	Level Level
	Where string // e.g. "skills[2]" or "mcp_servers.alpaca"
	Msg   string
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s: %s", p.Level, p.Where, p.Msg) }

// HasErrors reports whether any problem is an error.
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Level == Error {
			return true
		}
	}
	return false
}

// PathInside resolves a rig-relative path against dir and proves the result stays inside dir even
// after symlink resolution (a rig must not be able to point a skill at ~/.ssh with a symlink).
// The returned path is the resolved absolute path. The target must exist.
func PathInside(dir, rel string) (string, error) {
	rootReal, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(rootReal, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
		return "", fmt.Errorf("%q resolves outside the rig directory", rel)
	}
	return real, nil
}

// Check runs the validations JSON Schema cannot express (docs/merge-semantics.md Appendix A) for one
// manifest in directory dir. It never mutates anything.
func Check(l *Loaded) []Problem {
	var ps []Problem
	add := func(level Level, where, format string, a ...any) {
		ps = append(ps, Problem{level, where, fmt.Sprintf(format, a...)})
	}
	m, dir := l.M, l.Dir

	// ---- a path that is `private:` (synced only between your own machines, docs/private-sync.md) must not also be something the
	// rig ships: publishing would send it to everyone ----
	if len(m.Private) > 0 {
		var shipped []struct{ where, path string }
		for i, x := range m.Instructions {
			shipped = append(shipped, struct{ where, path string }{fmt.Sprintf("instructions[%d].file", i), x.File})
		}
		for i, x := range m.Skills {
			if x.Path != "" {
				shipped = append(shipped, struct{ where, path string }{fmt.Sprintf("skills[%d].path", i), x.Path})
			}
		}
		for i, x := range m.Agents {
			shipped = append(shipped, struct{ where, path string }{fmt.Sprintf("agents[%d].path", i), x.Path})
		}
		for i, x := range m.Commands {
			shipped = append(shipped, struct{ where, path string }{fmt.Sprintf("commands[%d].path", i), x.Path})
		}
		for _, sh := range shipped {
			for _, pv := range m.Private {
				a, b := strings.TrimSuffix(path.Clean(sh.path), "/"), strings.TrimSuffix(path.Clean(pv), "/")
				if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
					add(Error, sh.where, "%s is listed under `private:` and also shipped by the rig: publishing would send your private files to everyone. Move it out of private:, or stop referencing it", sh.path)
				}
			}
		}
	}

	// ---- duplicate identities, incl. case-insensitive collisions (macOS/Windows file systems) ----
	dup := func(cat string, keys []string) {
		seen, seenFold := map[string]int{}, map[string]string{}
		for i, k := range keys {
			where := fmt.Sprintf("%s[%d]", cat, i)
			if _, ok := seen[k]; ok {
				add(Error, where, "duplicate id %q", k)
				continue
			}
			seen[k] = i
			if windowsReserved(k) {
				add(Error, where, "id %q is a reserved device name on Windows (CON, NUL, COM1, ...) and cannot be a file or directory name there", k)
			}
			if prev, ok := seenFold[strings.ToLower(k)]; ok && prev != k {
				add(Error, where, "id %q differs from %q only by case; they collide on case-insensitive file systems", k, prev)
			}
			seenFold[strings.ToLower(k)] = k
		}
	}
	var keys []string
	for _, x := range m.Instructions {
		keys = append(keys, x.Key())
	}
	dup("instructions", keys)
	keys = nil
	for _, x := range m.Skills {
		keys = append(keys, x.Key())
	}
	dup("skills", keys)
	keys = nil
	for _, x := range m.Agents {
		keys = append(keys, x.Key())
	}
	dup("agents", keys)
	keys = nil
	for _, x := range m.Commands {
		keys = append(keys, x.Key())
	}
	dup("commands", keys)
	keys = nil
	for _, x := range m.Hooks {
		keys = append(keys, x.Key())
	}
	dup("hooks", keys)
	// A command and a skill with the same name create the same /name in Claude Code (§2.4).
	skillKeys := map[string]bool{}
	for _, s := range m.Skills {
		skillKeys[s.Key()] = true
	}
	for i, c := range m.Commands {
		if skillKeys[c.Key()] {
			add(Warn, fmt.Sprintf("commands[%d]", i), "command %q has the same name as a skill: both create /%s", c.Key(), c.Key())
		}
	}

	// ---- files referenced from the rig ----
	file := func(where, rel string) (abs string, ok bool) {
		abs, err := PathInside(dir, rel)
		if err != nil {
			if os.IsNotExist(unwrap(err)) {
				add(Error, where, "%s does not exist in the rig", rel)
			} else {
				add(Error, where, "%v", err)
			}
			return "", false
		}
		return abs, true
	}
	for i, x := range m.Instructions {
		if abs, ok := file(fmt.Sprintf("instructions[%d].file", i), x.File); ok {
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				add(Error, fmt.Sprintf("instructions[%d].file", i), "%s is a directory, want a Markdown file", x.File)
			}
		}
	}
	for i, x := range m.Skills {
		where := fmt.Sprintf("skills[%d]", i)
		if x.Path == "" {
			continue // external ref: pin checked below
		}
		abs, ok := file(where+".path", x.Path)
		if !ok {
			continue
		}
		checkSkillDir(abs, where, add)
	}
	for i, x := range m.Agents {
		file(fmt.Sprintf("agents[%d].path", i), x.Path)
	}
	for i, x := range m.Commands {
		file(fmt.Sprintf("commands[%d].path", i), x.Path)
	}
	for i, h := range m.Hooks {
		where := fmt.Sprintf("hooks[%d]", i)
		runs := map[string]string{}
		if h.Run.Single != "" {
			runs["*"] = h.Run.Single
		}
		for osName, r := range h.Run.PerOS {
			runs[osName] = r
		}
		for osName, r := range runs {
			if _, isBuiltin := Builtin(r); isBuiltin {
				continue
			}
			file(fmt.Sprintf("%s.run(%s)", where, osName), r)
		}
	}

	// ---- pins (warnings locally; rejected on public publish) ----
	for i, s := range m.Skills {
		if s.Ref != "" && !strings.Contains(s.Ref, "@") {
			add(Warn, fmt.Sprintf("skills[%d].ref", i), "external skill %q is not pinned to a version", s.Ref)
		}
	}
	names := make([]string, 0, len(m.MCPServers))
	for n := range m.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	declared := map[string]bool{}
	for k := range m.Secrets {
		declared[k] = true
	}
	for _, n := range names {
		s := m.MCPServers[n]
		where := "mcp_servers." + n
		if !s.IsRemote() {
			if msg := pinProblem(s.Command, s.Args); msg != "" {
				add(Warn, where, "%s", msg)
			}
		}
		for _, v := range s.Env {
			if ref, ok := SecretRef(v); ok && !declared[ref] {
				add(Warn, where, "uses secret://%s which is not declared under secrets:", ref)
			}
		}
		if ref, ok := SecretRef(s.BearerToken); ok && !declared[ref] {
			add(Warn, where, "uses secret://%s which is not declared under secrets:", ref)
		}
		// declared secret hosts should overlap the server's declared egress (Appendix A rule 8)
		if len(s.Network.Allow) > 0 {
			for k, v := range s.Env {
				if ref, ok := SecretRef(v); ok {
					if d, ok := m.Secrets[ref]; ok && len(d.Hosts) > 0 && !hostsOverlap(d.Hosts, s.Network.Allow) {
						add(Warn, where, "%s: secret %s is bound to %v but the server's network.allow is %v", k, ref, d.Hosts, s.Network.Allow)
					}
				}
			}
		}
	}

	// ---- cross references ----
	if f := m.Routing.FallbackOnLimit; f != "" {
		if _, ok := m.Models[f]; !ok {
			add(Error, "routing.fallback_on_limit", "%q is not defined under models:", f)
		}
	}
	for gname, g := range m.Gateways {
		for model := range g.Routes {
			if _, ok := m.Models[model]; !ok {
				add(Error, "gateways."+gname+".routes", "%q is not defined under models:", model)
			}
		}
	}
	agentKeys := map[string]bool{}
	for _, a := range m.Agents {
		agentKeys[a.Key()] = true
	}
	for i, t := range m.Routing.LocalFor {
		if name, ok := strings.CutPrefix(t, "subagent:"); ok && !agentKeys[name] {
			add(Warn, fmt.Sprintf("routing.local_for[%d]", i), "subagent %q is not defined in this rig's agents (it may come from another layer)", name)
		}
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Level == Error && ps[j].Level != Error })
	return ps
}

func unwrap(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}

var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n`)

// checkSkillDir requires <dir>/SKILL.md with `name` and `description` frontmatter (Agent Skills format).
func checkSkillDir(abs, where string, add func(Level, string, string, ...any)) {
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		add(Error, where+".path", "a skill must be a directory containing SKILL.md")
		return
	}
	b, err := os.ReadFile(filepath.Join(abs, "SKILL.md"))
	if err != nil {
		add(Error, where+".path", "no SKILL.md in the skill directory")
		return
	}
	m := frontmatterRe.FindSubmatch(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
	if m == nil {
		add(Error, where+".path", "SKILL.md must start with YAML frontmatter (---)")
		return
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		add(Error, where+".path", "SKILL.md frontmatter is not valid YAML: %v", err)
		return
	}
	if fm.Name == "" || fm.Description == "" {
		add(Error, where+".path", "SKILL.md frontmatter needs both name and description")
	}
}

// pinProblem returns a message when a stdio server's package is not pinned to an exact version. It
// understands npx/bunx (pkg@x.y.z), uvx (pkg==x.y.z or pkg@x.y.z) and `pipx run`. For any other
// launcher it says it cannot verify (docs/merge-semantics.md Appendix A recommends a structured
// `package:` field to make this exact).
func pinProblem(cmd string, args []string) string {
	base := strings.ToLower(filepath.Base(cmd))
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".exe")
	firstArg := func(skip map[string]bool) string {
		for i := 0; i < len(args); i++ {
			a := args[i]
			if skip[a] {
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			return a
		}
		return ""
	}
	exactVer := regexp.MustCompile(`[@=]=?v?\d+\.\d+(\.\d+)?([-+][0-9A-Za-z.-]+)?$`)
	switch base {
	case "npx", "bunx", "pnpx":
		pkg := firstArg(nil)
		if pkg == "" {
			return "npx without a package argument; cannot verify a version pin"
		}
		// scoped names start with @: the version @ comes after the first slash
		v := pkg
		if strings.HasPrefix(v, "@") {
			if i := strings.Index(v, "/"); i > 0 {
				v = v[i:]
			}
		}
		if !strings.Contains(v, "@") || !exactVer.MatchString(pkg) {
			return fmt.Sprintf("package %q is not pinned to an exact version (want name@1.2.3)", pkg)
		}
	case "uvx":
		pkg := firstArg(map[string]bool{"--from": true})
		if pkg == "" || !exactVer.MatchString(pkg) {
			return fmt.Sprintf("package %q is not pinned to an exact version (want name==1.2.3)", pkg)
		}
	case "pipx":
		if len(args) > 0 && args[0] == "run" {
			var rest []string
			for _, a := range args[1:] {
				if !strings.HasPrefix(a, "-") {
					rest = append(rest, a)
				}
			}
			if len(rest) == 0 || !exactVer.MatchString(rest[0]) {
				return "pipx run package is not pinned to an exact version (want name==1.2.3)"
			}
		}
	case "rigfile":
		// already wrapped by `rigfile exec ... -- <real command>`: check the inner command
		for i, a := range args {
			if a == "--" && i+1 < len(args) {
				return pinProblem(args[i+1], args[i+2:])
			}
		}
	default:
		return fmt.Sprintf("cannot verify the version pin of launcher %q", cmd)
	}
	return ""
}

func hostsOverlap(secretHosts, allow []string) bool {
	for _, s := range secretHosts {
		for _, a := range allow {
			if hostCovers(s, a) || hostCovers(a, s) {
				return true
			}
		}
	}
	return false
}

// hostCovers reports whether pattern p (host, or *.suffix) covers host/pattern h.
func hostCovers(p, h string) bool {
	if p == h {
		return true
	}
	if strings.HasPrefix(p, "*.") {
		suffix := p[1:] // ".example.com"
		h = strings.TrimPrefix(h, "*")
		return strings.HasSuffix(h, suffix) && len(h) > len(suffix)
	}
	return false
}

// HostCovers is exported for the merge engine (secrets `hosts` may only narrow).
func HostCovers(p, h string) bool { return hostCovers(p, h) }

// windowsReserved reports whether a name (ignoring any extension) is one of the DOS device names Windows refuses
// as a file name, or ends in a dot or space, which Windows silently strips.
func windowsReserved(name string) bool {
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return true
	}
	base := strings.ToLower(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "con", "prn", "aux", "nul":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}
