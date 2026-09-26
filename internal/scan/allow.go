package scan

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"
)

// Allow is a repo-level allow list (`.rigfile-allow`), the ONLY way to suppress a finding (inline markers
// are off by default, see Options.HonorInlineAllow). It lives in the repo so every exception is visible in
// review. Format, one entry per line, `#` starts a comment:
//
//	<glob>                 allow every finding in matching paths
//	rule:<id> <glob>       allow one rule in matching paths
//	fp:<12 hex>            allow one specific finding (fingerprint printed with the finding)
//
// Globs: `*` matches within a path segment, `**` across segments, `?` one character. A glob without `/`
// matches the file name at any depth. Matching is case-insensitive (macOS/Windows file systems).
type Allow struct {
	entries []allowEntry
}

type allowEntry struct {
	rule, glob, fp string
}

// ParseAllow reads an allow file.
func ParseAllow(r io.Reader) (*Allow, error) {
	a := &Allow{}
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "fp:"):
			fp := strings.TrimSpace(strings.TrimPrefix(line, "fp:"))
			if len(fp) != 12 || strings.Trim(fp, "0123456789abcdef") != "" {
				return nil, fmt.Errorf(".rigfile-allow:%d: fp: wants 12 lowercase hex characters", n)
			}
			a.entries = append(a.entries, allowEntry{fp: fp})
		case strings.HasPrefix(line, "rule:"):
			f := strings.Fields(strings.TrimPrefix(line, "rule:"))
			if len(f) != 2 {
				return nil, fmt.Errorf(".rigfile-allow:%d: want `rule:<id> <glob>`", n)
			}
			a.entries = append(a.entries, allowEntry{rule: f[0], glob: strings.ToLower(f[1])})
		default:
			a.entries = append(a.entries, allowEntry{glob: strings.ToLower(line)})
		}
	}
	return a, sc.Err()
}

// Filter drops the allowed findings and returns the rest.
func (a *Allow) Filter(in []Finding) []Finding {
	if a == nil || len(a.entries) == 0 {
		return in
	}
	var out []Finding
outer:
	for _, f := range in {
		p := strings.ToLower(f.Path)
		for _, e := range a.entries {
			switch {
			case e.fp != "":
				if e.fp == f.Fingerprint {
					continue outer
				}
			case e.rule != "" && e.rule != f.RuleID:
			default:
				if globMatch(e.glob, p) {
					continue outer
				}
			}
		}
		out = append(out, f)
	}
	return out
}

func globMatch(glob, p string) bool {
	if !strings.Contains(glob, "/") {
		return segMatch(glob, path.Base(p))
	}
	return matchSegs(strings.Split(strings.TrimPrefix(glob, "/"), "/"), strings.Split(p, "/"))
}

func matchSegs(g, p []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			if len(g) == 1 {
				return true
			}
			for i := 0; i <= len(p); i++ {
				if matchSegs(g[1:], p[i:]) {
					return true
				}
			}
			return false
		}
		if len(p) == 0 || !segMatch(g[0], p[0]) {
			return false
		}
		g, p = g[1:], p[1:]
	}
	return len(p) == 0
}

func segMatch(pat, s string) bool {
	ok, err := path.Match(pat, s)
	return err == nil && ok
}
