package rigdiff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

// Limits keep a diff of a hostile rig cheap. A file over the limit is reported by size only.
const (
	MaxDiffFileBytes = 256 << 10
	MaxDiffLines     = 3000
	MaxDiffTotal     = 512 << 10 // all rendered diff text together
	contextLines     = 3
)

// FileChange is one file-level change.
type FileChange struct {
	Path    string `json:"path"`
	Change  Change `json:"change"`
	SizeA   int64  `json:"size_before,omitempty"`
	SizeB   int64  `json:"size_after,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
	Diff    string `json:"diff,omitempty"`    // unified, for small text files
	Omitted string `json:"omitted,omitempty"` // why there is no diff text
	Notes   []Note `json:"notes,omitempty"`
}

type fileInfo struct {
	size int64
	sum  string
	abs  string
}

func walk(root string) (map[string]fileInfo, error) {
	out := map[string]fileInfo{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // links and devices are not part of a rig; the extractor refuses them
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = fileInfo{int64(len(b)), hex.EncodeToString(sum[:]), p}
		return nil
	})
	return out, err
}

func isText(b []byte) bool { return utf8.Valid(b) && !bytes.Contains(b, []byte{0}) }

// Files compares two rig directories. rigfile.yaml is left to the manifest comparison, and rigfile.lock is machine written.
func Files(dirA, dirB string, m *manifest.Manifest) ([]FileChange, error) {
	fa, err := walk(dirA)
	if err != nil {
		return nil, err
	}
	fb, err := walk(dirB)
	if err != nil {
		return nil, err
	}
	skip := func(p string) bool { return p == manifest.FileName || p == "rigfile.lock" }
	all := map[string]bool{}
	for p := range fa {
		all[p] = true
	}
	for p := range fb {
		all[p] = true
	}
	paths := make([]string, 0, len(all))
	for p := range all {
		if !skip(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	budget := MaxDiffTotal
	var out []FileChange
	for _, p := range paths {
		a, inA := fa[p]
		b, inB := fb[p]
		fc := FileChange{Path: p, SizeA: a.size, SizeB: b.size}
		switch {
		case inA && !inB:
			fc.Change = Removed
		case !inA && inB:
			fc.Change = Added
		case a.sum != b.sum:
			fc.Change = Changed
		default:
			continue
		}
		var ca, cb []byte
		if inA {
			ca, _ = os.ReadFile(a.abs)
		}
		if inB {
			cb, _ = os.ReadFile(b.abs)
		}
		switch {
		case (inA && !isText(ca)) || (inB && !isText(cb)):
			fc.Binary, fc.Omitted = true, "binary file"
		case len(ca) > MaxDiffFileBytes || len(cb) > MaxDiffFileBytes:
			fc.Omitted = "file too large to diff"
		default:
			d, why := unified(p, string(ca), string(cb), inA, inB)
			switch {
			case why != "":
				fc.Omitted = why
			case len(d) > budget:
				fc.Omitted = "diff output limit reached"
			default:
				fc.Diff = d
				budget -= len(d)
			}
		}
		fc.Notes = fileNotes(fc, m)
		out = append(out, fc)
	}
	return out, nil
}

var scriptExt = map[string]bool{".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".bat": true, ".cmd": true, ".py": true, ".js": true, ".mjs": true, ".ts": true, ".rb": true, ".pl": true}

// fileNotes flags files the agent will follow or the machine will run.
func fileNotes(fc FileChange, m *manifest.Manifest) []Note {
	if fc.Change == Removed {
		return nil
	}
	verb := map[Change]string{Added: "adds", Changed: "changes"}[fc.Change]
	var ns []Note
	if m != nil {
		for _, x := range m.Instructions {
			if x.File == fc.Path {
				ns = append(ns, note(Review, "%s text the agent will follow (instruction %s)", verb, x.Key()))
			}
		}
		for _, x := range m.Skills {
			if x.Path != "" && (fc.Path == x.Path || strings.HasPrefix(fc.Path, strings.TrimRight(x.Path, "/")+"/")) {
				ns = append(ns, note(Review, "%s text the agent will follow (skill %s)", verb, x.Key()))
			}
		}
		for _, x := range m.Agents {
			if x.Path == fc.Path {
				ns = append(ns, note(Review, "%s a subagent definition (%s)", verb, x.Key()))
			}
		}
		for _, x := range m.Commands {
			if x.Path == fc.Path {
				ns = append(ns, note(Review, "%s a slash command (%s)", verb, x.Key()))
			}
		}
	}
	if scriptExt[strings.ToLower(path.Ext(fc.Path))] {
		ns = append(ns, note(Review, "%s a script", verb))
	}
	return ns
}

// unified renders a unified diff of two texts. It returns ("", reason) when the input is too large to diff cheaply.
func unified(name, a, b string, inA, inB bool) (string, string) {
	la, lb := splitLines(a), splitLines(b)
	if len(la) > MaxDiffLines || len(lb) > MaxDiffLines {
		return "", "file too long to diff"
	}
	ops := diffOps(la, lb)
	hdrA, hdrB := "a/"+name, "b/"+name
	if !inA {
		hdrA = "/dev/null"
	}
	if !inB {
		hdrB = "/dev/null"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", hdrA, hdrB)
	// group edits into hunks with context
	type op struct {
		kind byte // ' ', '-', '+'
		text string
	}
	var seq []op
	for _, o := range ops {
		seq = append(seq, op{o.kind, o.text})
	}
	i := 0
	for i < len(seq) {
		for i < len(seq) && seq[i].kind == ' ' {
			i++
		}
		if i >= len(seq) {
			break
		}
		start := i - contextLines
		if start < 0 {
			start = 0
		}
		end := i
		lastChange := i
		for end < len(seq) {
			if seq[end].kind != ' ' {
				lastChange = end
			}
			if end-lastChange > 2*contextLines {
				break
			}
			end++
		}
		end = lastChange + contextLines + 1
		if end > len(seq) {
			end = len(seq)
		}
		na, nb := 0, 0
		for _, o := range seq[:start] {
			if o.kind != '+' {
				na++
			}
			if o.kind != '-' {
				nb++
			}
		}
		ca, cb := 0, 0
		for _, o := range seq[start:end] {
			if o.kind != '+' {
				ca++
			}
			if o.kind != '-' {
				cb++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", na+1, ca, nb+1, cb)
		for _, o := range seq[start:end] {
			sb.WriteByte(o.kind)
			sb.WriteString(o.text)
			sb.WriteByte('\n')
		}
		i = end
	}
	return sb.String(), ""
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}

type editOp struct {
	kind byte
	text string
}

// diffOps is a longest-common-subsequence diff. Inputs are capped at MaxDiffLines, so the O(n*m) table is bounded.
func diffOps(a, b []string) []editOp {
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []editOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, editOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, editOp{'-', a[i]})
			i++
		default:
			out = append(out, editOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, editOp{'-', a[i]})
	}
	for ; j < m; j++ {
		out = append(out, editOp{'+', b[j]})
	}
	return out
}
