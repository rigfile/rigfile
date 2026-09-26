package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const publishRigYAML = `apiVersion: rigfile.dev/v1
name: jiaxu/shared
version: 1.0.0
description: Shared rig
instructions:
  - {id: style, file: instructions/style.md}
skills:
  - {path: skills/pdf}
`

func publishRig(t *testing.T) string {
	d := t.TempDir()
	put(t, d, "rigfile.yaml", publishRigYAML, 0o644)
	put(t, d, "instructions/style.md", "# Style\n- be terse\n", 0o644)
	put(t, d, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: PDFs\n---\nbody\n", 0o644)
	put(t, d, "notes/diary.md", "not part of the rig", 0o644)
	return d
}

func TestPublishFromARigDirectoryWritesACleanRepoAndItCanBePulledBack(t *testing.T) {
	env := gitTestEnv(t)
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	m := pullMachine(t, env)
	out := filepath.Join(t.TempDir(), "repo")
	r := m.run("", "publish", publishRig(t), "--to-git", out, "--git-init")
	if r.code != 0 || !strings.Contains(r.out, "scan proof: 0 finding(s)") || !strings.Contains(r.out, "rigfile pull github.com/jiaxu/shared") {
		t.Fatalf("%+v", r)
	}
	for _, f := range []string{"rigfile.yaml", "README.md", ".gitignore", "skills/pdf/SKILL.md", "instructions/style.md", ".git"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "notes")); !os.IsNotExist(err) {
		t.Fatal("an unreferenced directory was published")
	}
	// the published repository is a valid source for `pull`
	p := filepath.ToSlash(out)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	pull := pullMachine(t, env)
	if r := pull.run("", "pull", "file://"+p, "--yes", "--no-git"); r.code != 0 || !strings.Contains(r.out, "applied") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(pull.home, ".claude", "skills", "pdf", "SKILL.md")); err != nil {
		t.Fatal("the pulled rig was not applied")
	}
}

func TestPublishBlocksASecretAndWritesNothing(t *testing.T) {
	m := newMachine(t)
	rig := publishRig(t)
	tok := "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"
	put(t, rig, "instructions/style.md", "# Style\ntoken = \""+tok+"\"\n", 0o644)
	out := filepath.Join(t.TempDir(), "repo")
	r := m.run("", "publish", rig, "--to-git", out)
	if r.code != 1 || !strings.Contains(r.out, "SECRET") || !strings.Contains(r.err, "no override") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.out+r.err, tok) {
		t.Fatal("the secret value was printed")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("nothing may be written")
	}
}

func TestPublishCaptureUsesTheChecklistAndPersonalInfoNeedsAck(t *testing.T) {
	m := newMachine(t)
	m.tty = true
	cd := filepath.Join(m.home, ".claude")
	put(t, cd, "CLAUDE.md", "# Me\nContact jia.x@gmail.com\n", 0o644)
	put(t, cd, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: PDFs\n---\nbody\n", 0o644)
	put(t, cd, "agents/reviewer.md", "---\nname: reviewer\ndescription: r\n---\nx\n", 0o644)

	// scripted keys: the checklist starts with CLAUDE.md unticked, so plain Enter leaves it out
	out := filepath.Join(t.TempDir(), "repo")
	r := m.run("\r", "publish", "--name", "jiaxu/mine", "--to-git", out)
	if r.code != 0 || !strings.Contains(r.out, "Choose what to publish") || !strings.Contains(r.out, "[ ] claude-md") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(out, "instructions")); !os.IsNotExist(err) {
		t.Fatal("the unticked CLAUDE.md was published")
	}
	if _, err := os.Stat(filepath.Join(out, "skills", "pdf", "SKILL.md")); err != nil {
		t.Fatal("the ticked skill was not published")
	}

	// --all includes CLAUDE.md: its e-mail address blocks until acknowledged
	out2 := filepath.Join(t.TempDir(), "repo2")
	r = m.run("", "publish", "--name", "jiaxu/mine", "--to-git", out2, "--all")
	if r.code != 1 || !strings.Contains(r.out, "PERSONAL") || !strings.Contains(r.err, "--ack-personal") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.out, "gmail.com") {
		t.Fatal("the e-mail address was printed in full")
	}
	if r = m.run("", "publish", "--name", "jiaxu/mine", "--to-git", out2, "--all", "--ack-personal"); r.code != 0 {
		t.Fatalf("%+v", r)
	}

	// cancelling writes nothing
	out3 := filepath.Join(t.TempDir(), "repo3")
	if r = m.run("q", "publish", "--name", "jiaxu/mine", "--to-git", out3); r.code != 1 || !strings.Contains(r.out, "cancelled") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(out3); !os.IsNotExist(err) {
		t.Fatal("cancel wrote something")
	}
	// without a terminal the checklist cannot run
	m.tty = false
	if r = m.run("", "publish", "--name", "jiaxu/mine", "--to-git", filepath.Join(t.TempDir(), "x")); r.code != 1 || !strings.Contains(r.err, "--all") {
		t.Fatalf("%+v", r)
	}
}

func TestPublishRefusesAfterAnUnsafeBaseApply(t *testing.T) {
	m := newMachine(t)
	rig := plainRig(t, "")
	if r := m.run("", "apply", rig, "--yes", "--no-git", "--i-understand-unsafe-base"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := m.run("", "publish", publishRig(t), "--to-git", filepath.Join(t.TempDir(), "repo"))
	if r.code != 1 || !strings.Contains(r.err, "base-secure was skipped") {
		t.Fatalf("%+v", r)
	}
}
