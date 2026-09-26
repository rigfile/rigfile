package githook

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/scan"
)

// Fake token assembled at run time (see internal/scan tests): never a complete credential in the source.
var fakeGH = "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"

type repo struct {
	t   *testing.T
	dir string
	g   Git
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	// isolate from the developer's real git configuration (their global hooks would run inside these repos)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	dir := t.TempDir()
	r := &repo{t: t, dir: dir, g: Git{Dir: dir}}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.name", "Test")
	r.git("config", "user.email", "test@example.test")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	c := exec.Command("git", args...)
	c.Dir = r.dir
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		r.t.Fatalf("git %v: %v: %s", args, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

func (r *repo) write(rel, content string) {
	r.t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and commits WITHOUT hooks (no hooks are installed in these tests; --no-verify
// documents intent) and returns the commit id.
func (r *repo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--no-verify", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func scanner(t *testing.T, o scan.Options) *scan.Scanner {
	t.Helper()
	s, err := scan.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStagedBlocksSecretsAndCredentialFilesButNotExamples(t *testing.T) {
	r := newRepo(t)
	r.write("README.md", "hello\n")
	r.commit("init")
	r.write("src/app.py", "import os\n\ntoken = \""+fakeGH+"\"\n")
	r.write(".env", "A=1\n")
	r.write(".env.example", "A=\n")
	r.git("add", "-A")
	res, err := r.g.ScanStaged(scanner(t, scan.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, f := range res.Findings {
		kinds = append(kinds, f.Kind+":"+f.Path)
		if f.Kind == "content" && f.Line != 3 {
			t.Errorf("wrong line %d", f.Line)
		}
	}
	joined := strings.Join(kinds, " ")
	if !strings.Contains(joined, "content:src/app.py") || !strings.Contains(joined, "name:.env") || strings.Contains(joined, ".env.example") {
		t.Fatalf("%v", kinds)
	}
	out := Render("commit", res)
	if strings.Contains(out, fakeGH) || strings.Contains(out, "wJ4kP9xQm2Rt7V") {
		t.Fatal("the rendered message contains the secret value")
	}
	if !strings.Contains(out, "src/app.py:3") || !strings.Contains(out, "ROTATE") || !strings.Contains(out, "[fp:") {
		t.Fatalf("message lacks location or guidance:\n%s", out)
	}
}

func TestOnlyAddedLinesAreJudged(t *testing.T) {
	r := newRepo(t)
	r.write("legacy.py", "a = 1\ntoken = \""+fakeGH+"\"\nb = 2\n")
	r.commit("old secret, committed long ago")
	sc := scanner(t, scan.Options{})
	// an unrelated edit to another line of the same file must not be blocked by the old secret
	r.write("legacy.py", "a = 1\ntoken = \""+fakeGH+"\"\nb = 3\n")
	r.git("add", "-A")
	if res, err := r.g.ScanStaged(sc); err != nil || len(res.Findings) != 0 {
		t.Fatalf("pre-existing secret must not block an unrelated edit: %+v %v", res.Findings, err)
	}
	// touching the secret's own line does
	r.write("legacy.py", "a = 1\ntoken = \""+fakeGH+"\"  # renewed\nb = 3\n")
	r.git("add", "-A")
	if res, _ := r.g.ScanStaged(sc); len(res.Findings) != 1 || res.Findings[0].Line != 2 {
		t.Fatalf("%+v", res.Findings)
	}
}

func TestDeletedBinaryLargeAndSymlinkedFiles(t *testing.T) {
	r := newRepo(t)
	r.write("gone.py", "token = \""+fakeGH+"\"\n")
	r.commit("with secret")
	r.git("rm", "-q", "gone.py")
	r.write("blob.bin", "token = "+fakeGH+"\x00\x01")
	r.write("big.txt", strings.Repeat("x", 3000))
	if err := os.Symlink("../../etc/passwd", filepath.Join(r.dir, "link")); err != nil {
		t.Log("symlinks unavailable")
	}
	r.git("add", "-A")
	res, err := r.g.ScanStaged(scanner(t, scan.Options{MaxFileBytes: 2000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Kind != "size" || res.Findings[0].Path != "big.txt" {
		t.Fatalf("only the oversize file should be reported (deleted, binary and symlink are fine): %+v", res.Findings)
	}
}

func TestAllowListComesFromHEADNotFromTheCommitItself(t *testing.T) {
	r := newRepo(t)
	r.write("README.md", "x\n")
	r.commit("init")
	sc := scanner(t, scan.Options{})
	r.write("docs/example.md", "token = \""+fakeGH+"\"\n")
	r.git("add", "-A")
	first, _ := r.g.ScanStaged(sc)
	if len(first.Findings) != 1 {
		t.Fatal(first.Findings)
	}
	// the same commit tries to allow-list its own secret: must NOT work, and the user is told why
	r.write(AllowFile, "fp:"+first.Findings[0].Fingerprint+"\n")
	r.git("add", "-A")
	res, _ := r.g.ScanStaged(sc)
	if len(res.Findings) != 1 || !strings.Contains(strings.Join(res.Notes, " "), "NEXT commit") {
		t.Fatalf("a commit must not allow-list its own secret: %+v %v", res.Findings, res.Notes)
	}
	// once the allow list is committed on its own, the next commit passes
	r.git("reset", "-q", "docs/example.md")
	r.git("commit", "-q", "--no-verify", "-m", "allow list")
	r.git("add", "-A")
	if res, _ := r.g.ScanStaged(sc); len(res.Findings) != 0 {
		t.Fatalf("committed allow-list entry should apply: %+v", res.Findings)
	}
}

func TestFirstCommitUsesTheIndexAllowList(t *testing.T) {
	r := newRepo(t)
	sc := scanner(t, scan.Options{})
	r.write("a.md", "token = \""+fakeGH+"\"\n")
	r.git("add", "-A")
	if f, _ := r.g.ScanStaged(sc); len(f.Findings) != 1 {
		t.Fatalf("%+v", f.Findings)
	}
	// there is no HEAD yet, so the staged allow list is the only one that can exist
	r.write(AllowFile, "a.md\n")
	r.git("add", "-A")
	if res, _ := r.g.ScanStaged(sc); len(res.Findings) != 0 {
		t.Fatalf("%+v", res.Findings)
	}
}

func TestPrePushScansOnlyTheNewCommits(t *testing.T) {
	r := newRepo(t)
	sc := scanner(t, scan.Options{})
	bare := t.TempDir()
	c := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	r.git("remote", "add", "origin", bare)

	r.write("README.md", "hello\n")
	base := r.commit("clean base")
	r.git("push", "-q", "origin", "main")

	// commits made with --no-verify, one with a secret, then a merge from a side branch
	r.write("ok.txt", "fine\n")
	r.commit("ok")
	r.write("leak.py", "token = \""+fakeGH+"\"\n")
	bad := r.commit("oops, no hooks")
	r.git("checkout", "-q", "-b", "side", base)
	r.write("side.txt", "side\n")
	r.commit("side")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--no-verify", "--no-edit", "side")
	tip := r.git("rev-parse", "HEAD")

	var stderr bytes.Buffer
	stdin := "refs/heads/main " + tip + " refs/heads/main " + base + "\n"
	if code := PrePush(r.g, Fixed(sc), "origin", strings.NewReader(stdin), &stderr); code != 1 || !strings.Contains(stderr.String(), "leak.py:1") || !strings.Contains(stderr.String(), bad[:8]) {
		t.Fatalf("code %d\n%s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "wJ4kP9xQm2Rt7V") {
		t.Fatal("value leaked")
	}
	// a new branch (remote oid zero): only commits the remote does not have
	stderr.Reset()
	stdin = "refs/heads/feature " + tip + " refs/heads/feature " + ZeroOID + "\n"
	if code := PrePush(r.g, Fixed(sc), "origin", strings.NewReader(stdin), &stderr); code != 1 {
		t.Fatalf("new branch must be scanned too: %d\n%s", code, stderr.String())
	}
	// once the leaking commit is already on the remote, pushing on top of it is clean
	r.git("push", "-q", "--no-verify", "origin", "main")
	r.write("more.txt", "more\n")
	next := r.commit("more")
	stderr.Reset()
	stdin = "refs/heads/main " + next + " refs/heads/main " + tip + "\n"
	if code := PrePush(r.g, Fixed(sc), "origin", strings.NewReader(stdin), &stderr); code != 0 {
		t.Fatalf("only new commits count: %d\n%s", code, stderr.String())
	}
	// deleting a ref scans nothing
	stdin = "(delete) " + ZeroOID + " refs/heads/old " + tip + "\n"
	if code := PrePush(r.g, Fixed(sc), "origin", strings.NewReader(stdin), &stderr); code != 0 {
		t.Fatalf("delete: %d", code)
	}
}

func TestRootCommitIsScanned(t *testing.T) {
	r := newRepo(t)
	r.write("keys.py", "token = \""+fakeGH+"\"\n")
	root := r.commit("root with a secret")
	res, err := r.g.ScanCommits(scanner(t, scan.Options{}), []string{root}, "")
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Commit != root[:8] {
		t.Fatalf("%+v %v", res.Findings, err)
	}
}

func TestReferenceTransactionBackstopAndTheScannedTreeCache(t *testing.T) {
	r := newRepo(t)
	sc := scanner(t, scan.Options{})
	r.write("README.md", "hello\n")
	base := r.commit("base")

	// a commit made with --no-verify that contains a secret: the backstop catches it in the "prepared" state
	r.write("leak.py", "token = \""+fakeGH+"\"\n")
	bad := r.commit("sneaky")
	line := base + " " + bad + " refs/heads/main\n"
	var stderr bytes.Buffer
	if code := ReferenceTransaction(r.g, Fixed(sc), "prepared", strings.NewReader(line), &stderr); code != 1 || !strings.Contains(stderr.String(), "ref update blocked") {
		t.Fatalf("code %d\n%s", code, stderr.String())
	}
	// other states, remote-tracking refs, deletions and unchanged refs are ignored
	for name, in := range map[string]string{
		"committed": line, "aborted": line,
	} {
		if code := ReferenceTransaction(r.g, Fixed(sc), name, strings.NewReader(in), &stderr); code != 0 {
			t.Fatalf("state %s must not block", name)
		}
	}
	for _, in := range []string{
		base + " " + bad + " refs/remotes/origin/main\n", bad + " " + ZeroOID + " refs/heads/main\n", bad + " " + bad + " refs/heads/main\n",
		base + " " + bad + " refs/stash\n",
	} {
		if code := ReferenceTransaction(r.g, Fixed(sc), "prepared", strings.NewReader(in), &stderr); code != 0 {
			t.Fatalf("%q must be ignored", in)
		}
	}
	// a clean commit whose tree pre-commit approved is not scanned again
	r.git("reset", "-q", "--hard", base)
	r.write("clean.txt", "clean\n")
	r.git("add", "-A")
	if code := PreCommit(r.g, Fixed(sc), &stderr); code != 0 {
		t.Fatalf("clean staged changes: %d %s", code, stderr.String())
	}
	clean := func() string { r.git("commit", "-q", "--no-verify", "-m", "clean"); return r.git("rev-parse", "HEAD") }()
	if !r.g.wasScanned(r.git("rev-parse", clean+"^{tree}")) {
		t.Fatal("pre-commit should record the approved tree")
	}
	if code := ReferenceTransaction(r.g, Fixed(sc), "prepared", strings.NewReader(base+" "+clean+" refs/heads/main\n"), &stderr); code != 0 {
		t.Fatal("clean commit must pass")
	}
}

func TestPreCommitFailsClosedOutsideARepoAndReferenceTransactionFailsOpen(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir() // not a repository
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	g := Git{Dir: dir}
	sc := scanner(t, scan.Options{})
	var stderr bytes.Buffer
	if code := PreCommit(g, Fixed(sc), &stderr); code != 1 || !strings.Contains(stderr.String(), "could not scan") {
		t.Fatalf("pre-commit must fail closed: %d %s", code, stderr.String())
	}
	stderr.Reset()
	line := strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " refs/heads/main\n"
	if code := ReferenceTransaction(g, Fixed(sc), "prepared", strings.NewReader(line), &stderr); code != 0 {
		t.Fatalf("reference-transaction must fail open on internal errors: %d", code)
	}
}

func TestBackstopLatency(t *testing.T) {
	r := newRepo(t)
	sc := scanner(t, scan.Options{})
	r.write("README.md", "hello\n")
	prev := r.commit("base")
	var total time.Duration
	const n = 10
	for i := 0; i < n; i++ {
		r.write("f.txt", strings.Repeat("line\n", i+1))
		r.git("add", "-A")
		var sink bytes.Buffer
		if PreCommit(r.g, Fixed(sc), &sink) != 0 {
			t.Fatal(sink.String())
		}
		r.git("commit", "-q", "--no-verify", "-m", "c")
		cur := r.git("rev-parse", "HEAD")
		start := time.Now()
		ReferenceTransaction(r.g, Fixed(sc), "prepared", strings.NewReader(prev+" "+cur+" refs/heads/main\n"), &sink)
		total += time.Since(start)
		prev = cur
	}
	t.Logf("reference-transaction backstop (tree already approved by pre-commit): %v per update, in-process", total/n)
}
