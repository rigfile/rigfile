package main

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/minisign"
)

func TestVerifySignatureCommand(t *testing.T) {
	k := minisign.KeyPair{ID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, Private: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))}
	dir := t.TempDir()
	file := filepath.Join(dir, "SHA256SUMS")
	_ = os.WriteFile(file, []byte("abc  rigfile.tar.gz\n"), 0o644)
	_ = os.WriteFile(file+".minisig", []byte(k.Sign([]byte("abc  rigfile.tar.gz\n"), "rigfile v1")), 0o644)
	pub := filepath.Join(dir, "min.pub")
	_ = os.WriteFile(pub, []byte(k.Public()), 0o644)
	m := newMachine(t)
	if r := m.run("", "verify-signature", file, "--pubkey", pub); r.code != 0 || !strings.Contains(r.out, "signature is valid") {
		t.Fatalf("%+v", r)
	}
	_ = os.WriteFile(file, []byte("tampered\n"), 0o644)
	if r := m.run("", "verify-signature", file, "--pubkey", pub); r.code != 1 || !strings.Contains(r.err, "does not match") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "verify-signature", file); r.code != 1 || !strings.Contains(r.err, "no release signing key") {
		t.Fatalf("a build without a key must say so: %+v", r)
	}
}

func TestSelfUpdateRefusesWithoutAReleaseKey(t *testing.T) {
	m := newMachine(t)
	if r := m.run("", "self-update", "--check"); r.code != 1 || !strings.Contains(r.err, "no release signing key") {
		t.Fatalf("%+v", r)
	}
}
