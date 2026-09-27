package rigdiff

import (
	"fmt"
	"io"
	"strings"

	"github.com/rigfile/rigfile/internal/manifest"
)

// Result is a full comparison of two versions of a rig.
type Result struct {
	From    string       `json:"from"`
	To      string       `json:"to"`
	Entries []Entry      `json:"entries"`
	Files   []FileChange `json:"files"`
}

// Rigs compares two rig directories. from and to are labels (versions or commits) for the output.
func Rigs(dirA, dirB, from, to string) (*Result, error) {
	la, err := manifest.Load(dirA)
	if err != nil {
		return nil, fmt.Errorf("before: %w", err)
	}
	lb, err := manifest.Load(dirB)
	if err != nil {
		return nil, fmt.Errorf("after: %w", err)
	}
	files, err := Files(la.Dir, lb.Dir, lb.M)
	if err != nil {
		return nil, err
	}
	return &Result{From: from, To: to, Entries: Manifests(la.M, lb.M), Files: files}, nil
}

// Review lists every note that asks for attention.
func (r *Result) Review() []string {
	var out []string
	for _, e := range r.Entries {
		for _, n := range e.Notes {
			if n.Level == Review {
				out = append(out, fmt.Sprintf("%s %s: %s", e.Category, e.Key, n.Text))
			}
		}
	}
	for _, f := range r.Files {
		for _, n := range f.Notes {
			if n.Level == Review {
				out = append(out, fmt.Sprintf("file %s: %s", f.Path, n.Text))
			}
		}
	}
	return out
}

// Empty reports whether nothing changed.
func (r *Result) Empty() bool { return len(r.Entries) == 0 && len(r.Files) == 0 }

var mark = map[Change]string{Added: "+", Removed: "-", Changed: "~"}

// Render writes the comparison as text. With diffs=false the per-file diff bodies are left out.
func (r *Result) Render(w io.Writer, diffs bool) {
	fmt.Fprintf(w, "%s -> %s\n", r.From, r.To)
	if r.Empty() {
		fmt.Fprintln(w, "  no changes")
		return
	}
	if rv := r.Review(); len(rv) > 0 {
		fmt.Fprintf(w, "\nLook at these before you accept (%d):\n", len(rv))
		for _, s := range rv {
			fmt.Fprintf(w, "  ! %s\n", s)
		}
	}
	if len(r.Entries) > 0 {
		fmt.Fprintln(w, "\nManifest:")
		for _, e := range r.Entries {
			line := fmt.Sprintf("  %s %s %s", mark[e.Change], e.Category, e.Key)
			switch e.Change {
			case Added:
				if e.After != "" {
					line += "   " + e.After
				}
			case Removed:
				if e.Before != "" {
					line += "   (was " + e.Before + ")"
				}
			case Changed:
				line += "   " + e.Before + "  ->  " + e.After
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(r.Files) > 0 {
		fmt.Fprintln(w, "\nFiles:")
		for _, f := range r.Files {
			extra := ""
			if f.Omitted != "" {
				extra = "   (" + f.Omitted + ")"
			}
			fmt.Fprintf(w, "  %s %s%s\n", mark[f.Change], f.Path, extra)
			if diffs && f.Diff != "" {
				for _, l := range strings.Split(strings.TrimRight(f.Diff, "\n"), "\n") {
					fmt.Fprintf(w, "      %s\n", l)
				}
			}
		}
	}
}
