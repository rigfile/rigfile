// Package vault is end-to-end encrypted sync of a person's private files between their own machines (docs/private-sync.md).
//
// The storage (a directory, in practice a git repository or a synced folder) is treated as hostile: it sees ciphertext and
// public keys only. Confidentiality is age (X25519, every file encrypted to every enrolled device). Authenticity is separate,
// because anyone holding public keys can produce valid age ciphertext: every device has an Ed25519 signing key, the list of
// devices (the roster) is a signed hash chain that each device pins locally, and the index of files is signed by whoever wrote
// it and carries per-device counters so an old snapshot cannot be served again.
package vault

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"filippo.io/age"
)

// DeviceInfo is the public half of a device: what other devices and the storage may see.
type DeviceInfo struct {
	Name  string `json:"name"`
	Age   string `json:"age"`   // age X25519 recipient (public)
	Sign  string `json:"sign"`  // Ed25519 public key, base64
	Added string `json:"added"` // RFC 3339
}

// Device is this machine's identity in a vault. The secret parts never leave the local secret store.
type Device struct {
	Name      string `json:"name"`
	AgeSecret string `json:"age_secret"`
	SignKey   string `json:"sign_key"` // Ed25519 private key, base64
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidName reports whether s can name a device.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// NewDevice generates fresh keys for a device.
func NewDevice(name string) (*Device, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("vault: %q is not a device name (lower-case letters, digits, hyphens)", name)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Device{Name: name, AgeSecret: id.String(), SignKey: base64.StdEncoding.EncodeToString(priv)}, nil
}

func (d *Device) identity() (*age.X25519Identity, error) { return age.ParseX25519Identity(d.AgeSecret) }

func (d *Device) signer() (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(d.SignKey)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("vault: the device signing key is damaged")
	}
	return ed25519.PrivateKey(b), nil
}

// Info is the device's public description.
func (d *Device) Info(now time.Time) (DeviceInfo, error) {
	id, err := d.identity()
	if err != nil {
		return DeviceInfo{}, err
	}
	pk, err := d.signer()
	if err != nil {
		return DeviceInfo{}, err
	}
	return DeviceInfo{Name: d.Name, Age: id.Recipient().String(), Sign: base64.StdEncoding.EncodeToString(pk.Public().(ed25519.PublicKey)), Added: now.UTC().Format(time.RFC3339)}, nil
}

// Marshal / ParseDevice serialise the secret identity for the local secret store.
func (d *Device) Marshal() []byte { b, _ := json.Marshal(d); return b }

// ParseDevice reads a stored identity.
func ParseDevice(b []byte) (*Device, error) {
	var d Device
	if err := json.Unmarshal(b, &d); err != nil || !ValidName(d.Name) {
		return nil, errors.New("vault: the stored device identity is damaged")
	}
	if _, err := d.identity(); err != nil {
		return nil, err
	}
	if _, err := d.signer(); err != nil {
		return nil, err
	}
	return &d, nil
}

// Fingerprint identifies a device's keys for a person to compare across two screens: five groups of four hex digits.
func (i DeviceInfo) Fingerprint() string { return groups(sum(i.Name + "\n" + i.Age + "\n" + i.Sign)) }

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func groups(hexs string) string {
	var parts []string
	for i := 0; i < 20; i += 4 {
		parts = append(parts, hexs[i:i+4])
	}
	return strings.Join(parts, "-")
}
