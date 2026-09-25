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
	Summary  string   // one line for the review screen
	Detail   []string // extra lines
	Runs     bool     // executes code on the user's machine (hooks, MCP servers): flagged on the screen
	Items    []state.Item

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

// Apply performs every actionable op in order and records owned items in ts. It stops at the first
// error; whatever already happened is in the writer's journal, so it can be rolled back.
func (p *Plan) Apply(x *Exec, ts *state.TargetState) error {
	for _, o := range p.Ops {
		if o.Do != nil {
			if err := o.Do(x); err != nil {
				return fmt.Errorf("%s %s: %w", o.Category, o.Key, err)
			}
		}
		if o.Symbol == Conflict {
			continue
		}
		for _, it := range o.Items {
			ts.Upsert(it)
		}
	}
	return nil
}

var sectionOrder = []struct{ category, title string }{
	{"instruction", "INSTRUCTIONS"}, {"skill", "SKILLS"}, {"agent", "AGENTS"}, {"command", "COMMANDS"},
	{"mcp", "MCP SERVERS"}, {"hook", "HOOKS"}, {"permission", "PERMISSIONS"},
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
