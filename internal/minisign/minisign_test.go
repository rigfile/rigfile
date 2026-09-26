package minisign

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

func newKey(t *testing.T, seedByte byte) KeyPair {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = seedByte
	}
	return KeyPair{ID: [8]byte{1, 2, 3, 4, 5, 6, 7, byte(seedByte)}, Private: ed25519.NewKeyFromSeed(seed)}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	k := newKey(t, 7)
	pk, err := ParsePublicKey(k.Public())
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("abc123  rigfile_1.0.0_linux_amd64.tar.gz\n")
	sig := k.Sign(msg, "timestamp:1 file:SHA256SUMS")
	if err := Verify(pk, msg, sig); err != nil {
		t.Fatal(err)
	}
	// a bare base64 line is accepted as a public key too
	lines := strings.Split(strings.TrimSpace(k.Public()), "\n")
	if _, err := ParsePublicKey(lines[1]); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRefusesEveryTampering(t *testing.T) {
	k := newKey(t, 7)
	pk, _ := ParsePublicKey(k.Public())
	msg := []byte("the release checksums")
	sig := k.Sign(msg, "file:SHA256SUMS")

	if err := Verify(pk, []byte("the release checksums!"), sig); err == nil {
		t.Error("a modified file must fail")
	}
	other, _ := ParsePublicKey(newKey(t, 9).Public())
	if err := Verify(other, msg, sig); err == nil || !strings.Contains(err.Error(), "different key") {
		t.Errorf("wrong key: %v", err)
	}
	// same key id, different key material
	forged := *pk
	forged.Key = newKey(t, 9).Private.Public().(ed25519.PublicKey)
	if err := Verify(&forged, msg, sig); err == nil {
		t.Error("a different key with the same id must fail")
	}
	// an edited trusted comment (e.g. to claim a newer version) must fail
	if err := Verify(pk, msg, strings.Replace(sig, "file:SHA256SUMS", "file:EVIL", 1)); err == nil || !strings.Contains(err.Error(), "trusted comment") {
		t.Errorf("trusted comment: %v", err)
	}
	for _, bad := range []string{"", "not a signature", "untrusted comment: x\n!!!\ntrusted comment: y\nAAAA\n"} {
		if err := Verify(pk, msg, bad); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
	if _, err := ParsePublicKey("untrusted comment: x\nAAAA"); err == nil {
		t.Error("a short key must fail")
	}
}
