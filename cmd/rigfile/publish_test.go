package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const publishRigYAML = `apiVersion: rigfile.dev/v1
name: adams/shared
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

// sourceDirFromArgv finds gh's --source=<dir> in a captured argv (the scratch directory publishToGitHub built,
// still on disk at the moment runCmd is called: cleanup happens after it returns).
func sourceDirFromArgv(argv []string) string {
	for _, a := range argv {
		if d, ok := strings.CutPrefix(a, "--source="); ok {
			return d
		}
	}
	return ""
}

func TestPublishToGitHubCreatesAndPushesAScrubbedRepo(t *testing.T) {
	m := newMachine(t)
	var argv []string
	var sourceHadFiles, sourceHadNotes bool
	var out, errb bytes.Buffer
	code := runWith(m, &out, &errb, func(e *env) {
		e.runCmd = func(_ context.Context, a []string) error {
			argv = a
			d := sourceDirFromArgv(a)
			for _, f := range []string{"rigfile.yaml", "README.md", ".gitignore", "skills/pdf/SKILL.md", "instructions/style.md", ".git"} {
				if _, err := os.Stat(filepath.Join(d, filepath.FromSlash(f))); err == nil {
					sourceHadFiles = true
				} else {
					sourceHadFiles = false
					break
				}
			}
			if _, err := os.Stat(filepath.Join(d, "notes")); !os.IsNotExist(err) {
				sourceHadNotes = true
			}
			return nil
		}
	}, "publish", publishRig(t), "--to-github", "acme")
	r := result{code, portable(out.String()), portable(errb.String())}
	if r.code != 0 || !strings.Contains(r.out, "scan proof: 0 finding(s)") || !strings.Contains(r.out, "pushed to https://github.com/acme/shared") || !strings.Contains(r.out, "rigfile pull github.com/acme/shared") {
		t.Fatalf("%+v", r)
	}
	if len(argv) == 0 || argv[0] != "gh" || argv[1] != "repo" || argv[2] != "create" || argv[3] != "acme/shared" || argv[4] != "--private" {
		t.Fatalf("gh argv: %v", argv)
	}
	if !sourceHadFiles {
		t.Fatal("the scratch repo was missing expected files at push time")
	}
	if sourceHadNotes {
		t.Fatal("an unreferenced directory was published")
	}
	if d := sourceDirFromArgv(argv); d != "" {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatal("the scratch directory was not cleaned up")
		}
	}
}

func TestPublishToGitHubPublic(t *testing.T) {
	m := newMachine(t)
	var argv []string
	var out, errb bytes.Buffer
	runWith(m, &out, &errb, func(e *env) {
		e.runCmd = func(_ context.Context, a []string) error { argv = a; return nil }
	}, "publish", publishRig(t), "--to-github", "acme", "--public")
	found := false
	for _, a := range argv {
		if a == "--public" {
			found = true
		}
	}
	if !found {
		t.Fatalf("--public not passed through: %v", argv)
	}
}

func TestPublishToGitHubRejectsOwnerSlashName(t *testing.T) {
	m := newMachine(t)
	r := m.run("", "publish", publishRig(t), "--to-github", "acme/shared")
	if r.code != 2 || !strings.Contains(r.err, "just the owner") {
		t.Fatalf("%+v", r)
	}
}

func TestPublishBlocksASecretAndWritesNothing(t *testing.T) {
	m := newMachine(t)
	rig := publishRig(t)
	tok := "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"
	put(t, rig, "instructions/style.md", "# Style\ntoken = \""+tok+"\"\n", 0o644)
	r := m.run("", "publish", rig, "--to-github", "acme")
	if r.code != 1 || !strings.Contains(r.out, "SECRET") || !strings.Contains(r.err, "no override") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.out+r.err, tok) {
		t.Fatal("the secret value was printed")
	}
	if len(m.ran) != 0 {
		t.Fatal("nothing may be run when publishing is blocked")
	}
}

func TestPublishCaptureUsesTheChecklistAndPersonalInfoNeedsAck(t *testing.T) {
	m := newMachine(t)
	m.tty = true
	cd := filepath.Join(m.home, ".claude")
	put(t, cd, "CLAUDE.md", "# Me\nContact ada.x@bytebuilderslab.app\n", 0o644)
	put(t, cd, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: PDFs\n---\nbody\n", 0o644)
	put(t, cd, "agents/reviewer.md", "---\nname: reviewer\ndescription: r\n---\nx\n", 0o644)

	// scripted keys: the checklist starts with CLAUDE.md unticked, so plain Enter leaves it out
	r := m.run("\r", "publish", "--name", "adams/mine", "--to-github", "acme")
	if r.code != 0 || !strings.Contains(r.out, "Choose what to publish") || !strings.Contains(r.out, "[ ] claude-md") {
		t.Fatalf("%+v", r)
	}

	// --all includes CLAUDE.md: its e-mail address blocks until acknowledged
	r = m.run("", "publish", "--name", "adams/mine", "--to-github", "acme", "--all")
	if r.code != 1 || !strings.Contains(r.out, "PERSONAL") || !strings.Contains(r.err, "--ack-personal") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.out, "gmail.com") {
		t.Fatal("the e-mail address was printed in full")
	}
	if r = m.run("", "publish", "--name", "adams/mine", "--to-github", "acme", "--all", "--ack-personal"); r.code != 0 {
		t.Fatalf("%+v", r)
	}

	// cancelling writes nothing and runs nothing
	m.ran = nil
	if r = m.run("q", "publish", "--name", "adams/mine", "--to-github", "acme"); r.code != 1 || !strings.Contains(r.out, "cancelled") {
		t.Fatalf("%+v", r)
	}
	if len(m.ran) != 0 {
		t.Fatal("cancel ran something")
	}
	// without a terminal the checklist cannot run
	m.tty = false
	if r = m.run("", "publish", "--name", "adams/mine", "--to-github", "acme"); r.code != 1 || !strings.Contains(r.err, "--all") {
		t.Fatalf("%+v", r)
	}
}

func TestPublishRefusesAfterAnUnsafeBaseApply(t *testing.T) {
	m := newMachine(t)
	rig := plainRig(t, "")
	if r := m.run("", "apply", rig, "--yes", "--no-git", "--i-understand-unsafe-base"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := m.run("", "publish", publishRig(t), "--to-github", "acme")
	if r.code != 1 || !strings.Contains(r.err, "base-secure was skipped") {
		t.Fatalf("%+v", r)
	}
}
