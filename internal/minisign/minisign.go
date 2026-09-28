// Package minisign verifies (and, for tests and the release tooling, creates) minisign signatures: Ed25519 over the
// file's BLAKE2b-512 hash ("ED", what minisign 0.6+ writes) or over the raw file ("Ed", legacy), plus the signed
// trusted comment. Format as in https://jedisct1.github.io/minisign/ . UNVERIFIED against the real minisign binary
// until the owner runs `rigfile verify-signature` on a file signed by it (docs/owner-checklist.md).
package minisign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// PublicKey is a parsed minisign public key.
type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

// ParsePublicKey reads a public key file (comment line + base64 line) or just the base64 line.
func ParsePublicKey(text string) (*PublicKey, error) {
	line := lastNonComment(text)
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) != 2+8+32 || string(raw[:2]) != "Ed" {
		return nil, errors.New("minisign: not a minisign public key")
	}
	pk := &PublicKey{Key: ed25519.PublicKey(append([]byte(nil), raw[10:]...))}
	copy(pk.ID[:], raw[2:10])
	return pk, nil
}

func lastNonComment(text string) string {
	var last string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "untrusted comment:") && !strings.HasPrefix(l, "trusted comment:") {
			last = l
		}
	}
	return last
}

// Signature is a parsed .minisig file.
type Signature struct {
	Algo           string // "Ed" or "ED"
	ID             [8]byte
	Sig            [64]byte
	TrustedComment string
	GlobalSig      [64]byte
}

// ParseSignature reads a .minisig file.
func ParseSignature(text string) (*Signature, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "untrusted comment:") || !strings.HasPrefix(lines[2], "trusted comment:") {
		return nil, errors.New("minisign: not a minisign signature file")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != 2+8+64 {
		return nil, errors.New("minisign: malformed signature")
	}
	g, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(g) != 64 {
		return nil, errors.New("minisign: malformed global signature")
	}
	s := &Signature{Algo: string(raw[:2]), TrustedComment: strings.TrimPrefix(lines[2], "trusted comment: ")}
	if s.Algo != "Ed" && s.Algo != "ED" {
		return nil, fmt.Errorf("minisign: unsupported signature algorithm %q", s.Algo)
	}
	copy(s.ID[:], raw[2:10])
	copy(s.Sig[:], raw[10:])
	copy(s.GlobalSig[:], g)
	return s, nil
}

// Verify checks that sig is a valid signature of msg by pk, including the trusted comment.
func Verify(pk *PublicKey, msg []byte, sigText string) error {
	sig, err := ParseSignature(sigText)
	if err != nil {
		return err
	}
	if sig.ID != pk.ID {
		return errors.New("minisign: the signature was made by a different key")
	}
	signed := msg
	if sig.Algo == "ED" {
		h := blake2b.Sum512(msg)
		signed = h[:]
	}
	if !ed25519.Verify(pk.Key, signed, sig.Sig[:]) {
		return errors.New("minisign: the signature does not match the file")
	}
	if !ed25519.Verify(pk.Key, append(append([]byte(nil), sig.Sig[:]...), sig.TrustedComment...), sig.GlobalSig[:]) {
		return errors.New("minisign: the trusted comment was altered")
	}
	return nil
}

// KeyPair is an Ed25519 key with its minisign key id.
type KeyPair struct {
	ID      [8]byte
	Private ed25519.PrivateKey
}

// Public renders the public key file.
func (k KeyPair) Public() string {
	raw := append(append([]byte("Ed"), k.ID[:]...), k.Private.Public().(ed25519.PublicKey)...)
	return "untrusted comment: minisign public key: " + strings.ToUpper(hex.EncodeToString(reverse(k.ID[:]))) + "\n" + base64.StdEncoding.EncodeToString(raw) + "\n"
}

// Sign produces a prehashed ("ED") signature file for msg with a trusted comment.
func (k KeyPair) Sign(msg []byte, trusted string) string {
	h := blake2b.Sum512(msg)
	sig := ed25519.Sign(k.Private, h[:])
	raw := append(append([]byte("ED"), k.ID[:]...), sig...)
	global := ed25519.Sign(k.Private, append(append([]byte(nil), sig...), trusted...))
	var b bytes.Buffer
	b.WriteString("untrusted comment: signature from rigfile release tooling\n")
	b.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n")
	b.WriteString("trusted comment: " + trusted + "\n")
	b.WriteString(base64.StdEncoding.EncodeToString(global) + "\n")
	return b.String()
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}
