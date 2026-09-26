// Package layers resolves a rig's `from:` chain into the linearised layer list that internal/merge
// consumes (docs/merge-semantics.md §2.1): depth-first post-order, parents before children, a layer
// that appears more than once (diamond) applied once at its earliest position, cycles and excessive
// depth rejected, rigfile/base-secure always lowest and locked.
//
// Stage 1 has no network and no registry: layers come from a local directory (DirSource). The Source
// interface is the seam Stage 4/5 plug the git and registry sources into.
package layers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
)

// MaxDepth bounds `from:` nesting (merge-semantics §2.1 proposal).
const MaxDepth = 8

// Ref is a parsed `owner/name[@range]`.
type Ref struct {
	Name  string // owner/name
	Range string // "" (any), exact "1.2.3", "^1", "^1.2", "~1.2", ...
}

// ParseRef splits "owner/name@range".
func ParseRef(s string) (Ref, error) {
	name, rng, _ := strings.Cut(s, "@")
	if strings.Count(name, "/") != 1 || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return Ref{}, fmt.Errorf("layers: %q is not owner/name[@range]", s)
	}
	return Ref{Name: name, Range: rng}, nil
}

// NotFoundError means the source does not have the layer.
type NotFoundError struct{ Ref Ref }

func (e *NotFoundError) Error() string { return fmt.Sprintf("layer %s is not available", e.Ref.Name) }

// Source finds layers.
type Source interface {
	Resolve(ref Ref) (*manifest.Loaded, error)
}

// DirSource looks for <Root>/<owner>/<name>/rigfile.yaml.
type DirSource struct{ Root string }

// Resolve implements Source.
func (d DirSource) Resolve(ref Ref) (*manifest.Loaded, error) {
	if d.Root == "" {
		return nil, &NotFoundError{ref}
	}
	owner, name, _ := strings.Cut(ref.Name, "/")
	dir := filepath.Join(d.Root, owner, name)
	if _, err := os.Stat(filepath.Join(dir, manifest.FileName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &NotFoundError{ref}
		}
		return nil, err
	}
	l, err := manifest.Load(dir)
	if err != nil {
		return nil, err
	}
	if l.M.Name != ref.Name {
		return nil, fmt.Errorf("layers: %s declares name %q but was requested as %q", dir, l.M.Name, ref.Name)
	}
	return l, nil
}

// WithBase makes rigfile/base-secure resolve to the given (embedded) layer instead of anything on disk, so no
// directory can stand in for it. Every other reference goes to next.
func WithBase(base *manifest.Loaded, next Source) Source { return baseSource{base, next} }

type baseSource struct {
	base *manifest.Loaded
	next Source
}

func (b baseSource) Resolve(ref Ref) (*manifest.Loaded, error) {
	if ref.Name == merge.BaseSecure {
		if b.base == nil {
			return nil, &NotFoundError{ref}
		}
		return b.base, nil
	}
	return b.next.Resolve(ref)
}

// Result is the resolved layer list plus notes for the plan screen.
type Result struct {
	Layers   []merge.Layer
	Loaded   map[string]*manifest.Loaded // by layer name, for file access and Check
	Warnings []string
}

// Resolve linearises top and everything it inherits.
func Resolve(top *manifest.Loaded, src Source) (*Result, error) {
	r := &Result{Loaded: map[string]*manifest.Loaded{}}
	done := map[string]bool{}

	// base-secure is always the lowest layer, listed or not (§2.1).
	baseRef := Ref{Name: merge.BaseSecure}
	if base, err := src.Resolve(baseRef); err == nil {
		if err := r.walk(base, src, done, nil, 0, true); err != nil {
			return nil, err
		}
	} else {
		var nf *NotFoundError
		if !errors.As(err, &nf) {
			return nil, err
		}
		r.Warnings = append(r.Warnings, merge.BaseSecure+" is not part of this run")
	}
	if err := r.walk(top, src, done, nil, 0, false); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Result) walk(l *manifest.Loaded, src Source, done map[string]bool, stack []string, depth int, isBase bool) error {
	name := l.M.Name
	if depth > MaxDepth {
		return fmt.Errorf("layers: inheritance is deeper than %d levels at %s", MaxDepth, strings.Join(append(stack, name), " -> "))
	}
	for _, s := range stack {
		if s == name {
			return fmt.Errorf("layers: cycle: %s", strings.Join(append(stack, name), " -> "))
		}
	}
	if done[name] {
		return nil // diamond: already applied at its earliest position
	}
	stack = append(stack, name)
	for _, raw := range l.M.From {
		ref, err := ParseRef(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if ref.Name == merge.BaseSecure {
			// already placed first; a listed version only pins/ranges it
			if b := r.Loaded[merge.BaseSecure]; b != nil && !Satisfies(b.M.Version, ref.Range) {
				return fmt.Errorf("%s requires %s@%s but %s is available", name, ref.Name, ref.Range, b.M.Version)
			}
			continue
		}
		child, err := src.Resolve(ref)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if !Satisfies(child.M.Version, ref.Range) {
			return fmt.Errorf("%s requires %s@%s but found version %s", name, ref.Name, ref.Range, child.M.Version)
		}
		if err := r.walk(child, src, done, stack, depth+1, false); err != nil {
			return err
		}
	}
	done[name] = true
	r.Loaded[name] = l
	r.Layers = append(r.Layers, merge.Layer{
		Name: name, Version: l.M.Version, Locked: isBase && name == merge.BaseSecure, M: l.M, Dir: l.Dir,
	})
	return nil
}

// Satisfies reports whether version satisfies a simple range: "" (any), exact ("1.2.3", "1.2" = 1.2.x),
// caret ("^1", "^1.2", "^0.2.3") or tilde ("~1", "~1.2"). Pre-release versions satisfy only an exact match.
func Satisfies(version, rng string) bool {
	if rng == "" {
		return true
	}
	if version == rng {
		return true
	}
	v, pre, ok := parseVersion(version)
	if !ok || pre {
		return false
	}
	op, body := "", rng
	if strings.HasPrefix(rng, "^") || strings.HasPrefix(rng, "~") {
		op, body = rng[:1], rng[1:]
	}
	parts := strings.Split(body, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return false
	}
	var want [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return false
		}
		want[i] = n
	}
	cmp := func(a, b [3]int) int {
		for i := 0; i < 3; i++ {
			if a[i] != b[i] {
				if a[i] < b[i] {
					return -1
				}
				return 1
			}
		}
		return 0
	}
	switch op {
	case "": // exact, missing parts are wildcards
		for i := range parts {
			if v[i] != want[i] {
				return false
			}
		}
		return true
	case "^":
		upper := [3]int{want[0] + 1, 0, 0}
		if want[0] == 0 && len(parts) >= 2 {
			upper = [3]int{0, want[1] + 1, 0}
			if want[1] == 0 && len(parts) == 3 {
				upper = [3]int{0, 0, want[2] + 1}
			}
		}
		return cmp(v, want) >= 0 && cmp(v, upper) < 0
	default: // "~"
		upper := [3]int{want[0] + 1, 0, 0}
		if len(parts) >= 2 {
			upper = [3]int{want[0], want[1] + 1, 0}
		}
		return cmp(v, want) >= 0 && cmp(v, upper) < 0
	}
}

func parseVersion(s string) (v [3]int, pre, ok bool) {
	if i := strings.IndexAny(s, "+"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "-"); i >= 0 {
		s, pre = s[:i], true
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, pre, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, pre, false
		}
		v[i] = n
	}
	return v, pre, true
}
