package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/source"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

func gitTestEnv(t *testing.T) []string {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	return []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test"}
}

func gitIn(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false"}, args...)...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRig turns a rig directory into a local git repository and returns its file:// source.
func gitRig(t *testing.T, rig string, env []string) string {
	gitIn(t, rig, env, "init", "-q", "-b", "main")
	gitIn(t, rig, env, "config", "uploadpack.allowAnySHA1InWant", "true")
	gitIn(t, rig, env, "add", "-A")
	gitIn(t, rig, env, "commit", "-q", "-m", "one")
	p := filepath.ToSlash(rig)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func pullMachine(t *testing.T, env []string) *machine {
	m := newMachine(t)
	m.src = &source.Client{CacheDir: filepath.Join(t.TempDir(), "sources"), Git: &source.GitFetcher{Env: env}}
	return m
}

func TestPullFetchesShowsTheSourceAndAppliesOnApproval(t *testing.T) {
	env := gitTestEnv(t)
	m := pullMachine(t, env)
	url := gitRig(t, plainRig(t, ""), env)

	// plan-only: the screen names the source and its pin, and nothing is written
	r := m.run("", "pull", url, "--plan-only", "--no-git")
	for _, want := range []string{"Source: " + url, "commit ", "tree ", "you did not write", "Nothing has run yet", "Rig: jiaxu/plain"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("missing %q:\n%+v", want, r)
		}
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("plan-only wrote to the machine")
	}

	// apply: recorded in state.json with the resolved commit and content hash
	if r := m.run("", "pull", url, "--yes", "--no-git"); r.code != 0 || !strings.Contains(r.out, "applied") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "commands", "hi.md")); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(m.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	rr := st.Target("claude-code").Rig
	if rr.Source != url || len(rr.Commit) != 40 || len(rr.TreeSHA256) != 64 {
		t.Fatalf("%+v", rr)
	}
}

func TestUpdateFollowsTheSourceAndSaysWhenNothingChanged(t *testing.T) {
	env := gitTestEnv(t)
	m := pullMachine(t, env)
	rig := plainRig(t, "")
	url := gitRig(t, rig, env)
	if r := m.run("", "pull", url, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "update", "--yes", "--no-git"); r.code != 0 || !strings.Contains(r.out, "is up to date") {
		t.Fatalf("%+v", r)
	}
	// the author publishes a new version
	put(t, rig, "commands/new.md", "new command", 0o644)
	put(t, rig, "rigfile.yaml", strings.Replace(mustReadStr(t, filepath.Join(rig, "rigfile.yaml")), "commands:\n", "commands:\n  - {path: commands/new.md}\n", 1), 0o644)
	gitIn(t, rig, env, "add", "-A")
	gitIn(t, rig, env, "commit", "-q", "-m", "two")
	r := m.run("", "update", "--plan-only", "--no-git")
	if r.code != 0 || !strings.Contains(r.out, "Update: ") || !strings.Contains(r.out, "new") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "commands", "new.md")); !os.IsNotExist(err) {
		t.Fatal("--plan-only applied the update")
	}
	if r := m.run("", "update", "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "commands", "new.md")); err != nil {
		t.Fatal("the update was not applied")
	}
}

func TestPullRejectsNonSourcesAndUpdateNeedsAPull(t *testing.T) {
	m := newMachine(t)
	if r := m.run("", "pull", "./some/dir"); r.code != 2 || !strings.Contains(r.err, "rigfile apply") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "pull", "http://github.com/o/r"); r.code == 0 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "update"); r.code != 1 || !strings.Contains(r.err, "nothing was pulled") {
		t.Fatalf("%+v", r)
	}
}

func mustReadStr(t *testing.T, p string) string { return string(mustRead(t, p)) }
