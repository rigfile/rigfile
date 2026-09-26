// Package engine is the target-independent part of plan/apply: a Plan is an ordered list of Ops. Each
// Op describes one change for the review screen (plan §5.1), knows how to perform it through the
// backup-first Writer, and lists the state items Rigfile owns afterwards. Adapters (Claude Code now,
// Codex etc. in Stage 3) only build Ops.
package engine

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// Symbols for Op.Symbol.
const (
	New       = "+" // will be created / added
	Update    = "~" // will be changed
	Unchanged = "=" // already as wanted (still recorded as owned)
	Conflict  = "!" // refused: something we do not own is in the way; nothing is done
	Removal   = "-" // will be removed
)

// Exec is what an Op needs to act.
type Exec struct {
	W *apply.Writer
}

// Op is one planned change.
type Op struct {
	Category string // instruction | skill | agent | command | hook | permission | mcp
	Key      string
	Symbol   string
	Summary  string       // one line for the review screen
	Detail   []string     // extra lines
	Runs     bool         // executes code on the user's machine (hooks, MCP servers): flagged on the screen
	Items    []state.Item // ownership to record after a successful apply
	Keep     []state.Item // previous ownership retained unchanged (an owned item we refused to touch)

	Do func(*Exec) error // nil for Unchanged and Conflict
}

// Actionable reports whether the op changes something on disk (or in a vendor CLI). Several ops may
// share one write (all settings.json changes are one file write): only the first carries Do.
func (o Op) Actionable() bool { return o.Symbol == New || o.Symbol == Update || o.Symbol == Removal }

// Plan is the computed set of changes for one target.
type Plan struct {
	Target   string
	Ops      []Op
	Notes    []string // informational lines (skipped items, warnings)
	Skipped  []merge.Skipped
	Replaced []merge.Replacement
}

// Changes counts actionable ops.
func (p *Plan) Changes() int {
	n := 0
	for _, o := range p.Ops {
		if o.Actionable() {
			n++
		}
	}
	return n
}

// Conflicts lists refused ops.
func (p *Plan) Conflicts() []Op {
	var out []Op
	for _, o := range p.Ops {
		if o.Symbol == Conflict {
			out = append(out, o)
		}
	}
	return out
}

// Apply performs every actionable op in order and REPLACES ts.Items with what the plan now owns: items of
// ops that succeeded or are unchanged, plus previous ownership kept by refused ops. Things the rig no
// longer contains are therefore dropped from state (their removal ops deleted them, or left them in
// place with a note). It stops at the first error and leaves ts untouched; whatever already happened is
// in the writer's journal, so it can be rolled back.
func (p *Plan) Apply(x *Exec, ts *state.TargetState) error {
	var items []state.Item
	for _, o := range p.Ops {
		if o.Do != nil {
			if err := o.Do(x); err != nil {
				return fmt.Errorf("%s %s: %w", o.Category, o.Key, err)
			}
		}
		if o.Symbol == Conflict {
			items = append(items, o.Keep...)
			continue
		}
		items = append(items, o.Items...)
	}
	ts.Items = items
	return nil
}

// Identities is the set of ownership identities the plan will hold after applying (for orphan detection).
func (p *Plan) Identities() map[string]bool {
	ids := map[string]bool{}
	for _, o := range p.Ops {
		for _, it := range o.Items {
			ids[state.Identity(it)] = true
		}
		for _, it := range o.Keep {
			ids[state.Identity(it)] = true
		}
	}
	return ids
}

var sectionOrder = []struct{ category, title string }{
	{"instruction", "INSTRUCTIONS"}, {"skill", "SKILLS"}, {"agent", "AGENTS"}, {"command", "COMMANDS"},
	{"mcp", "MCP SERVERS"}, {"hook", "HOOKS"}, {"permission", "PERMISSIONS"}, {"git", "GIT"},
}

// Render prints the plan grouped by category (plan §5.1). ⚠ marks things that execute code.
func (p *Plan) Render(w io.Writer) {
	fmt.Fprintf(w, "%s\n", p.Target)
	byCat := map[string][]Op{}
	for _, o := range p.Ops {
		byCat[o.Category] = append(byCat[o.Category], o)
	}
	for _, s := range sectionOrder {
		ops := byCat[s.category]
		if len(ops) == 0 {
			continue
		}
		for i, o := range ops {
			title := ""
			if i == 0 {
				title = s.title
			}
			warn := ""
			if o.Runs && o.Actionable() {
				warn = "   ⚠ executes code"
			}
			fmt.Fprintf(w, "%-13s %s %s%s\n", title, o.Symbol, o.Summary, warn)
			for _, d := range o.Detail {
				fmt.Fprintf(w, "%-13s     %s\n", "", d)
			}
		}
	}
	if len(p.Replaced) > 0 {
		fmt.Fprintln(w, "REPLACED")
		for _, r := range p.Replaced {
			fmt.Fprintf(w, "              %s %q: %s → %s\n", r.Category, r.Key, r.From, r.By)
		}
	}
	if len(p.Skipped) > 0 {
		fmt.Fprintln(w, "NOT APPLICABLE HERE")
		skipped := append([]merge.Skipped(nil), p.Skipped...)
		sort.SliceStable(skipped, func(i, j int) bool { return skipped[i].Category < skipped[j].Category })
		for _, s := range skipped {
			fmt.Fprintf(w, "              %s %q (%s)\n", s.Category, s.Key, s.Reason)
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(w, "NOTE          %s\n", n)
	}
	switch c := p.Changes(); {
	case len(p.Conflicts()) > 0:
		fmt.Fprintf(w, "%d change(s), %d refused (see ! lines)\n", c, len(p.Conflicts()))
	case c == 0:
		fmt.Fprintln(w, "no changes")
	default:
		fmt.Fprintf(w, "%d change(s)\n", c)
	}
}

// Short replaces a home prefix with ~ for display.
func Short(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/") || strings.HasPrefix(path, home+`\`)) {
		return "~" + path[len(home):]
	}
	return path
}
