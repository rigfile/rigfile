package claudecode

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/splice"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

type builder struct {
	env  Env
	plan *engine.Plan
	err  error
}

func (b *builder) fail(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *builder) note(format string, a ...any) {
	b.plan.Notes = append(b.plan.Notes, fmt.Sprintf(format, a...))
}

// Build computes the Claude Code plan for a projection. It reads the machine (current files, the vendor
// CLI's presence) but changes nothing. An error means the plan could not be computed safely (malformed
// markers, unreadable files); conflicts are NOT errors: they appear as refused ops.
func Build(env Env, p *merge.Projection) (*engine.Plan, error) {
	if env.Plat == nil || env.ClaudeDir == "" {
		return nil, errors.New("claudecode: Env needs Plat and ClaudeDir")
	}
	b := &builder{env: env, plan: &engine.Plan{Target: "Claude Code", Skipped: p.Skipped}}
	b.instructions(p)
	b.skills(p)
	b.agents(p)
	b.commands(p)
	b.mcp(p)
	b.settings(p)
	b.fileOrphans()
	if b.err != nil {
		return nil, b.err
	}
	return b.plan, nil
}

func srcPath(dir, rel string) (string, error) { return manifest.PathInside(dir, rel) }

// ---- instructions -----------------------------------------------------------------------------

func (b *builder) instructions(p *merge.Projection) {
	user := filepath.Join(b.env.ClaudeDir, "CLAUDE.md")
	project := ""
	if b.env.ProjectDir != "" {
		project = filepath.Join(b.env.ProjectDir, "CLAUDE.md")
	}
	groups := map[string][]merge.Prov[manifest.Instruction]{}
	var order []string
	seen := map[string]bool{}
	addDest := func(d string) {
		if !seen[d] {
			seen[d] = true
			order = append(order, d)
		}
	}
	for _, it := range p.Instructions {
		dest := user
		if it.V.EffectiveScope() == "project" {
			if project == "" {
				b.note("instruction %q has scope: project; pass --project <dir> to install it", it.V.ID)
				continue
			}
			dest = project
		}
		addDest(dest)
		groups[dest] = append(groups[dest], it)
	}
	// files that hold sections from an earlier apply but no longer receive any: still need cleaning
	if b.env.State != nil {
		for _, it := range b.env.State.Items {
			if it.Kind == state.KindRegion {
				addDest(it.Path)
			}
		}
	}
	for _, dest := range order {
		b.instructionFile(dest, groups[dest])
	}
}

func (b *builder) instructionFile(dest string, items []merge.Prov[manifest.Instruction]) {
	orig, exists, err := readOptional(dest)
	if err != nil {
		b.fail(err)
		return
	}
	doc := orig
	var detail []string
	var stItems []state.Item
	var conflicts []string
	var keep []state.Item
	for _, it := range items {
		abs, err := srcPath(it.Dir, it.V.File)
		if err != nil {
			b.fail(fmt.Errorf("instruction %s: %w", it.V.ID, err))
			return
		}
		body, err := os.ReadFile(abs)
		if err != nil {
			b.fail(err)
			return
		}
		reg, found, ferr := splice.Find(doc, splice.HTML, it.V.ID)
		if ferr != nil {
			b.fail(fmt.Errorf("%s: %w (fix the markers by hand; Rigfile will not edit a file it cannot parse safely)", b.env.short(dest), ferr))
			return
		}
		if found && reg.Drifted && !b.env.Overwrite {
			conflicts = append(conflicts, fmt.Sprintf("! section %q was edited by hand; not touched (use --overwrite)", it.V.ID))
			if prev, mine := b.env.owned("instruction", it.V.ID, dest); mine {
				keep = append(keep, prev)
			}
			continue
		}
		next, changed, err := splice.Upsert(doc, splice.HTML, it.V.ID, body, splice.Options{Overwrite: b.env.Overwrite})
		if err != nil {
			b.fail(fmt.Errorf("instruction %s: %w", it.V.ID, err))
			return
		}
		switch {
		case !found:
			detail = append(detail, fmt.Sprintf("+ section %q  (from %s)", it.V.ID, it.Layer))
		case changed:
			detail = append(detail, fmt.Sprintf("~ section %q  (updated from %s)", it.V.ID, it.Layer))
		default:
			detail = append(detail, fmt.Sprintf("= section %q  (up to date)", it.V.ID))
		}
		doc = next
		r, _, _ := splice.Find(doc, splice.HTML, it.V.ID)
		stItems = append(stItems, state.Item{
			Category: "instruction", Key: it.V.ID, Kind: state.KindRegion, Path: dest, Hash: r.Hash,
			Detail: map[string]string{"region": it.V.ID, "style": "html"},
		})
	}
	// sections from an earlier apply that the rig no longer has: removed if unmodified
	current := map[string]bool{}
	for _, it := range items {
		current[it.V.ID] = true
	}
	removedAny := false
	if b.env.State != nil {
		for _, prev := range b.env.State.Items {
			if prev.Kind != state.KindRegion || prev.Path != dest || current[prev.Detail["region"]] {
				continue
			}
			reg, found, ferr := splice.Find(doc, splice.HTML, prev.Detail["region"])
			switch {
			case ferr != nil:
				b.fail(fmt.Errorf("%s: %w", b.env.short(dest), ferr))
				return
			case !found:
				// already gone: nothing to do, ownership is dropped
			case reg.Drifted && !b.env.Overwrite:
				b.note("section %q in %s is no longer in the rig but was edited by hand; left in place", prev.Detail["region"], b.env.short(dest))
			default:
				next, _, err := splice.Remove(doc, splice.HTML, prev.Detail["region"])
				if err != nil {
					b.fail(err)
					return
				}
				doc = next
				removedAny = true
				detail = append(detail, fmt.Sprintf("- section %q  (no longer in the rig)", prev.Detail["region"]))
			}
		}
	}
	if len(items) == 0 && !removedAny {
		return // a destination that only had orphans we could not (or need not) remove: nothing to show
	}
	detail = append(detail, conflicts...)
	op := engine.Op{Category: "instruction", Key: filepath.Base(dest), Detail: detail, Items: append(stItems, keep...)}
	label := b.env.short(dest)
	switch {
	case bytes.Equal(doc, orig) && len(conflicts) == 0:
		op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
	case bytes.Equal(doc, orig):
		op.Symbol, op.Summary = engine.Conflict, label
		op.Keep, op.Items = op.Items, nil
	default:
		op.Symbol = engine.Update
		if !exists {
			op.Symbol = engine.New
		}
		if len(items) == 0 && removedAny {
			op.Symbol = engine.Removal
		}
		op.Summary = label
		final := doc
		op.Do = func(x *engine.Exec) error { _, err := x.W.WriteFile(dest, final); return err }
	}
	b.plan.Ops = append(b.plan.Ops, op)
}

// ---- skills (directories), agents and commands (single files) -----------------------------------

func (b *builder) skills(p *merge.Projection) {
	for _, s := range p.Skills {
		key := s.V.Key()
		if s.V.Path == "" {
			b.note("skill %q is an external reference (%s): fetching arrives with the registry/git sources in Stage 4; skipped", key, s.V.Ref)
			continue
		}
		src, err := srcPath(s.Dir, s.V.Path)
		if err != nil {
			b.fail(fmt.Errorf("skill %s: %w", key, err))
			return
		}
		entries, err := hashing.TreeEntries(src)
		if err != nil {
			b.fail(fmt.Errorf("skill %s: %w", key, err))
			return
		}
		b.treeOp("skill", key, filepath.Join(b.env.ClaudeDir, "skills", key), src, entries, s.Layer)
	}
}

func (b *builder) treeOp(category, key, dest, src string, entries []hashing.Entry, from string) {
	env := b.env
	want := hashing.TreeOf(entries)
	item := state.Item{Category: category, Key: key, Kind: state.KindTree, Path: dest, Hash: want}
	label := fmt.Sprintf("%s → %s", key, env.short(dest))
	op := engine.Op{Category: category, Key: key, Items: []state.Item{item}}

	do := func(x *engine.Exec) error {
		have := map[string]bool{}
		for _, e := range entries {
			have[e.Path] = true
			data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(e.Path)))
			if err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if e.Exec {
				mode = 0o755
			}
			if _, err := x.W.WriteFileMode(filepath.Join(dest, filepath.FromSlash(e.Path)), data, mode); err != nil {
				return err
			}
		}
		// remove files of a previous version that the new version no longer has
		old, _ := hashing.TreeEntries(dest)
		for _, e := range old {
			if !have[e.Path] {
				if _, err := x.W.Delete(filepath.Join(dest, filepath.FromSlash(e.Path))); err != nil {
					return err
				}
			}
		}
		return nil
	}

	st, err := os.Lstat(dest)
	switch {
	case errors.Is(err, os.ErrNotExist):
		op.Symbol, op.Summary, op.Do = engine.New, label, do
	case err != nil:
		b.fail(err)
		return
	case !st.IsDir():
		op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   a file with that name exists; not touched", nil
	default:
		cur, herr := hashing.Tree(dest)
		prev, mine := env.owned(category, key, dest)
		switch {
		case herr != nil:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   cannot be inspected ("+herr.Error()+"); not touched", nil
		case cur == want:
			op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
		case mine && prev.Hash == cur:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (updated from "+from+")", do
		case env.Overwrite:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (--overwrite)", do
		case mine:
			op.Symbol, op.Summary, op.Items, op.Keep = engine.Conflict, label+"   edited by hand since Rigfile wrote it; not touched (use --overwrite)", nil, []state.Item{prev}
		default:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   already exists and is not managed by Rigfile; not touched (use --overwrite)", nil
		}
	}
	b.plan.Ops = append(b.plan.Ops, op)
}

func (b *builder) agents(p *merge.Projection) {
	for _, a := range p.Agents {
		src, err := srcPath(a.Dir, a.V.Path)
		if err != nil {
			b.fail(fmt.Errorf("agent %s: %w", a.V.Key(), err))
			return
		}
		data, err := os.ReadFile(src)
		if err != nil {
			b.fail(err)
			return
		}
		b.fileOp("agent", a.V.Key(), filepath.Join(b.env.ClaudeDir, "agents", a.V.Key()+".md"), data, 0o644, a.Layer, false)
	}
}

func (b *builder) commands(p *merge.Projection) {
	for _, c := range p.Commands {
		src, err := srcPath(c.Dir, c.V.Path)
		if err != nil {
			b.fail(fmt.Errorf("command %s: %w", c.V.Key(), err))
			return
		}
		data, err := os.ReadFile(src)
		if err != nil {
			b.fail(err)
			return
		}
		b.fileOp("command", c.V.Key(), filepath.Join(b.env.ClaudeDir, "commands", c.V.Key()+".md"), data, 0o644, c.Layer, false)
	}
}

// keySlug makes a state/file-safe token from an id.
func keySlug(s string) string { return strings.NewReplacer("/", "_", `\`, "_", ":", "_").Replace(s) }

// keepOwnedOnConflict retains previous ownership for refused ops that had none set: if the rig changed an
// item into something Stage 1 cannot install (unknown built-in hook, unsupported MCP shape, ...), the
// earlier installation stays in place and stays owned rather than being treated as an orphan and removed.
func (b *builder) keepOwnedOnConflict(ops []engine.Op) {
	if b.env.State == nil {
		return
	}
	for i := range ops {
		if ops[i].Symbol != engine.Conflict || len(ops[i].Keep) > 0 {
			continue
		}
		for _, it := range b.env.State.Items {
			if it.Category == ops[i].Category && it.Key == ops[i].Key {
				ops[i].Keep = append(ops[i].Keep, it)
			}
		}
	}
}

// fileOrphans removes files and directories Rigfile owns that the rig no longer contains, if unmodified.
func (b *builder) fileOrphans() {
	if b.env.State == nil || b.err != nil {
		return
	}
	ids := b.plan.Identities()
	for _, it := range b.env.State.Items {
		if ids[state.Identity(it)] || (it.Kind != state.KindFile && it.Kind != state.KindTree) {
			continue
		}
		label := fmt.Sprintf("%s → %s", it.Key, b.env.short(it.Path))
		switch it.Kind {
		case state.KindFile:
			cur, err := hashing.File(it.Path)
			switch {
			case err != nil: // gone already
			case cur != it.Hash && !b.env.Overwrite:
				b.note("%s %q is no longer in the rig but was edited by hand; left in place (%s)", it.Category, it.Key, b.env.short(it.Path))
			default:
				path := it.Path
				b.plan.Ops = append(b.plan.Ops, engine.Op{Category: it.Category, Key: it.Key, Symbol: engine.Removal,
					Summary: label + "   (no longer in the rig)", Runs: it.Category == "hook",
					Do: func(x *engine.Exec) error { _, err := x.W.Delete(path); return err }})
			}
		case state.KindTree:
			cur, err := hashing.Tree(it.Path)
			switch {
			case err != nil:
			case cur != it.Hash && !b.env.Overwrite:
				b.note("%s %q is no longer in the rig but was edited by hand; left in place (%s)", it.Category, it.Key, b.env.short(it.Path))
			default:
				dir := it.Path
				b.plan.Ops = append(b.plan.Ops, engine.Op{Category: it.Category, Key: it.Key, Symbol: engine.Removal,
					Summary: label + "   (no longer in the rig)",
					Do: func(x *engine.Exec) error {
						es, err := hashing.TreeEntries(dir)
						if err != nil {
							return err
						}
						for _, e := range es {
							if _, err := x.W.Delete(filepath.Join(dir, filepath.FromSlash(e.Path))); err != nil {
								return err
							}
						}
						return nil
					}})
			}
		}
	}
}
