// Package common holds the plan-building machinery every non-Claude adapter shares (Codex, Gemini CLI, Cursor,
// Claude Desktop): owned whole files, owned directories, marked regions inside user files (with hand-edit
// conflicts and orphan removal), and orphan cleanup. The rules are the ones Claude Code's adapter established:
// never touch what Rigfile does not own, refuse (a `!` op) instead of overwriting, remove only what is
// unmodified, and record ownership in state.json so drift, diff and rollback work.
package common

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/hashing"
	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/splice"
	"github.com/rigfile/rigfile/internal/state"
)

// Env is the shared part of an adapter's environment.
type Env struct {
	Plat      *platform.Info
	State     *state.TargetState // what a previous apply recorded (nil = nothing)
	Overwrite bool               // replace hand-edited managed content and files Rigfile does not own
	CheckOnly bool               // judging a partial plan (doctor): never plan removals
}

// Builder accumulates one target's plan.
type Builder struct {
	Env  Env
	Plan *engine.Plan
	Err  error
}

// New starts a plan titled title.
func New(env Env, title string) *Builder {
	return &Builder{Env: env, Plan: &engine.Plan{Target: title}}
}

// Fail records the first error (a plan that could not be computed safely).
func (b *Builder) Fail(err error) {
	if b.Err == nil {
		b.Err = err
	}
}

// Note adds an informational line to the plan screen.
func (b *Builder) Note(format string, a ...any) {
	b.Plan.Notes = append(b.Plan.Notes, fmt.Sprintf(format, a...))
}

// Result returns the plan (after orphan cleanup) or the first error.
func (b *Builder) Result() (*engine.Plan, error) {
	if !b.Env.CheckOnly {
		b.orphans()
	}
	if b.Err != nil {
		return nil, b.Err
	}
	return b.Plan, nil
}

// Short renders a path with ~ for the home directory.
func (b *Builder) Short(p string) string {
	h, _ := b.Env.Plat.Home()
	return engine.Short(p, h)
}

// Owned finds the state record for (category, key, path).
func (b *Builder) Owned(category, key, path string) (state.Item, bool) {
	if b.Env.State == nil {
		return state.Item{}, false
	}
	for _, it := range b.Env.State.Items {
		if it.Category == category && it.Key == key && it.Path == path {
			return it, true
		}
	}
	return state.Item{}, false
}

// ReadOptional reads a file; a missing file is (nil, false, nil).
func ReadOptional(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// FileOp plans writing one whole file Rigfile owns.
func (b *Builder) FileOp(category, key, dest string, want []byte, mode os.FileMode, from string, runs bool) {
	wantHash := hashing.Bytes(want)
	item := state.Item{Category: category, Key: key, Kind: state.KindFile, Path: dest, Hash: wantHash}
	label := fmt.Sprintf("%s → %s", key, b.Short(dest))
	cur, exists, err := ReadOptional(dest)
	if err != nil {
		b.Fail(fmt.Errorf("%s %s: %w", category, key, err))
		return
	}
	op := engine.Op{Category: category, Key: key, Items: []state.Item{item}, Runs: runs}
	write := func(x *engine.Exec) error { _, err := x.W.WriteFileMode(dest, want, mode); return err }
	switch {
	case !exists:
		op.Symbol, op.Summary, op.Do = engine.New, label, write
	case bytes.Equal(cur, want):
		op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
	default:
		prev, mine := b.Owned(category, key, dest)
		switch {
		case mine && prev.Hash == hashing.Bytes(cur):
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (updated from "+from+")", write
		case b.Env.Overwrite:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (--overwrite)", write
		case mine:
			op.Symbol, op.Summary, op.Items, op.Keep = engine.Conflict, label+"   edited by hand since Rigfile wrote it; not touched (use --overwrite)", nil, []state.Item{prev}
		default:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   already exists and is not managed by Rigfile; not touched (use --overwrite)", nil
		}
	}
	b.Plan.Ops = append(b.Plan.Ops, op)
}

// TreeFile is one file of a generated or copied directory.
type TreeFile struct {
	Path string // forward-slash relative path
	Data []byte
	Exec bool
}

// TreeOp plans writing a directory Rigfile owns from in-memory files (a copied skill, a skill generated from a
// command).
func (b *Builder) TreeOp(category, key, dest string, files []TreeFile, from string) {
	entries := make([]hashing.Entry, 0, len(files))
	for _, f := range files {
		entries = append(entries, hashing.Entry{Path: f.Path, Size: int64(len(f.Data)), SHA256: hashing.Bytes(f.Data), Exec: f.Exec})
	}
	sortEntries(entries)
	want := hashing.TreeOf(entries)
	item := state.Item{Category: category, Key: key, Kind: state.KindTree, Path: dest, Hash: want}
	label := fmt.Sprintf("%s → %s", key, b.Short(dest))
	op := engine.Op{Category: category, Key: key, Items: []state.Item{item}}
	do := func(x *engine.Exec) error {
		have := map[string]bool{}
		for _, f := range files {
			have[f.Path] = true
			mode := os.FileMode(0o644)
			if f.Exec {
				mode = 0o755
			}
			if _, err := x.W.WriteFileMode(filepath.Join(dest, filepath.FromSlash(f.Path)), f.Data, mode); err != nil {
				return err
			}
		}
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
		b.Fail(err)
		return
	case !st.IsDir():
		op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   a file with that name exists; not touched", nil
	default:
		cur, herr := hashing.Tree(dest)
		prev, mine := b.Owned(category, key, dest)
		switch {
		case herr != nil:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   cannot be inspected ("+herr.Error()+"); not touched", nil
		case cur == want:
			op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
		case mine && prev.Hash == cur:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (updated from "+from+")", do
		case b.Env.Overwrite:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (--overwrite)", do
		case mine:
			op.Symbol, op.Summary, op.Items, op.Keep = engine.Conflict, label+"   edited by hand since Rigfile wrote it; not touched (use --overwrite)", nil, []state.Item{prev}
		default:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   already exists and is not managed by Rigfile; not touched (use --overwrite)", nil
		}
	}
	b.Plan.Ops = append(b.Plan.Ops, op)
}

func sortEntries(es []hashing.Entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j].Path < es[j-1].Path; j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}

// Region is one marked block Rigfile owns inside a user file.
type Region struct {
	ID       string // marker id, unique per file
	Key      string // state key and review-screen label
	Category string // state category of this region; "" = the RegionSet's category
	Prepend  bool   // new regions go to the top of the file (TOML top-level keys)
	Body     []byte
	Layer    string // where it comes from, for the screen
}

// RegionSet plans every region of one destination file as ONE write (one backup, one op). style is the comment
// syntax (splice.HTML for Markdown, splice.Hash for TOML/YAML/shell); toml selects the TOML-validating upsert,
// so a table the user already defined turns into a refused op instead of a broken file. Regions from an earlier
// apply that are no longer wanted are removed when unmodified.
func (b *Builder) RegionSet(category, dest string, style splice.Style, styleName string, toml bool, regions []Region) {
	orig, exists, err := ReadOptional(dest)
	if err != nil {
		b.Fail(err)
		return
	}
	doc := orig
	var detail []string
	var items, keep []state.Item
	var conflicts []string
	for _, r := range regions {
		reg, found, ferr := splice.Find(doc, style, r.ID)
		if ferr != nil {
			b.Fail(fmt.Errorf("%s: %w (fix the markers by hand; Rigfile will not edit a file it cannot parse safely)", b.Short(dest), ferr))
			return
		}
		cat := category
		if r.Category != "" {
			cat = r.Category
		}
		fail := func(msg string) {
			conflicts = append(conflicts, "! "+msg)
			if prev, mine := b.Owned(cat, r.Key, dest); mine {
				keep = append(keep, prev)
			}
		}
		if found && reg.Drifted && !b.Env.Overwrite {
			fail(fmt.Sprintf("%q was edited by hand; not touched (use --overwrite)", r.Key))
			continue
		}
		var next []byte
		var changed bool
		if toml {
			next, changed, err = splice.UpsertTOML(doc, r.ID, r.Body, splice.Options{Overwrite: b.Env.Overwrite, Prepend: r.Prepend})
		} else {
			next, changed, err = splice.Upsert(doc, style, r.ID, r.Body, splice.Options{Overwrite: b.Env.Overwrite})
		}
		if errors.Is(err, splice.ErrInvalidResult) {
			fail(fmt.Sprintf("%q clashes with something you already defined in this file; not touched", r.Key))
			continue
		}
		if err != nil {
			b.Fail(fmt.Errorf("%s %s: %w", category, r.Key, err))
			return
		}
		switch {
		case !found:
			detail = append(detail, fmt.Sprintf("+ %q  (from %s)", r.Key, r.Layer))
		case changed:
			detail = append(detail, fmt.Sprintf("~ %q  (updated from %s)", r.Key, r.Layer))
		default:
			detail = append(detail, fmt.Sprintf("= %q  (up to date)", r.Key))
		}
		doc = next
		got, _, _ := splice.Find(doc, style, r.ID)
		items = append(items, state.Item{Category: cat, Key: r.Key, Kind: state.KindRegion, Path: dest, Hash: got.Hash,
			Detail: map[string]string{"region": r.ID, "style": styleName}})
	}
	current := map[string]bool{}
	for _, r := range regions {
		current[r.ID] = true
	}
	removedAny := false
	if b.Env.State != nil && !b.Env.CheckOnly {
		for _, prev := range b.Env.State.Items {
			if prev.Kind != state.KindRegion || prev.Path != dest || current[prev.Detail["region"]] {
				continue
			}
			reg, found, ferr := splice.Find(doc, style, prev.Detail["region"])
			switch {
			case ferr != nil:
				b.Fail(fmt.Errorf("%s: %w", b.Short(dest), ferr))
				return
			case !found:
			case reg.Drifted && !b.Env.Overwrite:
				b.Note("%q in %s is no longer in the rig but was edited by hand; left in place", prev.Key, b.Short(dest))
			default:
				next, _, err := splice.Remove(doc, style, prev.Detail["region"])
				if err != nil {
					b.Fail(err)
					return
				}
				doc = next
				removedAny = true
				detail = append(detail, fmt.Sprintf("- %q  (no longer in the rig)", prev.Key))
			}
		}
	}
	if len(regions) == 0 && !removedAny {
		return
	}
	detail = append(detail, conflicts...)
	op := engine.Op{Category: category, Key: filepath.Base(dest), Detail: detail, Items: append(items, keep...)}
	label := b.Short(dest)
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
		if len(regions) == 0 && removedAny {
			op.Symbol = engine.Removal
		}
		op.Summary = label
		final := doc
		op.Do = func(x *engine.Exec) error { _, err := x.W.WriteFile(dest, final); return err }
	}
	b.Plan.Ops = append(b.Plan.Ops, op)
}

// orphans removes files and directories Rigfile owns that the rig no longer contains, if unmodified.
func (b *Builder) orphans() {
	if b.Env.State == nil || b.Err != nil {
		return
	}
	ids := b.Plan.Identities()
	for _, it := range b.Env.State.Items {
		if ids[state.Identity(it)] || (it.Kind != state.KindFile && it.Kind != state.KindTree) {
			continue
		}
		label := fmt.Sprintf("%s → %s", it.Key, b.Short(it.Path))
		switch it.Kind {
		case state.KindFile:
			cur, err := hashing.File(it.Path)
			switch {
			case err != nil:
			case cur != it.Hash && !b.Env.Overwrite:
				b.Note("%s %q is no longer in the rig but was edited by hand; left in place (%s)", it.Category, it.Key, b.Short(it.Path))
			default:
				path := it.Path
				b.Plan.Ops = append(b.Plan.Ops, engine.Op{Category: it.Category, Key: it.Key, Symbol: engine.Removal, Summary: label + "   (no longer in the rig)",
					Do: func(x *engine.Exec) error { _, err := x.W.Delete(path); return err }})
			}
		case state.KindTree:
			cur, err := hashing.Tree(it.Path)
			switch {
			case err != nil:
			case cur != it.Hash && !b.Env.Overwrite:
				b.Note("%s %q is no longer in the rig but was edited by hand; left in place (%s)", it.Category, it.Key, b.Short(it.Path))
			default:
				dir := it.Path
				b.Plan.Ops = append(b.Plan.Ops, engine.Op{Category: it.Category, Key: it.Key, Symbol: engine.Removal, Summary: label + "   (no longer in the rig)",
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

// SrcPath resolves a rig-relative path inside dir, refusing escapes (symlinks, ..).
func SrcPath(dir, rel string) (string, error) { return manifest.PathInside(dir, rel) }
