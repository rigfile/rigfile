package scan

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// The rule data is gitleaks' default config (MIT, see NOTICE), embedded at a pinned tag (ADR 0003). We
// evaluate it ourselves (engine.go) with the same semantics as gitleaks v8.30.1's detector: keyword
// prefilter, regex, secretGroup, entropy, rule and global allowlists. Refresh with
// scripts/update-gitleaks-rules.sh; TestEmbeddedRulesMatchRecordedHash fails on any silent edit.

//go:embed rules/gitleaks.toml
var gitleaksTOML []byte

//go:embed rules/gitleaks.toml.sha256
var gitleaksSHA string

//go:embed rules/VERSION
var gitleaksVersion string

// rigfileTOML holds Rigfile's own additions (gitleaks format), reviewed in this repo.
//
//go:embed rules/rigfile.toml
var rigfileTOML []byte

// RulesVersion is the gitleaks tag the embedded rules come from.
func RulesVersion() string { return strings.TrimSpace(gitleaksVersion) }

type tomlAllow struct {
	Description string   `toml:"description"`
	Condition   string   `toml:"condition"`
	RegexTarget string   `toml:"regexTarget"`
	Paths       []string `toml:"paths"`
	Regexes     []string `toml:"regexes"`
	StopWords   []string `toml:"stopwords"`
	Commits     []string `toml:"commits"`
}

type tomlRule struct {
	ID          string      `toml:"id"`
	Description string      `toml:"description"`
	Regex       string      `toml:"regex"`
	Path        string      `toml:"path"`
	SecretGroup int         `toml:"secretGroup"`
	Entropy     float64     `toml:"entropy"`
	Keywords    []string    `toml:"keywords"`
	Tags        []string    `toml:"tags"`
	Allowlists  []tomlAllow `toml:"allowlists"`
}

type tomlConfig struct {
	Title      string      `toml:"title"`
	MinVersion string      `toml:"minVersion"`
	Allowlist  tomlAllow   `toml:"allowlist"`
	Allowlists []tomlAllow `toml:"allowlists"`
	Rules      []tomlRule  `toml:"rules"`
}

type allowlist struct {
	and         bool
	regexTarget string // "" (secret) | "match" | "line"
	paths       []*regexp.Regexp
	regexes     []*regexp.Regexp
	stopWords   []string // lower-case
	hasCommits  bool     // commit checks never match here (no commit context); kept for AND semantics
}

type rule struct {
	id, description string
	re, pathRe      *regexp.Regexp
	secretGroup     int
	entropy         float64
	keywords        []string // lower-case
	allow           []*allowlist
	generic         bool
	window          int // >0: run the regex only around keyword hits (see windows.go)
}

// Ruleset is a compiled rule set. It is immutable and safe for concurrent use.
type Ruleset struct {
	rules  []*rule
	global []*allowlist
	// Version is the gitleaks tag the rules came from (empty for custom sets).
	Version string

	kwIndex map[string]int
	kw      *acDFA
}

// NumRules reports how many rules are loaded.
func (rs *Ruleset) NumRules() int { return len(rs.rules) }

var (
	defaultOnce sync.Once
	defaultRS   *Ruleset
	defaultErr  error
)

// DefaultRuleset returns the embedded gitleaks rules, compiled once.
func DefaultRuleset() (*Ruleset, error) {
	defaultOnce.Do(func() {
		if got := hashHex(gitleaksTOML); got != strings.TrimSpace(gitleaksSHA) {
			defaultErr = fmt.Errorf("scan: embedded gitleaks rules do not match their recorded hash (got %s)", got[:12])
			return
		}
		defaultRS, defaultErr = ParseRules(gitleaksTOML, rigfileTOML)
		if defaultRS != nil {
			defaultRS.Version = RulesVersion()
		}
	})
	return defaultRS, defaultErr
}

func hashHex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ParseRules compiles a gitleaks-format TOML config. Any rule that Go's regexp cannot compile is an error
// (no rule is silently dropped: a dropped rule is a hole).
func ParseRules(datas ...[]byte) (*Ruleset, error) {
	var c tomlConfig
	for _, data := range datas {
		var one tomlConfig
		if err := toml.Unmarshal(data, &one); err != nil {
			return nil, fmt.Errorf("scan: rules: %w", err)
		}
		c.Allowlists = append(c.Allowlists, one.Allowlist)
		c.Allowlists = append(c.Allowlists, one.Allowlists...)
		c.Rules = append(c.Rules, one.Rules...)
	}
	rs := &Ruleset{}
	compileAllow := func(t tomlAllow, where string) (*allowlist, error) {
		a := &allowlist{and: strings.EqualFold(t.Condition, "AND"), regexTarget: t.RegexTarget, hasCommits: len(t.Commits) > 0}
		if a.regexTarget != "" && a.regexTarget != "match" && a.regexTarget != "line" {
			return nil, fmt.Errorf("scan: %s: unknown regexTarget %q", where, a.regexTarget)
		}
		for _, p := range t.Paths {
			re, err := regexp.Compile(p)
			if err != nil {
				return nil, fmt.Errorf("scan: %s: path %q: %w", where, p, err)
			}
			a.paths = append(a.paths, re)
		}
		for _, p := range t.Regexes {
			re, err := regexp.Compile(p)
			if err != nil {
				return nil, fmt.Errorf("scan: %s: regex %q: %w", where, p, err)
			}
			a.regexes = append(a.regexes, re)
		}
		for _, w := range t.StopWords {
			a.stopWords = append(a.stopWords, strings.ToLower(w))
		}
		return a, nil
	}
	for _, t := range c.Allowlists {
		if len(t.Paths)+len(t.Regexes)+len(t.StopWords)+len(t.Commits) == 0 {
			continue
		}
		a, err := compileAllow(t, "global allowlist")
		if err != nil {
			return nil, err
		}
		rs.global = append(rs.global, a)
	}
	seen := map[string]bool{}
	for _, t := range c.Rules {
		if t.ID == "" || seen[t.ID] {
			return nil, fmt.Errorf("scan: rules: missing or duplicate id %q", t.ID)
		}
		seen[t.ID] = true
		r := &rule{id: t.ID, description: t.Description, secretGroup: t.SecretGroup, entropy: t.Entropy,
			generic: strings.Contains(strings.ToLower(t.ID), "generic"), window: boundedRuleWindow[t.ID]}
		var err error
		if t.Regex != "" {
			if r.re, err = regexp.Compile(t.Regex); err != nil {
				return nil, fmt.Errorf("scan: rule %s: %w", t.ID, err)
			}
		}
		if t.Path != "" {
			if r.pathRe, err = regexp.Compile(t.Path); err != nil {
				return nil, fmt.Errorf("scan: rule %s path: %w", t.ID, err)
			}
		}
		if r.re == nil && r.pathRe == nil {
			return nil, fmt.Errorf("scan: rule %s has neither regex nor path", t.ID)
		}
		for _, k := range t.Keywords {
			r.keywords = append(r.keywords, strings.ToLower(k))
		}
		for _, at := range t.Allowlists {
			a, err := compileAllow(at, "rule "+t.ID)
			if err != nil {
				return nil, err
			}
			r.allow = append(r.allow, a)
		}
		rs.rules = append(rs.rules, r)
	}
	rs.kwIndex = map[string]int{}
	var words []string
	for _, r := range rs.rules {
		for _, k := range r.keywords {
			if _, ok := rs.kwIndex[k]; !ok {
				for i := 0; i < len(k); i++ {
					if k[i] >= 128 {
						return nil, fmt.Errorf("scan: rule %s: keyword %q is not ASCII", r.id, k)
					}
				}
				rs.kwIndex[k] = len(words)
				words = append(words, k)
			}
		}
	}
	rs.kw = buildAC(words)
	return rs, nil
}
