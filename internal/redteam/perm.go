// Package redteam simulates how Claude Code applies permission rules (docs/targets/claude-code.md §8, §12.3)
// so the red-team suite can ask "would this attack be blocked by the rules base-secure wrote?" without a live
// Claude Code. It follows the documented semantics only, and says so wherever it approximates: the live
// procedure in docs/red-team.md is what confirms them against the real product.
package redteam

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// Verdict of a rule set for one action.
type Verdict int

// Verdicts, strongest last.
const (
	None Verdict = iota // no rule matches: Claude Code's normal permission flow applies
	Ask
	Deny
)

func (v Verdict) String() string { return [...]string{"none", "ask", "deny"}[v] }

// Rules is the deny/ask/allow lists of a settings file, in Claude Code's native syntax.
type Rules struct{ Deny, Ask, Allow []string }

// LoadRules reads permissions.{deny,ask,allow} from settings.json.
func LoadRules(settings []byte) (Rules, error) {
	var d struct {
		Permissions struct{ Deny, Ask, Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal(settings, &d); err != nil {
		return Rules{}, err
	}
	return Rules{d.Permissions.Deny, d.Permissions.Ask, d.Permissions.Allow}, nil
}

// Bash evaluates a shell command. Deny and ask apply when ANY subcommand matches, including commands nested
// in subshells, `$(…)` and backticks; wrappers (timeout, time, nice, nohup, stdbuf, command, builtin, noglob)
// and leading assignments are stripped first; `sh -c '…'`, path-qualified programs and `git -C` are NOT
// unwrapped: that is exactly the vendor's documented gap. Read denies also cover the file arguments of
// cat/head/tail/sed/tee and redirect targets.
func (r Rules) Bash(cmd, home, cwd string) (Verdict, string) {
	subs := splitBash(cmd)
	var ask string
	for _, list := range []struct {
		rules []string
		v     Verdict
	}{{r.Deny, Deny}, {r.Ask, Ask}} {
		for _, sub := range subs {
			for _, rule := range list.rules {
				tool, spec, ok := splitRule(rule)
				if !ok || tool != "Bash" {
					continue
				}
				if bashMatch(spec, sub) {
					if list.v == Deny {
						return Deny, rule
					}
					if ask == "" {
						ask = rule
					}
				}
			}
		}
	}
	for _, sub := range subs {
		fs := strings.Fields(sub)
		if len(fs) == 0 {
			continue
		}
		switch fs[0] {
		case "cat", "head", "tail", "sed", "tee":
			for _, a := range fs[1:] {
				if strings.HasPrefix(a, "-") {
					continue
				}
				if v, rule := r.Path("Read", a, home, cwd); v == Deny {
					return Deny, rule
				}
			}
		}
		for i, a := range fs { // redirect targets: `< file`, `> file`, `2> file`
			if (strings.HasPrefix(a, "<") || strings.HasPrefix(a, ">")) && len(a) == 1 && i+1 < len(fs) {
				if v, rule := r.Path("Read", fs[i+1], home, cwd); v == Deny {
					return Deny, rule
				}
			}
		}
	}
	if ask != "" {
		return Ask, ask
	}
	return None, ""
}

// Path evaluates a Read/Edit rule set for one path (tool is "Read" or "Edit"). A Read deny also covers Edit.
func (r Rules) Path(tool, p, home, cwd string) (Verdict, string) {
	abs := absPath(p, home, cwd)
	best, by := None, ""
	consider := func(v Verdict, rule string) {
		if v > best {
			best, by = v, rule
		}
	}
	for _, l := range []struct {
		rules []string
		v     Verdict
	}{{r.Ask, Ask}, {r.Deny, Deny}} {
		for _, rule := range l.rules {
			t, spec, ok := splitRule(rule)
			if !ok || (t != tool && !(tool == "Edit" && t == "Read" && l.v == Deny)) {
				continue
			}
			if pathMatch(spec, abs, home, cwd) {
				consider(l.v, rule)
			}
		}
	}
	return best, by
}

func absPath(p, home, cwd string) string {
	p = strings.NewReplacer("${HOME}", "~", "$HOME", "~").Replace(strings.ReplaceAll(p, `\`, "/"))
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return path.Join(home, p[2:])
	case strings.HasPrefix(p, "/"):
		return path.Clean(p)
	}
	return path.Join(cwd, p)
}

var ruleRe = regexp.MustCompile(`^([A-Za-z_]+)\((.*)\)$`)

func splitRule(r string) (tool, spec string, ok bool) {
	m := ruleRe.FindStringSubmatch(r)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// pathMatch implements the documented path-rule anchors for USER settings: `//abs`, `~/x`, `/x` (relative to
// ~/.claude), `./x` or `x` (relative to the working directory) and gitignore-style `**`/`*`.
func pathMatch(spec, abs, home, cwd string) bool {
	anyDepth := false
	switch {
	case strings.HasPrefix(spec, "//"):
		spec = spec[1:]
	case strings.HasPrefix(spec, "~/"):
		spec = path.Join(home, spec[2:])
	case strings.HasPrefix(spec, "/"):
		spec = path.Join(home, ".claude", spec[1:])
	case strings.HasPrefix(spec, "**/"):
		anyDepth = true
		spec = spec[3:]
	default:
		spec = path.Join(cwd, strings.TrimPrefix(spec, "./"))
	}
	re := globRegexp(spec)
	if anyDepth {
		return regexp.MustCompile(`(^|.*/)` + globBody(spec) + `$`).MatchString(abs)
	}
	return re.MatchString(abs)
}

func globBody(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

func globRegexp(g string) *regexp.Regexp { return regexp.MustCompile("^" + globBody(g) + "$") }

// --- bash ----------------------------------------------------------------------------------------------

var (
	wrapperRe = regexp.MustCompile(`^(timeout\s+\S+|time|nice(\s+-n\s+\S+)?|nohup|stdbuf(\s+-\S+)*|command|builtin|noglob|xargs)\s+`)
	assignRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*\s+`)
)

// splitBash splits on &&, ||, ;, |, |&, & and newlines (outside quotes), also extracts the insides of
// `$(…)`, backticks and ( … ), and strips assignments and wrappers.
func splitBash(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, normalize(t))
		}
		cur.Reset()
	}
	for _, c := range cmd {
		switch {
		case quote != 0:
			cur.WriteRune(c)
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
			cur.WriteRune(c)
		case c == ';' || c == '\n' || c == '|' || c == '&' || c == '(' || c == ')' || c == '`':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

func normalize(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "$"))
	for {
		before := s
		s = strings.TrimSpace(assignRe.ReplaceAllString(s, ""))
		s = strings.TrimSpace(wrapperRe.ReplaceAllString(s, ""))
		if s == before {
			return s
		}
	}
}

// bashMatch: `*` stands for any text; a trailing ` *` also matches the bare command; `:*` at the end is the
// same as ` *`.
func bashMatch(spec, cmd string) bool {
	spec = strings.TrimSuffix(spec, ":*") + map[bool]string{true: " *", false: ""}[strings.HasSuffix(spec, ":*")]
	re := regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(spec), `\*`, ".*") + "$")
	if re.MatchString(cmd) {
		return true
	}
	if strings.HasSuffix(spec, " *") && strings.Count(spec, "*") == 1 {
		return cmd == strings.TrimSuffix(spec, " *")
	}
	return false
}
