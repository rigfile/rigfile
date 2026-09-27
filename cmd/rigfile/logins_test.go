package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/platform"
)

func TestLoginsWalksThroughWhatTheAppliedRigNeeds(t *testing.T) {
	m := newMachine(t)
	m.key = "fake-api-key-value"
	pass := filepath.Join(t.TempDir(), "pass")
	if err := platform.WritePrivate(pass, []byte("correct horse battery staple\n")); err != nil {
		t.Fatal(err)
	}
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	rig := plainRig(t, "logins:\n  - {provider: github, reason: push and open pull requests}\n  - {provider: acme, method: api-key, reason: the acme service}\n")
	r := m.run("", "apply", rig, "--yes", "--no-git")
	if r.code != 0 || !strings.Contains(r.out, "LOGIN NEEDED   github") || !strings.Contains(r.out, "rigfile logins") {
		t.Fatalf("%+v", r)
	}
	r = m.run("y\n", "logins")
	if r.code != 0 || !strings.Contains(r.out, "[1/2] github") || !strings.Contains(r.out, "[2/2] acme") {
		t.Fatalf("%+v", r)
	}
	var ran []string
	for _, a := range m.ran {
		ran = append(ran, strings.Join(a, " "))
	}
	if strings.Join(ran, "|") != "gh auth login|gh auth status" {
		t.Fatalf("vendor commands: %v", ran)
	}
	if strings.Contains(r.out+r.err, m.key) {
		t.Fatal("the key was printed")
	}
	// the key is in the secret store, so a second run does not ask again
	m.key = "must-not-be-used"
	m.ran = nil
	if r = m.run("", "logins", "--provider", "acme"); r.code != 0 || !strings.Contains(r.out, "already stored") || len(m.ran) != 0 {
		t.Fatalf("%+v", r)
	}
	if r = m.run("", "logins", "--method", "oauth"); r.code != 2 {
		t.Fatalf("--method needs --provider: %+v", r)
	}
}

func TestLoginsWithNothingToDo(t *testing.T) {
	m := newMachine(t)
	if r := m.run("", "apply", plainRig(t, ""), "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "logins"); r.code != 0 || !strings.Contains(r.out, "needs no logins") {
		t.Fatalf("%+v", r)
	}
}
