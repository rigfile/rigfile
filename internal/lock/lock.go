// Package lock builds and verifies rigfile.lock (plan §6.4): the exact layers, versions and content
// hashes a rig was resolved to. `apply` refuses to proceed when what is on disk no longer matches.
//
// The lockfile is machine-independent (no absolute paths, no timestamps), so it can be committed
// with the rig and produces the same bytes on macOS and Linux.
package lock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/layers"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

// FileName is the lockfile name inside a rig directory.
const FileName = "rigfile.lock"

// Version of the lockfile format.
const Version = 1

// Lock is the on-disk model.
type Lock struct {
	LockVersion int               `json:"lockVersion"`
	Layers      []Layer           `json:"layers"`
	Merged      map[string]string `json:"merged"` // target -> hash of the canonical merged model
}

// Layer pins one layer.
type Layer struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	ManifestSHA256 string `json:"manifestSha256"`
	// Set for a layer fetched from a git source: the canonical source, the commit it resolved to and the content
	// hash of the fetched tree. Later resolutions are held to these.
	Source     string `json:"source,omitempty"`
	Commit     string `json:"commit,omitempty"`
	TreeSHA256 string `json:"treeSha256,omitempty"`
	Items      []Item `json:"items,omitempty"`
}

// Item pins one file or tree referenced by a layer.
type Item struct {
	Category string `json:"category"`
	Key      string `json:"key"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
}

// Build computes the lock for a resolved layer set. merged maps target -> merged-model hash.
func Build(res *layers.Result, merged map[string]string) (*Lock, error) {
	l := &Lock{LockVersion: Version, Merged: map[string]string{}}
	for t, h := range merged {
		l.Merged[t] = h
	}
	for _, ml := range res.Layers {
		loaded := res.Loaded[ml.Name]
		ly := Layer{Name: ml.Name, Version: ml.Version, ManifestSHA256: loaded.Hash}
		if ri, ok := res.Remotes[ml.Name]; ok {
			ly.Source, ly.Commit, ly.TreeSHA256 = ri.Source, ri.Commit, ri.TreeSHA256
		}
		items, err := layerItems(loaded)
		if err != nil {
			return nil, fmt.Errorf("layer %s: %w", ml.Name, err)
		}
		ly.Items = items
		l.Layers = append(l.Layers, ly)
	}
	return l, nil
}

// layerItems hashes every rig file a layer references.
func layerItems(l *manifest.Loaded) ([]Item, error) {
	var items []Item
	m := l.M
	hashFile := func(cat, key, rel string) error {
		abs, err := manifest.PathInside(l.Dir, rel)
		if err != nil {
			return err
		}
		sum, err := hashing.File(abs)
		if err != nil {
			return err
		}
		items = append(items, Item{cat, key, rel, sum})
		return nil
	}
	for _, x := range m.Instructions {
		if err := hashFile("instruction", x.Key(), x.File); err != nil {
			return nil, err
		}
	}
	for _, x := range m.Skills {
		if x.Path == "" {
			items = append(items, Item{"skill", x.Key(), x.Ref, hashing.Bytes([]byte("ref:" + x.Ref))})
			continue
		}
		abs, err := manifest.PathInside(l.Dir, x.Path)
		if err != nil {
			return nil, err
		}
		sum, err := hashing.Tree(abs)
		if err != nil {
			return nil, err
		}
		items = append(items, Item{"skill", x.Key(), x.Path, sum})
	}
	for _, x := range m.Agents {
		if err := hashFile("agent", x.Key(), x.Path); err != nil {
			return nil, err
		}
	}
	for _, x := range m.Commands {
		if err := hashFile("command", x.Key(), x.Path); err != nil {
			return nil, err
		}
	}
	for _, h := range m.Hooks {
		runs := map[string]string{}
		if h.Run.Single != "" {
			runs["*"] = h.Run.Single
		}
		for osName, r := range h.Run.PerOS {
			runs[osName] = r
		}
		for osName, r := range runs {
			if _, isBuiltin := manifest.Builtin(r); isBuiltin {
				continue
			}
			if err := hashFile("hook", h.Key()+"@"+osName, r); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		if items[i].Key != items[j].Key {
			return items[i].Key < items[j].Key
		}
		return items[i].Path < items[j].Path
	})
	return items, nil
}

// Marshal renders the lockfile: indented JSON, stable key order (map keys sorted by encoding/json),
// trailing newline.
func (l *Lock) Marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(l); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Parse reads a lockfile.
func Parse(data []byte) (*Lock, error) {
	var l Lock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("lockfile: %w", err)
	}
	if l.LockVersion != Version {
		return nil, fmt.Errorf("lockfile: unsupported lockVersion %d (this rigfile understands %d)", l.LockVersion, Version)
	}
	return &l, nil
}

// ErrMismatch wraps the differences found by Verify.
var ErrMismatch = errors.New("rigfile.lock does not match the rig")

// Verify compares an existing lockfile with a freshly built one and returns one human-readable line
// per difference (empty = identical). Use Mismatch to turn them into an error.
func Verify(existing, fresh *Lock) []string {
	var d []string
	old := map[string]Layer{}
	for _, l := range existing.Layers {
		old[l.Name] = l
	}
	seen := map[string]bool{}
	for _, nl := range fresh.Layers {
		seen[nl.Name] = true
		ol, ok := old[nl.Name]
		if !ok {
			d = append(d, fmt.Sprintf("layer %s is new (not in the lockfile)", nl.Name))
			continue
		}
		if ol.Version != nl.Version {
			d = append(d, fmt.Sprintf("layer %s: version %s -> %s", nl.Name, ol.Version, nl.Version))
		}
		if ol.Source != nl.Source || ol.Commit != nl.Commit || ol.TreeSHA256 != nl.TreeSHA256 {
			d = append(d, fmt.Sprintf("layer %s: source %s@%s -> %s@%s", nl.Name, ol.Source, short(ol.Commit), nl.Source, short(nl.Commit)))
		}
		if ol.ManifestSHA256 != nl.ManifestSHA256 {
			d = append(d, fmt.Sprintf("layer %s: rigfile.yaml changed", nl.Name))
		}
		oi := map[string]Item{}
		for _, it := range ol.Items {
			oi[it.Category+"|"+it.Key+"|"+it.Path] = it
		}
		ni := map[string]bool{}
		for _, it := range nl.Items {
			k := it.Category + "|" + it.Key + "|" + it.Path
			ni[k] = true
			switch o, ok := oi[k]; {
			case !ok:
				d = append(d, fmt.Sprintf("layer %s: %s %q (%s) is new", nl.Name, it.Category, it.Key, it.Path))
			case o.SHA256 != it.SHA256:
				d = append(d, fmt.Sprintf("layer %s: %s %q (%s) changed", nl.Name, it.Category, it.Key, it.Path))
			}
		}
		for k, it := range oi {
			if !ni[k] {
				d = append(d, fmt.Sprintf("layer %s: %s %q (%s) was removed", nl.Name, it.Category, it.Key, it.Path))
			}
		}
	}
	for name := range old {
		if !seen[name] {
			d = append(d, fmt.Sprintf("layer %s is in the lockfile but no longer part of the rig", name))
		}
	}
	for t, h := range fresh.Merged {
		if oh, ok := existing.Merged[t]; ok && oh != h {
			d = append(d, fmt.Sprintf("merged model for %s changed", t))
		}
	}
	sort.Strings(d)
	return d
}

// Mismatch returns an error describing differences, or nil if there are none.
func Mismatch(diffs []string) error {
	if len(diffs) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  %s\nRun `rigfile lock` to accept the changes, or restore the pinned content.", ErrMismatch, joinLines(diffs))
}

func joinLines(s []string) string {
	var b bytes.Buffer
	for i, l := range s {
		if i > 0 {
			b.WriteString("\n  ")
		}
		b.WriteString(l)
	}
	return b.String()
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// Pins returns, for every layer fetched from a git source, what to hold a later resolution to.
func (l *Lock) Pins() map[string][2]string {
	out := map[string][2]string{}
	for _, ly := range l.Layers {
		if ly.Source != "" {
			out[ly.Source] = [2]string{ly.Commit, ly.TreeSHA256}
		}
	}
	return out
}
