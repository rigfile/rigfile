package common

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/jsonedit"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// JSONEntry is one member Rigfile wants inside a JSON object (an MCP server in `mcpServers`).
type JSONEntry struct {
	Name  string
	Raw   string // the member's value as JSON text
	Layer string
}

// JSONMembers plans the members of the object at parent inside the JSON file dest as ONE write: members are
// added, an owned member whose rig value changed is replaced, an owned member the rig dropped is removed, and
// a member the user defined themselves is never touched (a refused op). Ownership is recorded per member
// (state kind json-value, path "<parent>.<name>", the compacted JSON as value), so drift is detected by
// comparing the file with what Rigfile wrote. Names must not contain '.'.
func (b *Builder) JSONMembers(category, dest string, parent []string, entries []JSONEntry) {
	orig, exists, err := ReadOptional(dest)
	if err != nil {
		b.Fail(err)
		return
	}
	work := orig
	var detail, conflicts []string
	var items, keep []state.Item
	changed := false
	prefix := strings.Join(parent, ".") + "."
	for _, e := range entries {
		compact, err := compactJSON(e.Raw)
		if err != nil {
			b.Fail(fmt.Errorf("%s %s: %w", category, e.Name, err))
			return
		}
		path := append(append([]string{}, parent...), e.Name)
		item := state.Item{Category: category, Key: e.Name, Kind: state.KindJSONValue, Path: dest, Hash: hashing.Bytes([]byte(compact)),
			Detail: map[string]string{"path": prefix + e.Name, "value": compact}}
		cur, present := jsonedit.ReadValueRaw(work, path)
		prev, mine := b.Owned(category, e.Name, dest)
		switch {
		case !present:
			next, _, _, err := jsonedit.SetMissingRaw(work, path, e.Raw)
			if err != nil {
				b.Fail(fmt.Errorf("%s: %w", b.Short(dest), err))
				return
			}
			work, changed = next, true
			detail = append(detail, fmt.Sprintf("+ %q  (from %s)", e.Name, e.Layer))
			items = append(items, item)
		case cur == compact:
			detail = append(detail, fmt.Sprintf("= %q  (up to date)", e.Name))
			if mine { // a value the user wrote themselves that happens to match is theirs, not ours to delete later
				items = append(items, item)
			}
		case (mine && prev.Detail["value"] == cur) || b.Env.Overwrite:
			next, _, err := jsonedit.ReplaceRaw(work, path, e.Raw)
			if err != nil {
				b.Fail(err)
				return
			}
			work, changed = next, true
			detail = append(detail, fmt.Sprintf("~ %q  (updated from %s)", e.Name, e.Layer))
			items = append(items, item)
		case mine:
			conflicts = append(conflicts, fmt.Sprintf("! %q was edited by hand since Rigfile wrote it; not touched (use --overwrite)", e.Name))
			keep = append(keep, prev)
		default:
			conflicts = append(conflicts, fmt.Sprintf("! %q already exists and is not managed by Rigfile; not touched (use --overwrite)", e.Name))
		}
	}
	// members written by an earlier apply that the rig no longer wants
	want := map[string]bool{}
	for _, e := range entries {
		want[e.Name] = true
	}
	removedAny := false
	if b.Env.State != nil && !b.Env.CheckOnly {
		for _, prev := range b.Env.State.Items {
			if prev.Kind != state.KindJSONValue || prev.Path != dest || !strings.HasPrefix(prev.Detail["path"], prefix) || want[prev.Key] {
				continue
			}
			path := strings.Split(prev.Detail["path"], ".")
			cur, present := jsonedit.ReadValueRaw(work, path)
			switch {
			case !present:
			case cur != prev.Detail["value"] && !b.Env.Overwrite:
				b.Note("%s %q is no longer in the rig but was edited by hand; left in place (%s)", category, prev.Key, b.Short(dest))
			default:
				next, _, err := jsonedit.RemoveMember(work, path)
				if err != nil {
					b.Fail(err)
					return
				}
				work, changed, removedAny = next, true, true
				detail = append(detail, fmt.Sprintf("- %q  (no longer in the rig)", prev.Key))
			}
		}
	}
	if len(entries) == 0 && !removedAny {
		return
	}
	detail = append(detail, conflicts...)
	op := engine.Op{Category: category, Key: baseName(dest), Detail: detail, Items: append(items, keep...)}
	label := b.Short(dest)
	switch {
	case !changed && len(conflicts) == 0:
		op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
	case !changed:
		op.Symbol, op.Summary = engine.Conflict, label
		op.Keep, op.Items = op.Items, nil
	default:
		op.Symbol = engine.Update
		if !exists {
			op.Symbol = engine.New
		}
		if len(entries) == 0 && removedAny {
			op.Symbol = engine.Removal
		}
		op.Summary = label
		final := work
		op.Do = func(x *engine.Exec) error { _, err := x.W.WriteFile(dest, final); return err }
	}
	if !bytes.Equal(work, orig) || !changed {
		b.Plan.Ops = append(b.Plan.Ops, op)
	}
}

func baseName(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	return p[i+1:]
}

func compactJSON(raw string) (string, error) {
	var buf bytes.Buffer
	if err := jsonCompact(&buf, raw); err != nil {
		return "", errors.New("not valid JSON: " + err.Error())
	}
	return buf.String(), nil
}
