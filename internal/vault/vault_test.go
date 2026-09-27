package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

type plainWriter struct{}

func (plainWriter) Write(p string, b []byte, m os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, m)
}

type rig struct {
	t      *testing.T
	root   string
	tr     DirTransport
	a, b   *Device
	va, vb *Vault
	lsA    *LocalState
	lsB    *LocalState
	home   map[string]string // device -> its home directory
}

func mkDevice(t *testing.T, name string) *Device {
	d, err := NewDevice(name)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// newRig makes a vault with device "laptop" and enrols "desktop" through the real request / fingerprint / approve / finish flow.
func newRig(t *testing.T) *rig {
	r := &rig{t: t, root: t.TempDir(), home: map[string]string{"laptop": t.TempDir(), "desktop": t.TempDir()}}
	r.tr = DirTransport{Root: filepath.Join(r.root, "vault")}
	r.a, r.b = mkDevice(t, "laptop"), mkDevice(t, "desktop")
	var err error
	if r.va, err = Create(r.tr, r.a, t0); err != nil {
		t.Fatal(err)
	}
	r.va.Now = func() time.Time { return t0 }
	r.lsA = &LocalState{Pin: r.va.Pin}
	info, err := RequestJoin(r.tr, r.b, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.va.Approve("desktop", info.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	pin, err := FinishJoin(r.tr, r.b, r.va.Roster().Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if r.vb, err = Open(r.tr, r.b, pin, func() time.Time { return t0 }); err != nil {
		t.Fatal(err)
	}
	r.lsB = &LocalState{Pin: pin}
	return r
}

func (r *rig) item(dev, logical, rel string) Item {
	return Item{Logical: logical, Path: filepath.Join(r.home[dev], rel)}
}

func (r *rig) put(dev, rel, content string) {
	p := filepath.Join(r.home[dev], rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) read(dev, rel string) string {
	b, _ := os.ReadFile(filepath.Join(r.home[dev], rel))
	return string(b)
}

func actions(rs []Result) string {
	var out []string
	for _, x := range rs {
		out = append(out, x.Logical+":"+x.Action)
	}
	return strings.Join(out, ",")
}

func TestTwoDevicesConvergeAndKeepConflictsApart(t *testing.T) {
	r := newRig(t)
	claudeA, claudeB := r.item("laptop", "home/.claude/CLAUDE.md", ".claude/CLAUDE.md"), r.item("desktop", "home/.claude/CLAUDE.md", ".claude/CLAUDE.md")
	r.put("laptop", ".claude/CLAUDE.md", "# me\nbe terse\n")
	if res, err := r.va.Push([]Item{claudeA}, r.lsA, PushOptions{}); err != nil || actions(res) != "home/.claude/CLAUDE.md:pushed" {
		t.Fatalf("%v %v", res, err)
	}
	// the other device sees it as available, tracks it, pulls it
	if _, avail, err := r.vb.Status(nil, r.lsB); err != nil || len(avail) != 1 || avail[0] != "home/.claude/CLAUDE.md" {
		t.Fatalf("%v %v", avail, err)
	}
	if res, err := r.vb.Pull([]Item{claudeB}, r.lsB, plainWriter{}); err != nil || actions(res) != "home/.claude/CLAUDE.md:pulled" || r.read("desktop", ".claude/CLAUDE.md") != "# me\nbe terse\n" {
		t.Fatalf("%v %v", res, err)
	}
	// an edit travels back
	r.put("desktop", ".claude/CLAUDE.md", "# me\nbe terse\nuse tabs\n")
	if res, _ := r.vb.Push([]Item{claudeB}, r.lsB, PushOptions{}); actions(res) != "home/.claude/CLAUDE.md:pushed" {
		t.Fatalf("%v", res)
	}
	if _, err := r.va.Pull([]Item{claudeA}, r.lsA, plainWriter{}); err != nil || r.read("laptop", ".claude/CLAUDE.md") != "# me\nbe terse\nuse tabs\n" {
		t.Fatalf("%v", err)
	}
	// both edit: nobody wins silently
	r.put("laptop", ".claude/CLAUDE.md", "laptop edit\n")
	r.put("desktop", ".claude/CLAUDE.md", "desktop edit\n")
	if res, _ := r.vb.Push([]Item{claudeB}, r.lsB, PushOptions{}); actions(res) != "home/.claude/CLAUDE.md:pushed" {
		t.Fatal(res)
	}
	res, err := r.va.Pull([]Item{claudeA}, r.lsA, plainWriter{})
	if err != nil || actions(res) != "home/.claude/CLAUDE.md:conflict" {
		t.Fatalf("%v %v", res, err)
	}
	if r.read("laptop", ".claude/CLAUDE.md") != "laptop edit\n" {
		t.Fatal("a conflict must leave the local file alone")
	}
	side := filepath.Join(r.home["laptop"], ".claude", "CLAUDE.md.conflict-desktop-2026-09-26")
	if b, _ := os.ReadFile(side); string(b) != "desktop edit\n" {
		t.Fatalf("the incoming version must be kept beside it: %q", b)
	}
	if res, _ := r.va.Push([]Item{claudeA}, r.lsA, PushOptions{}); actions(res) != "home/.claude/CLAUDE.md:conflict" {
		t.Fatalf("pushing over a change nobody has reviewed is refused: %v", res)
	}
}

func TestTheStorageSeesOnlyCiphertextAndPublicKeys(t *testing.T) {
	r := newRig(t)
	secretText := "my private notes: launch codename ORCHID"
	r.put("laptop", "notes/orchid-plan.md", secretText)
	if _, err := r.va.Push([]Item{r.item("laptop", "home/notes/orchid-plan.md", "notes/orchid-plan.md")}, r.lsA, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(r.tr.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, bad := range []string{"ORCHID", "orchid-plan", "AGE-SECRET-KEY", "home/notes"} {
			if bytes.Contains(b, []byte(bad)) {
				t.Errorf("%s leaks %q", p, bad)
			}
		}
		return nil
	})
	// no device secret is ever written to the storage
	for _, d := range []*Device{r.a, r.b} {
		filepath.WalkDir(r.tr.Root, func(p string, de fs.DirEntry, err error) error {
			if err == nil && !de.IsDir() {
				if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(d.AgeSecret)) || bytes.Contains(b, []byte(d.SignKey)) {
					t.Errorf("%s holds a private key", p)
				}
			}
			return nil
		})
	}
}

func TestForgeriesAndRollbackAreDetected(t *testing.T) {
	r := newRig(t)
	it := r.item("laptop", "home/a.md", "a.md")
	itB := r.item("desktop", "home/a.md", "a.md")
	r.put("laptop", "a.md", "version one\n")
	r.va.Push([]Item{it}, r.lsA, PushOptions{})
	r.vb.Pull([]Item{itB}, r.lsB, plainWriter{})
	oldIndex, _ := r.tr.Read(indexName)

	r.put("laptop", "a.md", "version two\n")
	r.va.Push([]Item{it}, r.lsA, PushOptions{})
	r.vb.Pull([]Item{itB}, r.lsB, plainWriter{})

	// 1. rollback: the storage serves the older index again
	r.tr.Write(indexName, oldIndex)
	if _, err := r.vb.Pull([]Item{itB}, r.lsB, plainWriter{}); !errors.Is(err, ErrRollback) {
		t.Fatalf("an older snapshot must be refused: %v", err)
	}
	r.va.Push([]Item{it}, r.lsA, PushOptions{}) // (laptop re-pushes nothing new; restore a good index below)

	// 2. an attacker with only the PUBLIC keys writes a well-formed index of their own: encrypted to everyone, signed by nobody enrolled
	evil := mkDevice(t, "evil")
	evilV := &Vault{T: r.tr, Dev: evil, roster: r.vb.roster, hist: r.vb.hist}
	idx := &Index{Vault: r.vb.roster.Vault, Counters: map[string]int{"laptop": 99}, Files: map[string]Entry{"home/a.md": {SHA256: hashBytes([]byte("evil")), Size: 4, Obj: "objects/x.age"}}}
	if err := evilV.writeIndex(idx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.vb.Pull([]Item{itB}, r.lsB, plainWriter{}); !errors.Is(err, ErrForged) {
		t.Fatalf("an index signed by a stranger must be refused: %v", err)
	}
}

func TestASwappedObjectAndAForgedRosterAreRefused(t *testing.T) {
	r := newRig(t)
	it := r.item("laptop", "home/a.md", "a.md")
	itB := r.item("desktop", "home/a.md", "a.md")
	r.put("laptop", "a.md", "the real content\n")
	r.va.Push([]Item{it}, r.lsA, PushOptions{})
	idx, _ := r.vb.readIndex()
	obj := idx.Files["home/a.md"].Obj
	// the storage substitutes a different, well-formed ciphertext for the object
	rs, _ := r.vb.recipients(r.vb.roster)
	forged, _ := encrypt([]byte("attacker content\n"), rs)
	r.tr.Write(obj, forged)
	if _, err := r.vb.Pull([]Item{itB}, r.lsB, plainWriter{}); !errors.Is(err, ErrForged) {
		t.Fatalf("a substituted object must fail the signed hash: %v", err)
	}
	if r.read("desktop", "a.md") != "" {
		t.Fatal("nothing may be written from a refused pull")
	}

	// a forged roster: the attacker adds their own device, signed with their own key
	evil := mkDevice(t, "evil")
	einfo, _ := evil.Info(t0)
	cur := r.vb.Roster()
	next := Roster{Vault: cur.Vault, Version: cur.Version + 1, Prev: cur.Hash(), Devices: append(append([]DeviceInfo(nil), cur.Devices...), einfo)}
	sd, _ := signDoc("roster", next.canonical(), evil)
	raw, _ := json.Marshal(sd)
	r.tr.Write("rosters/000003.json", raw)
	if _, err := Open(r.tr, r.b, r.lsB.Pin, nil); !errors.Is(err, ErrForged) {
		t.Fatalf("a roster signed by a non-member must be refused: %v", err)
	}
	r.tr.Remove("rosters/000003.json")

	// a rewritten history: a whole new chain from scratch does not match what this device pinned
	other := mkDevice(t, "laptop")
	fresh := t.TempDir()
	ot := DirTransport{Root: fresh}
	ov, _ := Create(ot, other, t0)
	_ = ov
	for n := 1; n <= 2; n++ {
		b, _ := ot.Read("rosters/000001.json")
		if n == 1 {
			r.tr.Write("rosters/000001.json", b)
		}
	}
	if _, err := Open(r.tr, r.b, r.lsB.Pin, nil); err == nil {
		t.Fatal("rewriting the roster history must be refused")
	}
}

func TestEnrolmentNeedsTheRightFingerprint(t *testing.T) {
	r := newRig(t)
	// the storage's writer posts a join request of their own
	evil := mkDevice(t, "intruder")
	einfo, _ := RequestJoin(r.tr, evil, t0)
	if err := r.va.Approve("intruder", "0000-0000-0000-0000-0000"); err == nil || !strings.Contains(err.Error(), "do NOT approve") {
		t.Fatalf("a wrong fingerprint must be refused: %v", err)
	}
	if err := r.va.Approve("nobody", einfo.Fingerprint()); err == nil {
		t.Fatal("no such request")
	}
	if _, ok := r.va.Roster().Device("intruder"); ok {
		t.Fatal("an unapproved device must not be enrolled")
	}
	// a device that was not approved cannot finish joining, and a wrong vault fingerprint is refused
	if _, err := FinishJoin(r.tr, evil, r.va.Roster().Fingerprint()); err == nil || !strings.Contains(err.Error(), "not been approved") {
		t.Fatalf("%v", err)
	}
	if _, err := FinishJoin(r.tr, r.b, "1111-1111-1111-1111-1111"); err == nil || !strings.Contains(err.Error(), "not the vault you approved") {
		t.Fatalf("%v", err)
	}
	if _, err := Open(r.tr, evil, r.lsB.Pin, nil); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("%v", err)
	}
	// the request stays visible until decided, and approving removes it
	if p, _ := Pending(r.tr); len(p) != 1 || p[0].Name != "intruder" {
		t.Fatalf("%v", p)
	}
}

func TestRevokeReEncryptsEverythingToTheRemainingDevices(t *testing.T) {
	r := newRig(t)
	it := r.item("laptop", "home/a.md", "a.md")
	r.put("laptop", "a.md", "kept private\n")
	r.va.Push([]Item{it}, r.lsA, PushOptions{})
	idx, _ := r.va.readIndex()
	oldObj := idx.Files["home/a.md"].Obj
	if err := r.va.Revoke("laptop"); err == nil {
		t.Fatal("a device cannot revoke itself")
	}
	if err := r.va.Revoke("desktop"); err != nil {
		t.Fatal(err)
	}
	// the revoked device can no longer open the vault, and cannot read the current index or object
	if _, err := Open(r.tr, r.b, r.lsB.Pin, nil); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("%v", err)
	}
	ct, _ := r.tr.Read(indexName)
	if _, err := r.vb.decrypt(ct); err == nil {
		t.Fatal("the revoked device must not decrypt the new index")
	}
	idx2, err := r.va.readIndex()
	if err != nil {
		t.Fatal(err)
	}
	newObj := idx2.Files["home/a.md"].Obj
	if newObj == oldObj {
		t.Fatal("the object must be re-encrypted")
	}
	if _, err := r.tr.Read(oldObj); err == nil {
		t.Fatal("the old ciphertext must be removed from the storage")
	}
	octx, _ := r.tr.Read(newObj)
	if _, err := r.vb.decrypt(octx); err == nil {
		t.Fatal("the revoked device must not decrypt the re-encrypted object")
	}
	// the remaining device still can
	if b, err := r.va.getObject(idx2.Files["home/a.md"]); err != nil || string(b) != "kept private\n" {
		t.Fatalf("%v %q", err, b)
	}
	// and a device that was revoked cannot write a valid index either
	if _, err := r.vb.Push(nil, r.lsB, PushOptions{}); err == nil {
		t.Fatal("a revoked device's operations must fail")
	}
}

func TestAnInterruptedDeviceChangeIsRepairedByRekey(t *testing.T) {
	r := newRig(t)
	it := r.item("laptop", "home/a.md", "a.md")
	r.put("laptop", "a.md", "x\n")
	r.va.Push([]Item{it}, r.lsA, PushOptions{})
	// the roster is published but the re-encryption never ran
	third := mkDevice(t, "tablet")
	tinfo, _ := third.Info(t0)
	cur := r.va.Roster()
	next := Roster{Vault: cur.Vault, Version: cur.Version + 1, Prev: cur.Hash(), Devices: append(append([]DeviceInfo(nil), cur.Devices...), tinfo)}
	if err := r.va.writeRoster(next); err != nil {
		t.Fatal(err)
	}
	if _, err := r.va.readIndex(); err == nil || !strings.Contains(err.Error(), "sync rekey") {
		t.Fatalf("%v", err)
	}
	if err := r.va.Rekey(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.va.readIndex(); err != nil {
		t.Fatalf("after rekey: %v", err)
	}
	// the new device is now a recipient
	tv := &Vault{T: r.tr, Dev: third, roster: r.va.roster, hist: r.va.hist}
	ct, _ := r.tr.Read(indexName)
	if _, err := tv.decrypt(ct); err != nil {
		t.Fatalf("the new recipient must read the index: %v", err)
	}
}

func TestPushRefusesSecretsUnlessOverriddenAndOddFiles(t *testing.T) {
	r := newRig(t)
	fake := "TOKEN=" + "abc123"
	r.put("laptop", "n.md", "notes "+fake)
	it := r.item("laptop", "home/n.md", "n.md")
	scan := func(logical string, b []byte) []string {
		if bytes.Contains(b, []byte(fake)) {
			return []string{"generic-api-key"}
		}
		return nil
	}
	res, _ := r.va.Push([]Item{it}, r.lsA, PushOptions{Scan: scan})
	if actions(res) != "home/n.md:refused" || !strings.Contains(res[0].Detail, "generic-api-key") || !strings.Contains(res[0].Detail, "--allow-secrets") || strings.Contains(res[0].Detail, "abc123") {
		t.Fatalf("%v", res)
	}
	if res, _ := r.va.Push([]Item{it}, r.lsA, PushOptions{Scan: scan, Allow: map[string]bool{"home/n.md": true}}); actions(res) != "home/n.md:pushed" {
		t.Fatalf("%v", res)
	}
	// a directory or a link is never synced
	os.MkdirAll(filepath.Join(r.home["laptop"], "d"), 0o755)
	if res, _ := r.va.Push([]Item{r.item("laptop", "home/d", "d")}, r.lsA, PushOptions{}); actions(res) != "home/d:refused" {
		t.Fatalf("%v", res)
	}
	if err := os.Symlink(filepath.Join(r.home["laptop"], "n.md"), filepath.Join(r.home["laptop"], "link.md")); err == nil {
		if res, _ := r.va.Push([]Item{r.item("laptop", "home/link.md", "link.md")}, r.lsA, PushOptions{}); actions(res) != "home/link.md:refused" {
			t.Fatalf("%v", res)
		}
	}
}

func TestLogicalNamesAndDeviceNames(t *testing.T) {
	for s, want := range map[string]bool{"home/.claude/CLAUDE.md": true, "home/claude/CLAUDE.md": true, "home/./x": false, "home/.git/config": false, "rig/acme/notes.md": true, "../x": false, "/abs": false, "a/../b": false, "": false, "home/a b.md": true, "a//b": false} {
		if ValidLogical(s) != want {
			t.Errorf("%q: %v", s, !want)
		}
	}
	for s, want := range map[string]bool{"laptop": true, "Laptop": false, "a b": false, "-x": false, "desk-2": true} {
		if ValidName(s) != want {
			t.Errorf("%q", s)
		}
	}
	d := mkDevice(t, "laptop")
	back, err := ParseDevice(d.Marshal())
	if err != nil || back.AgeSecret != d.AgeSecret {
		t.Fatal(err)
	}
	i1, _ := d.Info(t0)
	i2, _ := back.Info(t0.Add(time.Hour))
	if i1.Fingerprint() != i2.Fingerprint() || len(strings.Split(i1.Fingerprint(), "-")) != 5 {
		t.Fatalf("%s %s", i1.Fingerprint(), i2.Fingerprint())
	}
	if _, err := ParseDevice([]byte(`{"name":"x"}`)); err == nil {
		t.Fatal("a damaged identity is refused")
	}
	if err := (DirTransport{Root: t.TempDir()}).Write("../evil", []byte("x")); err == nil {
		t.Fatal("storage names cannot traverse")
	}
}
