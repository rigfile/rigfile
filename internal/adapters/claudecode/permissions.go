// Package claudecode is the Claude Code adapter (spike subset: permissions only).
// Vendor facts (formats, path anchors, evaluation order) are documented with sources in
// docs/targets/claude-code.md; section numbers below refer to that file.
package claudecode

import (
	"fmt"
	"slices"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/jsonedit"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// Target is this adapter's name in manifest `targets:` lists.
const Target = "claude-code"

// lists in evaluation-relevant order (deny first: it is the list that must never be lost).
var lists = []string{"deny", "ask", "allow"}

// TranslateRule converts one canonical rule to Claude Code's native rule string.
// Path anchors (§8): in Claude Code a leading "/" means "relative to the settings file", NOT the
// filesystem root, so absolute paths must be written "//path" and home paths "~/path".
// ok is false when the rule does not apply to this OS/target (os:/targets: filters).
func TranslateRule(pi *platform.Info, r manifest.PermissionRule) (rule string, ok bool, err error) {
	if len(r.OS) > 0 && !slices.Contains(r.OS, string(pi.OS)) {
		return "", false, nil
	}
	if len(r.Targets) > 0 && !slices.Contains(r.Targets, Target) {
		return "", false, nil
	}
	kind, val := r.Kind()
	switch kind {
	case "read", "edit":
		p, err := anchorPath(pi, val)
		if err != nil {
			return "", false, err
		}
		tool := "Read"
		if kind == "edit" {
			tool = "Edit"
		}
		return fmt.Sprintf("%s(%s)", tool, p), true, nil
	case "bash":
		return fmt.Sprintf("Bash(%s)", val), true, nil
	case "powershell":
		return fmt.Sprintf("PowerShell(%s)", val), true, nil
	case "web_fetch":
		return fmt.Sprintf("WebFetch(domain:%s)", val), true, nil
	case "mcp":
		return "mcp__" + strings.ReplaceAll(val, ":", "__"), true, nil
	default:
		return "", false, fmt.Errorf("permission rule must set exactly one of read/edit/bash/powershell/web_fetch/mcp")
	}
}

// anchorPath maps a canonical host path to Claude Code's read/edit path syntax.
func anchorPath(pi *platform.Info, p string) (string, error) {
	switch {
	case strings.HasPrefix(p, "~/"):
		return p, nil // portable on every OS
	case strings.HasPrefix(p, "${HOME}/"):
		return "~/" + strings.TrimPrefix(p, "${HOME}/"), nil
	case strings.HasPrefix(p, "${CONFIG_DIR}/"), strings.HasPrefix(p, "${APP_DATA}/"):
		abs, err := pi.Expand(p)
		if err != nil {
			return "", err
		}
		home, herr := pi.Home()
		if herr == nil && strings.HasPrefix(abs, home+pi.Sep()) {
			return "~/" + strings.ReplaceAll(strings.TrimPrefix(abs, home+pi.Sep()), `\`, "/"), nil
		}
		return absToClaude(abs), nil
	case strings.HasPrefix(p, "/"):
		return "/" + p, nil // "//abs/path": filesystem root anchor
	case strings.Contains(p, "${"):
		return "", fmt.Errorf("unsupported variable in %q", p)
	default:
		return p, nil // project-relative (cwd): "**/.env", "./x", "x"
	}
}

// absToClaude renders an absolute path as Claude Code normalises it: POSIX form, drive letters as
// "/c/..." (§8), always with the "//" root anchor.
func absToClaude(abs string) string {
	s := strings.ReplaceAll(abs, `\`, "/")
	if len(s) >= 2 && s[1] == ':' {
		s = "/" + strings.ToLower(s[:1]) + s[2:]
	}
	return "/" + s
}

// Change is one rule to add.
type Change struct {
	List string // deny | ask | allow
	Rule string
}

// Dropped is a desired rule that was not added, with the reason (shown on the plan screen).
type Dropped struct {
	List, Rule, Reason string
}

// PermPlan is the computed permissions change for one settings file.
type PermPlan struct {
	Adds    []Change
	Dropped []Dropped
	Present []Change // desired rules already in the file (informational)
}

// Empty reports whether applying the plan would change nothing.
func (p PermPlan) Empty() bool { return len(p.Adds) == 0 }

// PlanPermissions computes what must be added to settings so it satisfies the canonical permissions.
// Rules (docs/merge-semantics.md §4.5):
//   - only ever adds; nothing already in the file is removed or reordered;
//   - deny and ask are unioned;
//   - an allow is dropped (and reported) when it is provably shadowed by a deny or ask.
func PlanPermissions(pi *platform.Info, settings []byte, perms manifest.Permissions) (PermPlan, error) {
	var plan PermPlan
	have := map[string][]string{}
	for _, l := range lists {
		cur, err := jsonedit.ReadStrings(settings, []string{"permissions", l})
		if err != nil {
			return PermPlan{}, err
		}
		have[l] = cur
	}
	desired := map[string][]string{}
	for l, rules := range map[string][]manifest.PermissionRule{"deny": perms.Deny, "ask": perms.Ask, "allow": perms.Allow} {
		for i, r := range rules {
			native, ok, err := TranslateRule(pi, r)
			if err != nil {
				return PermPlan{}, fmt.Errorf("permissions.%s[%d]: %w", l, i, err)
			}
			if ok {
				desired[l] = append(desired[l], native)
			}
		}
	}

	// What denies/asks will be in force (existing + desired) for shadow checks.
	blocking := append(append(append([]string{}, have["deny"]...), have["ask"]...), append(desired["deny"], desired["ask"]...)...)

	for _, l := range lists {
		seen := map[string]bool{}
		for _, rule := range desired[l] {
			if seen[rule] {
				continue
			}
			seen[rule] = true
			if slices.Contains(have[l], rule) {
				plan.Present = append(plan.Present, Change{l, rule})
				continue
			}
			if l == "allow" {
				if by, shadowed := shadowedBy(rule, blocking); shadowed {
					plan.Dropped = append(plan.Dropped, Dropped{l, rule, "has no effect: covered by " + by})
					continue
				}
			}
			if l == "ask" && slices.Contains(desired["deny"], rule) {
				plan.Dropped = append(plan.Dropped, Dropped{l, rule, "redundant: the same rule is denied"})
				continue
			}
			plan.Adds = append(plan.Adds, Change{l, rule})
		}
	}
	return plan, nil
}

// shadowedBy reports whether allow rule `a` can never take effect because some deny/ask rule in
// `blocking` covers it. It only claims what it can prove: identical rules, or a Read/Edit deny
// pattern P/** that is a path prefix of the allow pattern. Anything else is left to Claude Code's
// native deny -> ask -> allow evaluation.
func shadowedBy(a string, blocking []string) (string, bool) {
	for _, b := range blocking {
		if a == b {
			return b, true
		}
		bt, barg, ok1 := splitRule(b)
		at, aarg, ok2 := splitRule(a)
		if ok1 && ok2 && bt == at && (bt == "Read" || bt == "Edit") && strings.HasSuffix(barg, "/**") {
			if strings.HasPrefix(aarg, strings.TrimSuffix(barg, "**")) {
				return b, true
			}
		}
	}
	return "", false
}

func splitRule(r string) (tool, arg string, ok bool) {
	i := strings.IndexByte(r, '(')
	if i < 0 || !strings.HasSuffix(r, ")") {
		return "", "", false
	}
	return r[:i], r[i+1 : len(r)-1], true
}

// Apply returns settings with every planned rule added (layout preserved; see internal/jsonedit).
func (p PermPlan) Apply(settings []byte) ([]byte, error) {
	out := settings
	for _, l := range lists {
		var add []string
		for _, c := range p.Adds {
			if c.List == l {
				add = append(add, c.Rule)
			}
		}
		if len(add) == 0 {
			continue
		}
		var err error
		out, _, err = jsonedit.AppendStrings(out, []string{"permissions", l}, add)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
