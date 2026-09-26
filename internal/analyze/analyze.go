// Package analyze is Rigfile's static analysis of the code and text a rig carries: scripts, hook and MCP definitions,
// permissions and instructions (docs/trust.md §2). It is a set of heuristics that make the obvious things visible; a
// determined attacker can evade every rule and a benign rig can trip some (docs/analysis-metrics.md has the measured
// numbers). Findings NEVER contain text from the analysed file: the message is a fixed string per rule, so a report
// cannot carry an injection or a secret into the page or terminal that shows it.
package analyze

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Level says how much a finding should worry the reader.
type Level string

const (
	Notice  Level = "notice"  // worth knowing
	Caution Level = "caution" // likely to matter: read before approving
	Danger  Level = "danger"  // almost never legitimate in a shared rig
)

func (l Level) rank() int {
	switch l {
	case Danger:
		return 3
	case Caution:
		return 2
	}
	return 1
}

// Finding is one result. Count is how many places in the file matched (Line is the first).
type Finding struct {
	Level   Level  `json:"level"`
	Rule    string `json:"rule"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Count   int    `json:"count"`
	Message string `json:"message"`
}

// Report is the analysis of one rig.
type Report struct {
	Findings []Finding `json:"findings"`
	Files    int       `json:"files"`
}

// Counts returns how many findings there are per level.
func (r *Report) Counts() map[Level]int {
	c := map[Level]int{}
	for _, f := range r.Findings {
		c[f.Level]++
	}
	return c
}

// Worst is the highest level present ("" when there are no findings).
func (r *Report) Worst() Level {
	var w Level
	for _, f := range r.Findings {
		if f.Level.rank() > w.rank() {
			w = f.Level
		}
	}
	return w
}

// Danger returns the danger-level findings.
func (r *Report) Danger() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Level == Danger {
			out = append(out, f)
		}
	}
	return out
}

// kind is what a file is, for choosing rules.
type kind int

const (
	kOther kind = iota
	kShell
	kPowerShell
	kPython
	kJS
	kBatch
	kText // Markdown and plain text: instructions, skills, agents, commands
	kManifest
	kBinary
)

func (k kind) script() bool {
	return k == kShell || k == kPowerShell || k == kPython || k == kJS || k == kBatch
}

var docNames = map[string]bool{"readme.md": true, "changelog.md": true, "license": true, "license.md": true, "licence": true, "notice": true, "code_of_conduct.md": true}

func classify(name string, data []byte) kind {
	if isBinary(data) {
		return kBinary
	}
	base := strings.ToLower(path.Base(name))
	ext := strings.ToLower(path.Ext(base))
	switch {
	case base == "rigfile.yaml":
		return kManifest
	case ext == ".sh" || ext == ".bash" || ext == ".zsh" || ext == ".ksh":
		return kShell
	case ext == ".ps1" || ext == ".psm1" || ext == ".psd1":
		return kPowerShell
	case ext == ".py":
		return kPython
	case ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".ts":
		return kJS
	case ext == ".bat" || ext == ".cmd":
		return kBatch
	}
	if bytes.HasPrefix(data, []byte("#!")) {
		nl := bytes.IndexByte(data, '\n')
		if nl < 0 {
			nl = len(data)
		}
		sb := string(data[:nl])
		switch {
		case strings.Contains(sb, "python"):
			return kPython
		case strings.Contains(sb, "node") || strings.Contains(sb, "deno") || strings.Contains(sb, "bun"):
			return kJS
		case strings.Contains(sb, "pwsh") || strings.Contains(sb, "powershell"):
			return kPowerShell
		default:
			return kShell
		}
	}
	if ext == ".md" || ext == ".mdc" || ext == ".txt" || ext == ".markdown" {
		if docNames[base] {
			return kOther
		}
		return kText
	}
	return kOther
}

// isBinary reports content that is not text (NUL bytes, or an executable header).
func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	if bytes.Contains(b[:n], []byte{0}) {
		return true
	}
	return !utf8.Valid(b[:n]) && n == len(b)
}

func executableMagic(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		return "elf"
	case bytes.HasPrefix(b, []byte("MZ")):
		return "pe"
	case bytes.HasPrefix(b, []byte{0xcf, 0xfa, 0xed, 0xfe}), bytes.HasPrefix(b, []byte{0xca, 0xfe, 0xba, 0xbe}), bytes.HasPrefix(b, []byte{0xfe, 0xed, 0xfa, 0xcf}):
		return "macho"
	}
	return ""
}

// Files analyses a rig given as rig-relative slash paths to contents.
func Files(files map[string][]byte) *Report {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	rep := &Report{Files: len(files)}
	for _, name := range names {
		data := files[name]
		k := classify(name, data)
		var raw []hit
		switch k {
		case kBinary:
			if m := executableMagic(data); m != "" {
				raw = append(raw, hit{rule: "bin.executable", line: 0})
			} else if !strings.HasPrefix(strings.ToLower(path.Ext(name)), ".") {
				raw = append(raw, hit{rule: "bin.opaque", line: 0})
			} else if !benignBinaryExt[strings.ToLower(path.Ext(name))] {
				raw = append(raw, hit{rule: "bin.opaque", line: 0})
			}
		case kManifest:
			raw = manifestHits(name, data)
			raw = append(raw, textHits(data, hiddenRules)...)
		case kText:
			raw = textHits(data, append(append([]rule{}, hiddenRules...), instructionRules...))
		case kOther:
			raw = textHits(data, hiddenRules)
		default:
			raw = scriptHits(k, data)
			raw = append(raw, textHits(data, hiddenRules)...)
		}
		rep.Findings = append(rep.Findings, aggregate(name, raw)...)
	}
	sortFindings(rep.Findings)
	return rep
}

var benignBinaryExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true, ".pdf": true, ".woff": true, ".woff2": true, ".ttf": true, ".svg": true, ".gz": true, ".zip": true}

// Dir analyses every regular file under dir (symlinks are skipped).
func Dir(dir string) (*Report, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if len(b) > 2<<20 {
			b = b[:2<<20]
		}
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Files(files), nil
}

// hit is a rule match before aggregation.
type hit struct {
	rule string
	line int
}

func aggregate(file string, hits []hit) []Finding {
	type agg struct {
		line, count int
	}
	m := map[string]*agg{}
	var order []string
	for _, h := range hits {
		a := m[h.rule]
		if a == nil {
			a = &agg{line: h.line}
			m[h.rule] = a
			order = append(order, h.rule)
		}
		a.count++
		if h.line != 0 && (a.line == 0 || h.line < a.line) {
			a.line = h.line
		}
	}
	// a credential read and a network call in the SAME file is the shape of exfiltration
	src := m["cred.access"]
	if src == nil {
		src = m["cred.env-dump"]
	}
	if src != nil && m["net.http"] != nil && m["combo.credential-exfil"] == nil {
		m["combo.credential-exfil"] = &agg{line: src.line, count: 1}
		order = append(order, "combo.credential-exfil")
	}
	var out []Finding
	for _, id := range order {
		def, ok := ruleInfo[id]
		if !ok {
			continue
		}
		a := m[id]
		lvl := def.level
		if id == "net.http" && m["net.exfil-host"] != nil {
			continue // the exfil-host finding already covers it, at a higher level
		}
		msg := def.msg
		if a.count > 1 {
			msg += fmt.Sprintf(" (%d places)", a.count)
		}
		out = append(out, Finding{Level: lvl, Rule: id, File: file, Line: a.line, Count: a.count, Message: msg})
	}
	return out
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Level.rank() != fs[j].Level.rank() {
			return fs[i].Level.rank() > fs[j].Level.rank()
		}
		if fs[i].File != fs[j].File {
			return fs[i].File < fs[j].File
		}
		return fs[i].Rule < fs[j].Rule
	})
}

// rule is one pattern.
type rule struct {
	id  string
	re  *regexp.Regexp
	neg bool // ignore a match that is the object of a prohibition ("do not skip the hooks")
}

var negated = regexp.MustCompile(`(?i)(\b(do not|don'?t|never|must not|mustn'?t|should not|shouldn'?t|cannot|can'?t|avoid|refuse to|not)\s+(\w+\s+){0,2})$`)

type ruleDef struct {
	level Level
	msg   string
}

func textHits(data []byte, rules []rule) []hit {
	var out []hit
	s := string(data)
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		for _, r := range rules {
			loc := r.re.FindStringIndex(ln)
			if loc == nil {
				continue
			}
			if r.neg && negated.MatchString(ln[:loc[0]]) {
				continue
			}
			out = append(out, hit{rule: r.id, line: i + 1})
		}
	}
	// hidden characters may sit anywhere; the line-by-line pass above already covers them
	return out
}
