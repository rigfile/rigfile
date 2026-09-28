package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var fpRe = regexp.MustCompile(`[0-9a-f]{4}(-[0-9a-f]{4}){4}`)

func syncMachine(t *testing.T) *machine {
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(pass, []byte("correct horse battery staple\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	return m
}

func lastFP(t *testing.T, out, after string) string {
	i := strings.LastIndex(out, after)
	if i < 0 {
		t.Fatalf("no %q in\n%s", after, out)
	}
	return fpRe.FindString(out[i:])
}

// enrol makes two machines share a vault in dir through the real init / join / approve / finish commands.
func enrol(t *testing.T, a, b *machine, dirA, dirB string, extra ...string) {
	t.Helper()
	if r := a.run("", append([]string{"sync", "init", dirA, "--name", "laptop"}, extra...)...); r.code != 0 || !strings.Contains(r.out, "Vault fingerprint") {
		t.Fatalf("%+v", r)
	}
	r := b.run("", append([]string{"sync", "join", dirB, "--name", "desktop"}, extra...)...)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	dfp := lastFP(t, r.out, "device's fingerprint")
	// a wrong fingerprint is refused
	if r := a.run("", "sync", "approve", "desktop", "--fingerprint", "0000-0000-0000-0000-0000"); r.code != 1 || !strings.Contains(r.err, "do NOT approve") {
		t.Fatalf("%+v", r)
	}
	r = a.run("", "sync", "approve", "desktop", "--fingerprint", dfp)
	if r.code != 0 || !strings.Contains(r.out, "Enrolled desktop") {
		t.Fatalf("%+v", r)
	}
	vfp := lastFP(t, r.out, "--fingerprint")
	if r := b.run("", "sync", "finish", "--fingerprint", "1111-1111-1111-1111-1111"); r.code != 1 || !strings.Contains(r.err, "not the vault you approved") {
		t.Fatalf("%+v", r)
	}
	if r := b.run("", "sync", "finish", "--fingerprint", vfp); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSyncTwoMachinesThroughTheCommands(t *testing.T) {
	a, b := syncMachine(t), syncMachine(t)
	vdir := filepath.Join(t.TempDir(), "vault")
	enrol(t, a, b, vdir, vdir)
	claudeA := filepath.Join(a.home, ".claude", "CLAUDE.md")
	claudeB := filepath.Join(b.home, ".claude", "CLAUDE.md")
	put(t, filepath.Join(a.home, ".claude"), "CLAUDE.md", "# mine\nbe terse\n", 0o644)

	if r := a.run("", "sync", "track", claudeA); r.code != 0 || !strings.Contains(r.out, "as home/.claude/CLAUDE.md") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "sync", "status"); !strings.Contains(r.out, "push") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "sync", "push"); r.code != 0 || !strings.Contains(r.out, "pushed") {
		t.Fatalf("%+v", r)
	}
	// the other machine sees it as available, tracks its own path, pulls
	if r := b.run("", "sync", "status"); !strings.Contains(r.out, "available  home/.claude/CLAUDE.md") {
		t.Fatalf("%+v", r)
	}
	if r := b.run("", "sync", "track", claudeB); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := b.run("", "sync", "pull")
	if r.code != 0 || !strings.Contains(r.out, "pulled") || string(mustRead(t, claudeB)) != "# mine\nbe terse\n" {
		t.Fatalf("%+v", r)
	}
	// both edit: exit code 3 and the incoming version is kept apart
	put(t, filepath.Join(a.home, ".claude"), "CLAUDE.md", "laptop\n", 0o644)
	put(t, filepath.Join(b.home, ".claude"), "CLAUDE.md", "desktop\n", 0o644)
	if r := b.run("", "sync", "push"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r = a.run("", "sync", "pull")
	if r.code != 3 || !strings.Contains(r.out, "conflict") || string(mustRead(t, claudeA)) != "laptop\n" {
		t.Fatalf("%+v", r)
	}
	matches, _ := filepath.Glob(claudeA + ".conflict-desktop-*")
	if len(matches) != 1 || string(mustRead(t, matches[0])) != "desktop\n" {
		t.Fatalf("%v", matches)
	}
	// devices and revocation
	if r := a.run("", "sync", "devices"); !strings.Contains(r.out, "laptop") || !strings.Contains(r.out, "desktop") || !strings.Contains(r.out, "(this device)") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "sync", "revoke", "desktop"); r.code != 0 || !strings.Contains(r.out, "treat those contents as exposed") {
		t.Fatalf("%+v", r)
	}
	if r := b.run("", "sync", "status"); r.code != 1 || !strings.Contains(r.err, "not in the vault") {
		t.Fatalf("a revoked device must be shut out: %+v", r)
	}
}

func TestSyncScansTracksSafelyAndRefusesCredentialPaths(t *testing.T) {
	a, b := syncMachine(t), syncMachine(t)
	vdir := filepath.Join(t.TempDir(), "vault")
	enrol(t, a, b, vdir, vdir)
	fake := "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"
	put(t, a.home, "notes.md", "my token is "+fake+"\n", 0o644)
	if r := a.run("", "sync", "track", filepath.Join(a.home, "notes.md")); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := a.run("", "sync", "push")
	if r.code != 3 || !strings.Contains(r.out, "refused") || !strings.Contains(r.out, "--allow-secrets home/notes.md") || strings.Contains(r.out+r.err, fake) {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "sync", "push", "--allow-secrets", "home/notes.md"); r.code != 0 || !strings.Contains(r.out, "pushed") {
		t.Fatalf("%+v", r)
	}
	// credential locations and outside-home paths can never be tracked
	for _, p := range []string{filepath.Join(a.home, ".ssh", "id_ed25519"), filepath.Join(a.home, ".aws", "credentials"), filepath.Join(a.home, ".rigfile", "secrets.age"), filepath.Join(a.home, ".env")} {
		if r := a.run("", "sync", "track", p); r.code != 1 || !strings.Contains(r.err, "never synced") {
			t.Fatalf("%s: %+v", p, r)
		}
	}
	if r := a.run("", "sync", "track", filepath.Join(t.TempDir(), "outside.md")); r.code != 1 || !strings.Contains(r.err, "outside your home") {
		t.Fatalf("%+v", r)
	}
	// a rig's private: paths, and only those
	rig := plainRig(t, "private:\n  - memory/\n")
	put(t, rig, "memory/me.md", "personal\n", 0o644)
	put(t, rig, "instructions/other.md", "public\n", 0o644)
	if r := a.run("", "sync", "track", filepath.Join(rig, "memory", "me.md"), "--rig", rig); r.code != 0 || !strings.Contains(r.out, "as rig/adams/plain/memory/me.md") {
		t.Fatalf("%+v", r)
	}
	if r := a.run("", "sync", "track", filepath.Join(rig, "instructions", "other.md"), "--rig", rig); r.code != 1 || !strings.Contains(r.err, "not listed under `private:`") {
		t.Fatalf("%+v", r)
	}
	// the other device cannot be made to write somewhere by a name in the vault: it writes only where IT tracked
	if r := b.run("", "sync", "pull"); r.code != 0 || !strings.Contains(r.out, "nothing tracked") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(b.home, "notes.md")); err == nil {
		t.Fatal("an untracked name must never create a file")
	}
}

func TestSyncOverAGitRepositoryYouOwn(t *testing.T) {
	genv := gitTestEnv(t)
	for _, kv := range genv {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	gitIn(t, root, genv, "init", "-q", "--bare", "-b", "main", bare)
	dirA, dirB := filepath.Join(root, "a"), filepath.Join(root, "b")
	gitIn(t, root, genv, "clone", "-q", bare, dirA)
	a, b := syncMachine(t), syncMachine(t)
	if r := a.run("", "sync", "init", dirA, "--name", "laptop", "--git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	gitIn(t, root, genv, "clone", "-q", bare, dirB)
	r := b.run("", "sync", "join", dirB, "--name", "desktop", "--git")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	dfp := lastFP(t, r.out, "device's fingerprint")
	r = a.run("", "sync", "approve", "desktop", "--fingerprint", dfp)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	vfp := lastFP(t, r.out, "--fingerprint")
	if r := b.run("", "sync", "finish", "--fingerprint", vfp, "--git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	put(t, a.home, "n.md", "private\n", 0o644)
	a.run("", "sync", "track", filepath.Join(a.home, "n.md"))
	if r := a.run("", "sync", "push"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	b.run("", "sync", "track", filepath.Join(b.home, "n.md"))
	if r := b.run("", "sync", "pull"); r.code != 0 || string(mustRead(t, filepath.Join(b.home, "n.md"))) != "private\n" {
		t.Fatalf("the git remote carries the vault: %+v", r)
	}
	// what is in git is ciphertext
	log := gitIn(t, root, genv, "-C", bare, "log", "--all", "--oneline")
	if !strings.Contains(log, "rigfile sync") {
		t.Fatalf("%s", log)
	}
	tree := gitIn(t, root, genv, "-C", bare, "ls-tree", "-r", "--name-only", "main")
	if strings.Contains(tree, "n.md") || !strings.Contains(tree, "index.age") {
		t.Fatalf("names must not appear in the repository:\n%s", tree)
	}
}
