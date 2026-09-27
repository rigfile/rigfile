// Package publish turns a rig (a directory, or a capture of this machine) into a clean repository that is safe to
// share (docs/sharing.md §6). Stages, in order, each of which can stop the run before anything is written:
// scrub (home paths rewritten, personal information listed), secret scan (blocks), manifest validation (blocks),
// stage to a temporary directory, scan the staged tree again from disk, and only then write the output.
package publish

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/scan"
)

// Finding is one thing the scrub found, never including the value itself.
type Finding struct {
	Kind   string // secret | personal | rewritten
	Rule   string // rule id, or email | phone | username | hostname | home-path
	File   string
	Line   int
	Sample string // masked: enough to find it, not enough to leak it
}

// Input is a rig as files: rig-relative slash paths to contents, including rigfile.yaml.
type Input struct {
	Files       map[string][]byte
	Home        string // this machine's home directory
	User, Host  string // the OS user and host name, looked for in text
	AckPersonal bool   // the user reviewed the personal-information list and accepts it
	Scanner     func() (*scan.Scanner, error)
}

// Prepared is the outcome of every stage except writing.
type Prepared struct {
	Files     map[string][]byte // what would be published (after scrubbing), incl. README.md and .gitignore
	Manifest  *manifest.Manifest
	Secrets   []Finding
	Personal  []Finding
	Rewritten []Finding
	Problems  []manifest.Problem
	Proof     Proof
	AckNeeded bool
}

// unpinned are the warnings about external packages and skills that are not pinned: fine locally, refused when publishing.
func (p *Prepared) unpinned() []manifest.Problem {
	var out []manifest.Problem
	for _, pr := range p.Problems {
		if pr.Level == manifest.Warn && strings.Contains(pr.Msg, "pinned") && !strings.Contains(pr.Msg, "cannot verify") {
			out = append(out, pr)
		}
	}
	return out
}

// Proof is the result of re-scanning the staged tree.
type Proof struct {
	Files    int
	Findings int
}

// Blocked lists why the rig cannot be published yet ("" entries never appear); empty means it can.
func (p *Prepared) Blocked() []string {
	var out []string
	if n := len(p.Secrets); n > 0 {
		out = append(out, fmt.Sprintf("%d secret finding(s): a rig with an unresolved secret is never published (no override)", n))
	}
	if manifest.HasErrors(p.Problems) {
		out = append(out, "the rig has errors (listed above)")
	}
	if n := len(p.unpinned()); n > 0 {
		out = append(out, fmt.Sprintf("%d package(s) are not pinned to an exact version: a public rig must pin what it runs", n))
	}
	if n := len(p.Personal); n > 0 && p.AckNeeded {
		out = append(out, fmt.Sprintf("%d personal-information item(s) to review: re-run with --ack-personal once you have checked them", n))
	}
	if p.Proof.Findings > 0 {
		out = append(out, "the staged output still scans dirty")
	}
	return out
}

var (
	homeRe  = regexp.MustCompile(`(?:/Users/|/home/)[A-Za-z0-9._-]+(?:/|\b)|[A-Za-z]:[\\/]+Users[\\/]+[A-Za-z0-9._ ~-]+(?:[\\/]+|\b)`)
	emailRe = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`)
	phoneRe = regexp.MustCompile(`(?:^|[^0-9A-Za-z])((?:\+\d{1,3}[ .-]?)?(?:\(\d{3}\)[ .-]?|\d{3}[ .-])\d{3}[ .-]\d{4})(?:$|[^0-9A-Za-z])`)
)

// placeholder e-mail domains and local parts that are not a person's address.
var (
	fakeDomains = []string{"example.com", "example.org", "example.net", "localhost", "anthropic.com", "users.noreply.github.com"}
	fakeSuffix  = []string{".test", ".invalid", ".example", ".local"}
	fakeLocals  = map[string]bool{"git": true, "noreply": true, "no-reply": true, "user": true, "you": true, "name": true, "email": true}
)

func isPlaceholderEmail(e string) bool {
	local, domain, _ := strings.Cut(strings.ToLower(e), "@")
	if fakeLocals[local] {
		return true
	}
	for _, d := range fakeDomains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	for _, s := range fakeSuffix {
		if strings.HasSuffix(domain, s) {
			return true
		}
	}
	return false
}

func mask(s string) string {
	if len(s) <= 3 {
		return strings.Repeat("*", len(s))
	}
	return s[:2] + strings.Repeat("*", len(s)-2)
}

func isText(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	for _, c := range b[:n] {
		if c == 0 {
			return false
		}
	}
	return true
}

// Prepare runs every stage except the final write.
func Prepare(in Input) (*Prepared, error) {
	if in.Scanner == nil {
		in.Scanner = func() (*scan.Scanner, error) { return scan.New(scan.Options{}) }
	}
	sc, err := in.Scanner()
	if err != nil {
		return nil, err
	}
	p := &Prepared{Files: map[string][]byte{}, AckNeeded: !in.AckPersonal}
	names := sortedKeys(in.Files)

	// 1. scrub: rewrite home paths, list personal information
	for _, name := range names {
		data := in.Files[name]
		if !isText(data) {
			p.Files[name] = data
			continue
		}
		text := string(data)
		text = rewriteHomePaths(text, name, &p.Rewritten)
		p.Personal = append(p.Personal, personalIn(text, name, in)...)
		p.Files[name] = []byte(text)
	}

	// 2. secrets: every file and file name goes through the full scanner
	for _, name := range names {
		for _, f := range sc.ScanFile(name, p.Files[name]) {
			p.Secrets = append(p.Secrets, Finding{Kind: "secret", Rule: f.RuleID, File: name, Line: f.Line, Sample: f.Description})
		}
	}

	// 3. the manifest must parse, validate and pass the consistency checks
	if _, ok := p.Files[manifest.FileName]; !ok {
		return nil, fmt.Errorf("publish: the rig has no %s", manifest.FileName)
	}
	p.Files["README.md"] = []byte{} // filled below once the manifest is known
	stage, err := os.MkdirTemp("", "rigfile-publish-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := writeTree(stage, p.Files); err != nil {
		return nil, err
	}
	loaded, err := manifest.Load(stage)
	if err != nil {
		return nil, fmt.Errorf("publish: the rig does not validate: %w", err)
	}
	p.Manifest = loaded.M
	p.Problems = manifest.Check(loaded)
	if strings.HasPrefix(loaded.M.Name, "rigfile/") {
		return nil, fmt.Errorf("publish: the name %q is reserved for the Rigfile project", loaded.M.Name)
	}

	// 4. generated files, then prove: scan the staged tree again from disk
	p.Files["README.md"] = []byte(readme(loaded.M))
	if _, ok := p.Files[".gitignore"]; !ok {
		p.Files[".gitignore"] = []byte(gitignore)
	}
	if err := os.RemoveAll(stage); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return nil, err
	}
	if err := writeTree(stage, p.Files); err != nil {
		return nil, err
	}
	err = filepath.WalkDir(stage, func(fp string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(stage, fp)
		data, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		p.Proof.Files++
		p.Proof.Findings += len(sc.ScanFile(filepath.ToSlash(rel), data))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

const gitignore = ".env\n.env.*\n!.env.example\n*.pem\n*.key\n*.p12\nsecrets.*\n.DS_Store\n"

func writeTree(root string, files map[string][]byte) error {
	for name, data := range files {
		dst := filepath.Join(root, filepath.FromSlash(name))
		if !strings.HasPrefix(filepath.Clean(dst), filepath.Clean(root)+string(filepath.Separator)) {
			return fmt.Errorf("publish: unsafe path %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(dst, data, mode); err != nil {
			return err
		}
	}
	return nil
}

// Write writes a Prepared rig into out, which must be empty or absent, and only if nothing blocks it.
func (p *Prepared) Write(out string) error {
	if b := p.Blocked(); len(b) > 0 {
		return fmt.Errorf("publish: blocked: %s", strings.Join(b, "; "))
	}
	if ents, err := os.ReadDir(out); err == nil && len(ents) > 0 {
		return fmt.Errorf("publish: %s already has files; choose an empty directory", out)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	return writeTree(out, p.Files)
}

func rewriteHomePaths(text, file string, rewritten *[]Finding) string {
	n := 0
	out := homeRe.ReplaceAllStringFunc(text, func(m string) string {
		n++
		if strings.HasSuffix(m, "/") || strings.HasSuffix(m, `\`) {
			return "~/"
		}
		return "~"
	})
	if n > 0 {
		*rewritten = append(*rewritten, Finding{Kind: "rewritten", Rule: "home-path", File: file, Sample: fmt.Sprintf("%d absolute home path(s) became ~/", n)})
	}
	return out
}

func personalIn(text, file string, in Input) []Finding {
	var out []Finding
	lines := strings.Split(text, "\n")
	userRe := wordRe(in.User)
	hostRe := wordRe(in.Host)
	for i, l := range lines {
		for _, m := range emailRe.FindAllString(l, -1) {
			if !isPlaceholderEmail(m) {
				out = append(out, Finding{Kind: "personal", Rule: "email", File: file, Line: i + 1, Sample: mask(m)})
			}
		}
		for _, m := range phoneRe.FindAllStringSubmatch(l, -1) {
			out = append(out, Finding{Kind: "personal", Rule: "phone", File: file, Line: i + 1, Sample: mask(m[1])})
		}
		if userRe != nil && userRe.MatchString(l) {
			out = append(out, Finding{Kind: "personal", Rule: "username", File: file, Line: i + 1, Sample: mask(in.User)})
		}
		if hostRe != nil && hostRe.MatchString(l) {
			out = append(out, Finding{Kind: "personal", Rule: "hostname", File: file, Line: i + 1, Sample: mask(in.Host)})
		}
	}
	return out
}

// genericAccounts are user and host names that identify no one (CI images, containers, default accounts); flagging them
// would only bury the real findings (`rigfile.dev` in every manifest matched a container user called dev).
var genericAccounts = map[string]bool{"dev": true, "root": true, "user": true, "admin": true, "test": true, "ubuntu": true, "runner": true,
	"docker": true, "builder": true, "vagrant": true, "node": true, "localhost": true, "github": true, "circleci": true, "administrator": true}

func wordRe(w string) *regexp.Regexp {
	if len(w) < 3 || genericAccounts[strings.ToLower(w)] {
		return nil
	}
	return regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(w) + `(?:$|[^A-Za-z0-9])`)
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FromDir reads a rig directory as an Input: rigfile.yaml, rigfile.lock if present, and only the files the manifest
// references (a skill's whole folder, instruction, agent, command and hook script files). Nothing else in the
// directory is copied.
func FromDir(dir string) (map[string][]byte, error) {
	l, err := manifest.Load(dir)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	add := func(rel string) error {
		abs, err := manifest.PathInside(l.Dir, rel)
		if err != nil {
			return err
		}
		st, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		if st.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link; symlinks are never published", rel)
		}
		if !st.IsDir() {
			b, err := os.ReadFile(abs)
			if err != nil {
				return err
			}
			files[path.Clean(rel)] = b
			return nil
		}
		return filepath.WalkDir(abs, func(fp string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%s contains a symbolic link; symlinks are never published", rel)
			}
			if d.IsDir() {
				return nil
			}
			sub, _ := filepath.Rel(abs, fp)
			b, err := os.ReadFile(fp)
			if err != nil {
				return err
			}
			files[path.Join(path.Clean(rel), filepath.ToSlash(sub))] = b
			return nil
		})
	}
	raw, err := os.ReadFile(filepath.Join(l.Dir, manifest.FileName))
	if err != nil {
		return nil, err
	}
	files[manifest.FileName] = raw
	if b, err := os.ReadFile(filepath.Join(l.Dir, "rigfile.lock")); err == nil {
		files["rigfile.lock"] = b
	}
	m := l.M
	for _, x := range m.Instructions {
		if err := add(x.File); err != nil {
			return nil, err
		}
	}
	for _, x := range m.Skills {
		if x.Path != "" {
			if err := add(x.Path); err != nil {
				return nil, err
			}
		}
	}
	for _, x := range m.Agents {
		if err := add(x.Path); err != nil {
			return nil, err
		}
	}
	for _, x := range m.Commands {
		if err := add(x.Path); err != nil {
			return nil, err
		}
	}
	for _, h := range m.Hooks {
		runs := []string{h.Run.Single}
		for _, r := range h.Run.PerOS {
			runs = append(runs, r)
		}
		for _, r := range runs {
			if r == "" {
				continue
			}
			if _, builtin := manifest.Builtin(r); builtin {
				continue
			}
			if err := add(r); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

// Audit is the server-side check of a rig that already exists as a directory: the same secret scanner and manifest
// checks that gate `rigfile publish`, without the local-only steps (home-path rewriting, personal information).
type Audit struct {
	Secrets  []Finding
	Problems []manifest.Problem
	Unpinned []manifest.Problem // warnings about unpinned packages: fine for a private rig, refused for a public one
	Manifest *manifest.Manifest
	Files    int
}

// AuditDir scans every file and file name under dir and validates its manifest.
func AuditDir(dir string, sc *scan.Scanner) (*Audit, error) {
	a := &Audit{}
	err := filepath.WalkDir(dir, func(fp string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, fp)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		a.Files++
		for _, f := range sc.ScanFile(rel, data) {
			a.Secrets = append(a.Secrets, Finding{Kind: "secret", Rule: f.RuleID, File: rel, Line: f.Line, Sample: f.Description})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	l, err := manifest.Load(dir)
	if err != nil {
		return nil, err
	}
	a.Manifest = l.M
	a.Problems = manifest.Check(l)
	p := &Prepared{Problems: a.Problems}
	a.Unpinned = p.unpinned()
	return a, nil
}

// Tarball packs the prepared rig as a deterministic gzip tarball (sorted paths, fixed times and ownership), the exact
// bytes the registry stores and hashes. It refuses a rig that is blocked.
func (p *Prepared) Tarball() ([]byte, error) {
	if b := p.Blocked(); len(b) > 0 {
		return nil, fmt.Errorf("publish: blocked: %s", strings.Join(b, "; "))
	}
	names := make([]string, 0, len(p.Files))
	for n := range p.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	gw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	tw := tar.NewWriter(gw)
	for _, n := range names {
		mode := int64(0o644)
		if strings.HasSuffix(n, ".sh") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(p.Files[n]))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(p.Files[n]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
