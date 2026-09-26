package scan

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one detection. It deliberately has NO field that holds the matched text: callers cannot
// print, log or persist a secret through it (plan §7.3, §8.1b). The Fingerprint identifies a finding
// for allow-lists without revealing it.
type Finding struct {
	Kind        string // "content" | "name" | "size"
	RuleID      string
	Description string
	Path        string
	Line, End   int    // 1-based, inclusive; 0 for name/size findings
	Column      int    // 1-based byte column of the match start; 0 for name/size findings
	Fingerprint string // first 12 hex chars of sha256(rule \0 path \0 secret)
	Commit      string // set by callers that scan history (short commit id); empty for working-tree text
}

// String renders "path:line: rule: description [fingerprint]" and never the value.
func (f Finding) String() string {
	loc := f.Path
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
	}
	return fmt.Sprintf("%s: %s: %s [%s]", loc, f.RuleID, f.Description, f.Fingerprint)
}

// Options tune a Scanner.
type Options struct {
	Rules        *Ruleset // nil = the embedded gitleaks rules
	MaxFileBytes int      // files larger than this are reported as a "size" finding and not scanned; 0 = 10 MiB
	// HonorInlineAllow makes `gitleaks:allow` / `rigfile:allow` on a line suppress findings on that line.
	// Off by default: an agent that writes a secret can add the marker itself, so the only allow mechanism
	// is the repo-level .rigfile-allow file (allow.go), which shows up in review.
	HonorInlineAllow bool
	// SkipNames disables the file-name blocklist (used when scanning command text or diffs, not files).
	SkipNames bool
}

// DefaultMaxFileBytes is the size limit (plan §8.1b): 10 MB.
const DefaultMaxFileBytes = 10 << 20

// Scanner scans blobs of text. Safe for concurrent use.
type Scanner struct {
	rs   *Ruleset
	opts Options
}

// New builds a Scanner.
func New(o Options) (*Scanner, error) {
	rs := o.Rules
	if rs == nil {
		var err error
		if rs, err = DefaultRuleset(); err != nil {
			return nil, err
		}
	}
	if o.MaxFileBytes == 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	return &Scanner{rs: rs, opts: o}, nil
}

// ScanFile checks one file: its name (blocklist), its size, and (unless binary) its content.
// path is forward-slash or native; it is only used for names, allowlists and reporting.
func (s *Scanner) ScanFile(path string, data []byte) []Finding {
	path = strings.ReplaceAll(path, `\`, "/")
	out := s.ScanName(path, len(data))
	if len(data) > s.opts.MaxFileBytes || isBinary(data) {
		return out
	}
	return append(out, s.ScanText(path, string(data))...)
}

// ScanName checks only a file's name and size, for callers that decided not to read a huge blob.
func (s *Scanner) ScanName(path string, size int) []Finding {
	path = strings.ReplaceAll(path, `\`, "/")
	var out []Finding
	if !s.opts.SkipNames && IsSensitiveFilename(path) {
		out = append(out, Finding{Kind: "name", RuleID: "sensitive-filename", Path: path,
			Description: "file name looks like a credentials or key file", Fingerprint: fingerprint("sensitive-filename", path, path)})
	}
	if size > s.opts.MaxFileBytes {
		out = append(out, Finding{Kind: "size", RuleID: "file-too-large", Path: path,
			Description: fmt.Sprintf("file is larger than %d MB", s.opts.MaxFileBytes>>20), Fingerprint: fingerprint("file-too-large", path, path)})
	}
	return out
}

// MaxFileBytes is the size limit in effect.
func (s *Scanner) MaxFileBytes() int { return s.opts.MaxFileBytes }

// ScanText scans text (a file's contents, a diff, a shell command). name is used for path-scoped rules
// and reporting; pass "" for text that is not a file.
func (s *Scanner) ScanText(name, text string) []Finding {
	return s.scanText(name, text, 0, nil)
}

func (s *Scanner) scanText(name, text string, depth int, sink *[]redSpan) []Finding {
	name = strings.ReplaceAll(name, `\`, "/")
	if text == "" && name == "" {
		return nil
	}
	if s.pathAllowedGlobally(name) {
		return nil
	}
	lower := asciiLower(text)
	present := s.rs.kw.present(lower)
	has := func(k string) bool { return present[s.rs.kwIndex[k]] }
	lines := newLineIndex(text)

	var out []Finding
	type raw struct {
		f          Finding
		secret     string
		rule       *rule
		start, end int // byte span of the secret in text
	}
	var raws []raw
	for _, r := range s.rs.rules {
		if r.pathRe != nil && name != "" && !r.pathRe.MatchString(name) {
			continue
		}
		if r.pathRe != nil && r.re == nil { // path-only rule
			if name != "" && r.pathRe.MatchString(name) && !s.ruleSkipsPath(r, name) {
				out = append(out, Finding{Kind: "name", RuleID: r.id, Description: r.description, Path: name, Fingerprint: fingerprint(r.id, name, name)})
			}
			continue
		}
		if r.pathRe != nil && name == "" {
			continue // a path-scoped rule cannot apply to pathless text
		}
		if len(r.keywords) > 0 {
			ok := false
			for _, k := range r.keywords {
				if has(k) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		if s.ruleSkipsPath(r, name) {
			continue
		}
		for _, idx := range findMatches(r, text, lower) {
			match := strings.Trim(text[idx[0]:idx[1]], "\n")
			end := idx[0] + len(match)
			secret := match
			if g := r.re.FindStringSubmatch(match); len(g) >= 2 {
				if r.secretGroup > 0 {
					if len(g) <= r.secretGroup {
						continue
					}
					secret = g[r.secretGroup]
				} else {
					for _, x := range g[1:] {
						if len(x) > 0 {
							secret = x
							break
						}
					}
				}
			}
			if r.window > 0 { // generic key/value rule: the value must be on the same line as its name
				// gitleaks' pattern lets `[\s'"=]{0,5}` cross a newline, so `API_KEY=` (empty) followed by the next
				// line's `SECRET_KEY=change-me` reads as one assignment. Found by the .env.example corpus class.
				if i := strings.Index(match, secret); i > 0 && strings.Contains(match[:i], "\n") {
					continue
				}
			}
			if r.entropy != 0 && shannonEntropy(secret) <= r.entropy {
				continue
			}
			startLine, endLine, col := lines.locate(idx[0], end)
			line := lines.text(text, startLine, endLine)
			if s.opts.HonorInlineAllow && (strings.Contains(line, "gitleaks:allow") || strings.Contains(line, "rigfile:allow")) {
				continue
			}
			if allowed(s.rs.global, name, secret, match, line) || allowed(r.allow, name, secret, match, line) {
				continue
			}
			ss := idx[0]
			if k := strings.Index(text[idx[0]:end], secret); k >= 0 {
				ss = idx[0] + k
			}
			raws = append(raws, raw{Finding{Kind: "content", RuleID: r.id, Description: r.description, Path: name,
				Line: startLine, End: endLine, Column: col, Fingerprint: fingerprint(r.id, name, secret)}, secret, r, ss, ss + len(secret)})
		}
	}
	// gitleaks' dedupe: a "generic" finding is dropped when a specific rule found the same secret on that line.
	for _, a := range raws {
		keep := true
		if a.rule.generic {
			for _, b := range raws {
				if !b.rule.generic && a.f.Line == b.f.Line && a.f.RuleID != b.f.RuleID && strings.Contains(b.secret, a.secret) {
					keep = false
					break
				}
			}
		}
		if keep {
			out = append(out, a.f)
			if sink != nil {
				*sink = append(*sink, redSpan{a.start, a.end, a.f.RuleID})
			}
		}
	}
	if depth < maxDecodeDepth {
		out = append(out, s.scanDecoded(name, text, lines, depth, sink)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

// pathAllowedGlobally mirrors gitleaks' checkCommitOrPathAllowed for the global allowlist.
func (s *Scanner) pathAllowedGlobally(path string) bool {
	return pathSkipped(s.rs.global, path)
}

func (s *Scanner) ruleSkipsPath(r *rule, path string) bool { return pathSkipped(r.allow, path) }

func pathSkipped(lists []*allowlist, path string) bool {
	if path == "" {
		return false
	}
	for _, a := range lists {
		pathHit := anyMatch(a.paths, path)
		if a.and {
			if len(a.regexes) > 0 || len(a.stopWords) > 0 {
				continue // decided per finding
			}
			ok := true
			if a.hasCommits {
				ok = false // no commit context: a commit condition can never hold
			}
			if len(a.paths) > 0 && !pathHit {
				ok = false
			}
			if ok && (len(a.paths) > 0 || a.hasCommits) {
				return true
			}
		} else if pathHit {
			return true
		}
	}
	return false
}

// allowed mirrors gitleaks' checkFindingAllowed.
func allowed(lists []*allowlist, path, secret, match, line string) bool {
	for _, a := range lists {
		target := secret
		switch a.regexTarget {
		case "match":
			target = match
		case "line":
			target = line
		}
		regexHit := anyMatch(a.regexes, target)
		stopHit := containsStop(a.stopWords, secret)
		if a.and {
			var checks []bool
			if a.hasCommits {
				checks = append(checks, false)
			}
			if len(a.paths) > 0 {
				checks = append(checks, anyMatch(a.paths, path))
			}
			if len(a.regexes) > 0 {
				checks = append(checks, regexHit)
			}
			if len(a.stopWords) > 0 {
				checks = append(checks, stopHit)
			}
			all := true
			for _, c := range checks {
				all = all && c
			}
			if all && len(checks) > 0 {
				return true
			}
		} else if regexHit || stopHit {
			return true
		}
	}
	return false
}

// redSpan is a byte range of scanned text to blank out.
type redSpan struct {
	start, end int
	rule       string
}

// Redact returns text with every detected secret replaced by [REDACTED:<rule>], and the findings. Encoded
// secrets (base64 and friends) are redacted as the whole encoded segment. It is for text that is about to
// enter a model's context (PostToolUse hooks); findings carry no values, as always.
func (s *Scanner) Redact(text string) (string, []Finding) {
	var spans []redSpan
	fs := s.scanText("", text, 0, &spans)
	if len(spans) == 0 {
		return text, fs
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var b strings.Builder
	pos := 0
	for _, sp := range spans {
		if sp.start < pos { // overlaps an earlier span
			if sp.end > pos {
				pos = sp.end
			}
			continue
		}
		b.WriteString(text[pos:sp.start])
		b.WriteString("[REDACTED:" + sp.rule + "]")
		pos = sp.end
	}
	b.WriteString(text[pos:])
	return b.String(), fs
}
