package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/githook"
)

// TestMain lets the test binary double as the `rigfile` executable: the git hook shims below call it with
// RIGFILE_TEST_AS_CLI=1, so these tests exercise real git invoking real hooks end to end.
func TestMain(m *testing.M) {
	if os.Getenv("RIGFILE_TEST_AS_CLI") == "1" {
		os.Exit(run(os.Args[1:], env{in: os.Stdin, out: os.Stdout, err: os.Stderr, getenv: os.Getenv}))
	}
	os.Exit(m.Run())
}

var e2eSecret = "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"

type gitEnv struct {
	t     *testing.T
	dir   string
	hooks string
}

func newGitEnv(t *testing.T) *gitEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("git hook shims are sh scripts; Windows is Stage 3")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	g := &gitEnv{t: t, dir: t.TempDir(), hooks: t.TempDir()}
	g.mustGit("init", "-q", "-b", "main")
	for _, kv := range [][2]string{{"user.name", "T"}, {"user.email", "t@example.test"}, {"commit.gpgsign", "false"}, {"core.hooksPath", g.hooks}} {
		g.mustGit("config", kv[0], kv[1])
	}
	return g
}

func (g *gitEnv) install(names ...string) {
	self, _ := os.Executable()
	for _, n := range names {
		// the test binary doubles as `rigfile`: a wrapper script sets the mode variable, then the real shim runs it
		wrapper := filepath.Join(g.hooks, ".rigfile-as-cli")
		if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nRIGFILE_TEST_AS_CLI=1 exec '"+self+"' \"$@\"\n"), 0o755); err != nil {
			g.t.Fatal(err)
		}
		sh := githook.ShimScript(n, wrapper)
		if err := os.WriteFile(filepath.Join(g.hooks, n), []byte(sh), 0o755); err != nil {
			g.t.Fatal(err)
		}
	}
}

func (g *gitEnv) git(args ...string) (string, string, error) {
	c := exec.Command("git", args...)
	c.Dir = g.dir
	var o, e bytes.Buffer
	c.Stdout, c.Stderr = &o, &e
	err := c.Run()
	return strings.TrimSpace(o.String()), e.String(), err
}

func (g *gitEnv) mustGit(args ...string) string {
	g.t.Helper()
	o, e, err := g.git(args...)
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, e)
	}
	return o
}

func (g *gitEnv) write(rel, content string) {
	g.t.Helper()
	p := filepath.Join(g.dir, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gitEnv) head() string { o, _, _ := g.git("rev-parse", "-q", "--verify", "HEAD"); return o }

func TestGitHooksEndToEnd(t *testing.T) {
	g := newGitEnv(t)
	g.install("pre-commit", "pre-push")

	g.write("README.md", "hello\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "clean")
	base := g.head()

	// 1. a secret is blocked at commit, with a clear message that never shows the value
	g.write("app.py", "token = \""+e2eSecret+"\"\n")
	g.mustGit("add", "-A")
	_, stderr, err := g.git("commit", "-q", "-m", "leak")
	if err == nil || !strings.Contains(stderr, "commit blocked") || !strings.Contains(stderr, "app.py:1") {
		t.Fatalf("commit must be blocked: err=%v\n%s", err, stderr)
	}
	if strings.Contains(stderr, e2eSecret) || strings.Contains(stderr, "wJ4kP9xQm2Rt7V") {
		t.Fatal("hook output contains the secret")
	}
	if g.head() != base {
		t.Fatal("blocked commit moved HEAD")
	}

	// 2. a credential file name is blocked even when its content is harmless; .env.example is fine
	g.mustGit("reset", "-q")
	os.Remove(filepath.Join(g.dir, "app.py"))
	g.write(".env", "A=1\n")
	g.write(".env.example", "A=\n")
	g.mustGit("add", "-f", ".env", ".env.example")
	if _, stderr, err := g.git("commit", "-q", "-m", "env"); err == nil || !strings.Contains(stderr, ".env\n") && !strings.Contains(stderr, "  .env") {
		t.Fatalf("%v\n%s", err, stderr)
	}
	g.mustGit("reset", "-q")
	os.Remove(filepath.Join(g.dir, ".env"))
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "example only") // .env.example alone passes

	// 3. WITHOUT the reference-transaction backstop, --no-verify still commits a secret (the documented gap) ...
	g.write("leak.py", "token = \""+e2eSecret+"\"\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "--no-verify", "-m", "sneaky")
	leaky := g.head()
	// ... and pre-push catches it when the pusher does NOT also pass --no-verify
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	g.mustGit("remote", "add", "origin", bare)
	if _, stderr, err := g.git("push", "-q", "origin", "main"); err == nil || !strings.Contains(stderr, "push blocked") || !strings.Contains(stderr, "leak.py:1") {
		t.Fatalf("pre-push must block: %v\n%s", err, stderr)
	}
	// git push --no-verify skips pre-push entirely (git-push docs): the documented gap
	if _, _, err := g.git("push", "-q", "--no-verify", "origin", "main"); err != nil {
		t.Fatalf("expected --no-verify push to succeed (documented gap): %v", err)
	}

	// 4. WITH the backstop, `git commit --no-verify` cannot create the leaking commit at all
	g.mustGit("reset", "-q", "--hard", base)
	g.install("reference-transaction")
	g.write("leak2.py", "token = \""+e2eSecret+"\"\n")
	g.mustGit("add", "-A")
	before := g.head()
	if _, stderr, err := g.git("commit", "-q", "--no-verify", "-m", "sneaky again"); err == nil || !strings.Contains(stderr, "ref update blocked") || !strings.Contains(stderr, "leak2.py:1") {
		t.Fatalf("backstop must block --no-verify: %v\n%s", err, stderr)
	}
	if g.head() != before {
		t.Fatal("the branch moved although the backstop blocked the update")
	}
	_ = leaky

	// 5. the backstop does not get in the way of ordinary git: clean commits, branches, merges, rebases, tags, fetch
	g.mustGit("reset", "-q", "--hard")
	g.mustGit("clean", "-fdq")
	g.write("a.txt", "a\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "a")
	g.mustGit("checkout", "-q", "-b", "feature")
	g.write("b.txt", "b\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "b")
	g.mustGit("checkout", "-q", "main")
	g.write("c.txt", "c\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "c")
	g.mustGit("checkout", "-q", "feature")
	g.mustGit("rebase", "-q", "main")
	g.mustGit("checkout", "-q", "main")
	g.mustGit("merge", "-q", "--no-edit", "feature")
	g.mustGit("tag", "v1")
	g.mustGit("tag", "-a", "-m", "annotated", "v2")
	g.mustGit("branch", "-D", "feature")
	g.mustGit("fetch", "-q", "origin")
}

func TestBackstopLatencyEndToEnd(t *testing.T) {
	g := newGitEnv(t)
	measure := func(label string) time.Duration {
		var total time.Duration
		const n = 8
		for i := 0; i < n; i++ {
			g.write("f.txt", strings.Repeat("line\n", i+1)+label)
			g.mustGit("add", "-A")
			start := time.Now()
			g.mustGit("commit", "-q", "-m", label)
			total += time.Since(start)
		}
		return total / n
	}
	g.write("README.md", "x\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "base")
	none := measure("no-hooks")
	g.install("pre-commit")
	pre := measure("pre-commit")
	g.install("reference-transaction")
	both := measure("pre-commit+ref-tx")
	t.Logf("git commit: no hooks %v, pre-commit %v (+%v), plus reference-transaction %v (+%v more)", none, pre, pre-none, both, both-pre)
}

// TestHooksPathSilencesRepoLocalHooks confirms the git behaviour the chaining design (S2-M4) depends on:
// with core.hooksPath set, hooks in .git/hooks no longer run (the docs only imply this).
func TestHooksPathSilencesRepoLocalHooks(t *testing.T) {
	g := newGitEnv(t)
	marker := filepath.Join(t.TempDir(), "local-hook-ran")
	local := filepath.Join(g.dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(local, []byte("#!/bin/sh\necho ran > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.write("a.txt", "a\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "with hooksPath set")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repo-local hook ran although core.hooksPath is set")
	}
	g.mustGit("config", "--unset", "core.hooksPath")
	g.write("b.txt", "b\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "-m", "without hooksPath")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the repo-local hook should run once core.hooksPath is unset")
	}
}
