package vault

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Signed is a document and the signature of the device that wrote it. Body is the JSON text that was signed (kept as text so
// it is verified byte for byte, never re-serialised).
type Signed struct {
	Body string `json:"body"`
	By   string `json:"by"`
	Sig  string `json:"sig"`
}

func signedInput(kind, body string) []byte { return []byte("rigfile-vault-v1\n" + kind + "\n" + body) }

func signDoc(kind string, body []byte, d *Device) (Signed, error) {
	k, err := d.signer()
	if err != nil {
		return Signed{}, err
	}
	return Signed{Body: string(body), By: d.Name, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(k, signedInput(kind, string(body))))}, nil
}

// verifyDoc checks the signature against the signer's public key (base64).
func verifyDoc(kind string, s Signed, signPub string) bool {
	pub, err := base64.StdEncoding.DecodeString(signPub)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(s.Sig)
	if err != nil {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), signedInput(kind, s.Body), sig)
}

// Roster is the list of enrolled devices, one version per change. Version n names the hash of version n-1, and is signed by a
// device that was enrolled in version n-1 (version 1 is signed by the device it enrols), so the history cannot be rewritten.
type Roster struct {
	Vault   string       `json:"vault"`
	Version int          `json:"version"`
	Devices []DeviceInfo `json:"devices"`
	Prev    string       `json:"prev"`
}

func (r Roster) canonical() []byte { b, _ := json.Marshal(r); return b }

// Hash identifies a roster version.
func (r Roster) Hash() string { return sum(string(r.canonical())) }

// Fingerprint is the roster's short form for a person to compare.
func (r Roster) Fingerprint() string { return groups(r.Hash()) }

// Device finds an enrolled device.
func (r Roster) Device(name string) (DeviceInfo, bool) {
	for _, d := range r.Devices {
		if d.Name == name {
			return d, true
		}
	}
	return DeviceInfo{}, false
}

// Errors a caller can act on.
var (
	ErrNotEnrolled = errors.New("vault: this device is not in the vault")
	ErrRollback    = errors.New("vault: the storage served an older snapshot than this device has already seen")
	ErrForged      = errors.New("vault: a document was not signed by an enrolled device")
)

// verifyRosterChain checks every version from 1 to the last: consecutive, hash-chained, each signed by a device of the
// version before it. With a pin, the chain must contain the pinned version unchanged (the storage cannot rewrite history a
// device already trusts).
func verifyRosterChain(docs []Signed, pin *Roster) (*Roster, []Roster, error) {
	if len(docs) == 0 {
		return nil, nil, errors.New("vault: no roster in the storage")
	}
	var hist []Roster
	var prev *Roster
	var last Roster
	for i, d := range docs {
		var r Roster
		if err := json.Unmarshal([]byte(d.Body), &r); err != nil {
			return nil, nil, fmt.Errorf("vault: roster %d is damaged", i+1)
		}
		if r.Version != i+1 {
			return nil, nil, fmt.Errorf("vault: the roster history is not consecutive (version %d where %d was expected)", r.Version, i+1)
		}
		signerSet := r
		if prev != nil {
			signerSet = *prev
			if r.Prev != prev.Hash() || r.Vault != prev.Vault {
				return nil, nil, fmt.Errorf("vault: roster %d does not continue roster %d", r.Version, prev.Version)
			}
		} else if r.Prev != "" {
			return nil, nil, errors.New("vault: the first roster names a predecessor")
		}
		signer, ok := signerSet.Device(d.By)
		if !ok || !verifyDoc("roster", d, signer.Sign) {
			return nil, nil, fmt.Errorf("%w: roster %d", ErrForged, r.Version)
		}
		if pin != nil && r.Version == pin.Version && r.Hash() != pin.Hash() {
			return nil, nil, errors.New("vault: the storage's roster history differs from the one this device trusts")
		}
		rc := r
		prev, last = &rc, r
		hist = append(hist, r)
	}
	if pin != nil && last.Version < pin.Version {
		return nil, nil, ErrRollback
	}
	return &last, hist, nil
}

// Entry is one synced file in the index.
type Entry struct {
	SHA256  string `json:"sha256"` // of the plaintext
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"`
	Obj     string `json:"obj"` // the object's name in the storage
	By      string `json:"by"`
	Counter int    `json:"counter"`
}

// Index is the list of files, encrypted to all devices and signed by the device that last wrote it.
type Index struct {
	Vault    string           `json:"vault"`
	Roster   int              `json:"roster"` // the roster version it was written under
	Writer   string           `json:"writer"`
	Counters map[string]int   `json:"counters"` // per device: how many index writes it has made
	Files    map[string]Entry `json:"files"`
}
