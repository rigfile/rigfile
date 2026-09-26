package githook

import (
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/scan"
)

// AllowFile is the repo-level allow list (internal/scan/allow.go).
const AllowFile = ".rigfile-allow"

// Result is what a scan found.
type Result struct {
	Findings []scan.Finding
	Notes    []string
	Files    int // files examined
}

// ScanStaged scans the index: every added/changed file's name and size, and the ADDED lines of its
// content. The allow list is read from HEAD (so a commit cannot allow-list its own secret; a staged change
// to it takes effect from the next commit), or from the index for the very first commit.
func (g Git) ScanStaged(sc *scan.Scanner) (Result, error) {
	cat, err := g.newCatBatch()
	if err != nil {
		return Result{}, err
	}
	defer cat.close()
	changes, err := g.stagedChanges()
	if err != nil {
		return Result{}, err
	}
	res := Result{}
	// allow list: HEAD's version; for the very first commit (no HEAD) the index version
	allow, note, err := loadAllow(cat, "HEAD:"+AllowFile, ":"+AllowFile)
	if err != nil {
		return Result{}, err
	}
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	for _, c := range changes {
		if c.Path == AllowFile {
			res.Notes = append(res.Notes, AllowFile+" is part of this commit: its entries take effect from the NEXT commit, so commit the allow-list change on its own first")
		}
	}
	found, n, err := g.scanChanges(sc, cat, changes, func(c Change) ([][2]int, bool, error) {
		return g.addedRanges([]string{"--cached"}, c.Path, c.Status)
	})
	if err != nil {
		return Result{}, err
	}
	res.Files = n
	res.Findings = allow.Filter(found)
	return res, nil
}

// ScanCommits scans the given commits (oldest first), each against its first parent. The allow list is
// read from allowRev ("" = none).
func (g Git) ScanCommits(sc *scan.Scanner, commits []string, allowRev string) (Result, error) {
	cat, err := g.newCatBatch()
	if err != nil {
		return Result{}, err
	}
	defer cat.close()
	res := Result{}
	var allow *scan.Allow
	if allowRev != "" {
		var note string
		if allow, note, err = loadAllow(cat, allowRev+":"+AllowFile); err != nil {
			return Result{}, err
		}
		if note != "" {
			res.Notes = append(res.Notes, note)
		}
	}
	var all []scan.Finding
	seen := map[string]bool{}
	for _, c := range commits {
		changes, parent, err := g.commitChanges(c)
		if err != nil {
			return Result{}, err
		}
		for _, ch := range changes {
			if ch.Path == AllowFile && allowRev != "" {
				res.Notes = append(res.Notes, fmt.Sprintf("commit %s changes %s; its entries were not applied to this scan", short(c), AllowFile))
			}
		}
		args := []string{c}
		if parent != "" {
			args = []string{parent, c}
		}
		found, n, err := g.scanChanges(sc, cat, changes, func(ch Change) ([][2]int, bool, error) {
			return g.addedRanges(args, ch.Path, ch.Status)
		})
		if err != nil {
			return Result{}, err
		}
		res.Files += n
		for _, f := range found {
			f.Commit = short(c)
			if k := f.Path + "\x00" + f.Fingerprint; !seen[k] { // the same finding in a later commit adds nothing
				seen[k] = true
				all = append(all, f)
			}
		}
	}
	res.Findings = allow.Filter(all)
	sort.SliceStable(res.Findings, func(i, j int) bool { return res.Findings[i].Path < res.Findings[j].Path })
	return res, nil
}

func short(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
}

// loadAllow reads .rigfile-allow from the first spec that exists ("HEAD:.rigfile-allow", then ":.rigfile-allow"
// for a repository with no commits yet). A missing file is an empty list.
func loadAllow(cat *catBatch, specs ...string) (*scan.Allow, string, error) {
	for i, spec := range specs {
		if i == 1 {
			// fall back to the index only when there is no HEAD at all
			if _, found, err := cat.lookup("HEAD", 1<<10); err != nil || found {
				return nil, "", err
			}
		}
		b, found, err := cat.lookup(spec, 1<<20)
		if err != nil {
			return nil, "", err
		}
		if !found {
			continue
		}
		a, err := scan.ParseAllow(strings.NewReader(string(b)))
		if err != nil {
			return nil, AllowFile + " is invalid and was ignored: " + err.Error(), nil
		}
		return a, "", nil
	}
	return nil, "", nil
}

func (g Git) scanChanges(sc *scan.Scanner, cat *catBatch, changes []Change, ranges func(Change) ([][2]int, bool, error)) ([]scan.Finding, int, error) {
	var out []scan.Finding
	n := 0
	for _, c := range changes {
		if c.Mode == "160000" { // submodule pointer
			continue
		}
		n++
		if c.Mode == "120000" { // symlink: the blob is the link target text, judge the name only
			out = append(out, sc.ScanName(c.Path, 0)...)
			continue
		}
		data, size, err := cat.read(c.OID, sc.MaxFileBytes())
		if err != nil {
			return nil, n, err
		}
		if data == nil { // over the size limit: not read
			out = append(out, sc.ScanName(c.Path, size)...)
			continue
		}
		fs := sc.ScanFile(c.Path, data)
		var rs [][2]int
		all := false
		needRanges := false
		for _, f := range fs {
			if f.Kind == "content" {
				needRanges = true
			}
		}
		if needRanges {
			if rs, all, err = ranges(c); err != nil {
				return nil, n, err
			}
		}
		for _, f := range fs {
			if f.Kind != "content" || all || inRanges(rs, f.Line, f.End) {
				out = append(out, f)
			}
		}
	}
	return out, n, nil
}

func inRanges(rs [][2]int, start, end int) bool {
	for _, r := range rs {
		if start <= r[1] && end >= r[0] {
			return true
		}
	}
	return false
}

// Render is the human message for a blocked operation. It never contains a matched value.
func Render(phase string, res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n✘ rigfile: %s blocked: possible secret(s) or credential file(s) in %s\n\n", phase, map[string]string{
		"commit": "the staged changes", "push": "the commits being pushed", "ref update": "the new commit(s)"}[phase])
	for _, f := range res.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		if f.Commit != "" {
			loc += " (commit " + f.Commit + ")"
		}
		fmt.Fprintf(&b, "  %s\n      %s: %s   [fp:%s]\n", loc, f.RuleID, f.Description, f.Fingerprint)
	}
	for _, n := range res.Notes {
		fmt.Fprintf(&b, "\nnote: %s\n", n)
	}
	b.WriteString(`
What to do:
  • Remove the secret. Read it from an environment variable, or store it with
    ` + "`rigfile secrets set <name>`" + ` and reference it as secret://<name>.
  • If the file should never be tracked: add it to .gitignore and run ` + "`git rm --cached <file>`" + `.
  • If it is a false positive: add ` + "`fp:<fingerprint>`" + ` (or a path glob) to .rigfile-allow and commit that
    file on its own first; entries apply from the next commit.
  • If a secret was ever pushed anywhere, ROTATE it: deleting it from git history is not enough.
`)
	return b.String()
}

// absGitDir avoids a subprocess when git already told us (hooks run with GIT_DIR exported).
func (g Git) absGitDir() (string, error) {
	if d := os.Getenv("GIT_DIR"); d != "" {
		if filepath.IsAbs(d) {
			return d, nil
		}
		base := g.Dir
		if base == "" {
			base, _ = os.Getwd()
		}
		return filepath.Join(base, d), nil
	}
	d, err := g.line("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", errNoRepo
	}
	return d, nil
}

// scannedFile is where pre-commit records the trees it approved, so the reference-transaction backstop
// does not scan them again.
func (g Git) scannedFile() (string, error) {
	d, err := g.absGitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "rigfile-scanned-trees"), nil
}

func (g Git) recordScanned(tree string) {
	p, err := g.scannedFile()
	if err != nil {
		return
	}
	var lines []string
	if b, err := os.ReadFile(p); err == nil {
		lines = strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	lines = append(lines, tree)
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	_ = os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// scannedSet loads the approved trees once.
func (g Git) scannedSet() map[string]bool {
	set := map[string]bool{}
	p, err := g.scannedFile()
	if err != nil {
		return set
	}
	if b, err := os.ReadFile(p); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l != "" {
				set[l] = true
			}
		}
	}
	return set
}

func (g Git) wasScanned(tree string) bool { return g.scannedSet()[tree] }

// commitTrees lists `rev-list --reverse <args>` as (commit, tree) pairs in ONE git call.
func (g Git) commitTrees(args ...string) ([][2]string, error) {
	b, err := g.out(append([]string{"rev-list", "--reverse", "--format=%T"}, args...)...)
	if err != nil {
		return nil, err
	}
	var out [][2]string
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := 0; i+1 < len(lines); i += 2 {
		out = append(out, [2]string{strings.TrimPrefix(lines[i], "commit "), lines[i+1]})
	}
	return out, nil
}

// looseCommit reads a commit's tree and parents straight from a LOOSE object file, with no subprocess. A
// commit git has just created is loose until the next gc; anything else (packed, missing, odd layout) reports
// ok=false and the caller falls back to git.
func (g Git) looseCommit(oid string) (tree string, parents []string, ok bool) {
	if len(oid) != 40 {
		return "", nil, false
	}
	dir, err := g.absGitDir()
	if err != nil {
		return "", nil, false
	}
	obj := os.Getenv("GIT_OBJECT_DIRECTORY")
	if obj == "" {
		common := dir
		if b, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil { // linked worktree
			c := strings.TrimSpace(string(b))
			if !filepath.IsAbs(c) {
				c = filepath.Join(dir, c)
			}
			common = c
		}
		obj = filepath.Join(common, "objects")
	}
	f, err := os.Open(filepath.Join(obj, oid[:2], oid[2:]))
	if err != nil {
		return "", nil, false
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return "", nil, false
	}
	defer zr.Close()
	buf := make([]byte, 1024) // header, tree, parents: well inside the first kilobyte
	n, _ := io.ReadFull(zr, buf)
	head := string(buf[:n])
	if !strings.HasPrefix(head, "commit ") {
		return "", nil, false
	}
	i := strings.IndexByte(head, 0)
	if i < 0 {
		return "", nil, false
	}
	for _, l := range strings.Split(head[i+1:], "\n") {
		switch {
		case strings.HasPrefix(l, "tree "):
			tree = strings.TrimPrefix(l, "tree ")
		case strings.HasPrefix(l, "parent "):
			parents = append(parents, strings.TrimPrefix(l, "parent "))
		case l == "":
			return tree, parents, len(tree) == 40
		}
	}
	return tree, parents, len(tree) == 40
}

// ScanHistory scans up to max commits of every ref (newest first when truncated), oldest first in the result,
// against each commit's first parent, with the allow list from HEAD. It reads history only; it never
// rewrites it.
func (g Git) ScanHistory(sc *scan.Scanner, max int) (res Result, commits int, truncated bool, err error) {
	args := []string{"rev-list", "--all"}
	if max > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", max+1))
	}
	b, err := g.out(args...)
	if err != nil {
		return Result{}, 0, false, err
	}
	list := strings.Fields(string(b))
	if max > 0 && len(list) > max {
		list, truncated = list[:max], true
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 { // oldest first
		list[i], list[j] = list[j], list[i]
	}
	allowRev := ""
	if g.ok("rev-parse", "--verify", "-q", "HEAD") {
		allowRev = "HEAD"
	}
	res, err = g.ScanCommits(sc, list, allowRev)
	return res, len(list), truncated, err
}

// PresentAtHead reports whether the finding is still in the file at HEAD (same fingerprint).
func (g Git) PresentAtHead(sc *scan.Scanner, f scan.Finding) bool {
	data, err := g.out("show", "HEAD:"+f.Path)
	if err != nil {
		return false
	}
	for _, h := range sc.ScanFile(f.Path, data) {
		if h.Fingerprint == f.Fingerprint {
			return true
		}
	}
	return false
}
