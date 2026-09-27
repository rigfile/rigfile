package vault

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"filippo.io/age"
)

// Storage names.
const (
	indexName   = "index.age"
	vaultInfo   = "vault.json"
	rosterFmt   = "rosters/%06d.json"
	pendingFmt  = "pending/%s.json"
	objectFmt   = "objects/%s.age"
	maxFileSize = 64 << 20
	maxFiles    = 5000
)

// Vault is one device's view of a vault in some storage.
type Vault struct {
	T      Transport
	Dev    *Device
	Pin    *Roster // the roster this device trusts (persist it; update it from Roster())
	Now    func() time.Time
	roster *Roster
	hist   []Roster // every verified roster version, index 0 = version 1
}

func (v *Vault) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("vault: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Create starts a vault with this device as its only member.
func Create(t Transport, dev *Device, now time.Time) (*Vault, error) {
	if _, err := t.Read(fmt.Sprintf(rosterFmt, 1)); err == nil {
		return nil, errors.New("vault: this storage already holds a vault")
	}
	info, err := dev.Info(now)
	if err != nil {
		return nil, err
	}
	r := Roster{Vault: newID(), Version: 1, Devices: []DeviceInfo{info}}
	v := &Vault{T: t, Dev: dev, Pin: &r, roster: &r, hist: []Roster{r}, Now: func() time.Time { return now }}
	sd, err := signDoc("roster", r.canonical(), dev)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(sd)
	if err := t.Write(fmt.Sprintf(rosterFmt, 1), raw); err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(map[string]any{"format": 1, "id": r.Vault})
	if err := t.Write(vaultInfo, meta); err != nil {
		return nil, err
	}
	if err := v.writeIndex(&Index{Vault: r.Vault, Counters: map[string]int{}, Files: map[string]Entry{}}); err != nil {
		return nil, err
	}
	return v, nil
}

// readRosters loads and verifies the roster chain against the pin.
func readRosters(t Transport, pin *Roster) (*Roster, []Roster, error) {
	var docs []Signed
	for n := 1; ; n++ {
		raw, err := t.Read(fmt.Sprintf(rosterFmt, n))
		if errors.Is(err, ErrNotExist) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		var sd Signed
		if err := json.Unmarshal(raw, &sd); err != nil {
			return nil, nil, fmt.Errorf("vault: roster %d is damaged", n)
		}
		docs = append(docs, sd)
		if n > 10000 {
			return nil, nil, errors.New("vault: implausible roster history")
		}
	}
	return verifyRosterChain(docs, pin)
}

// Open loads the vault as an enrolled device. pin is what this device trusts (nil only for Inspect during a join).
func Open(t Transport, dev *Device, pin *Roster, now func() time.Time) (*Vault, error) {
	if pin == nil {
		return nil, errors.New("vault: this device has no pinned roster; finish joining first")
	}
	r, hist, err := readRosters(t, pin)
	if err != nil {
		return nil, err
	}
	if _, ok := r.Device(dev.Name); !ok {
		return nil, ErrNotEnrolled
	}
	return &Vault{T: t, Dev: dev, Pin: r, roster: r, hist: hist, Now: now}, nil
}

// Inspect reads a vault's roster chain WITHOUT a pin, for a device that is joining: the caller must compare the returned
// roster's fingerprint with what an enrolled device shows before trusting it.
func Inspect(t Transport) (*Roster, error) {
	r, _, err := readRosters(t, nil)
	return r, err
}

// Roster returns the verified roster.
func (v *Vault) Roster() Roster { return *v.roster }

// RequestJoin publishes this device's PUBLIC keys for an enrolled device to approve.
func RequestJoin(t Transport, dev *Device, now time.Time) (DeviceInfo, error) {
	info, err := dev.Info(now)
	if err != nil {
		return DeviceInfo{}, err
	}
	raw, _ := json.Marshal(info)
	return info, t.Write(fmt.Sprintf(pendingFmt, dev.Name), raw)
}

// Pending lists join requests. They are unauthenticated: approval needs the fingerprint a person read off the joining device.
func Pending(t Transport) ([]DeviceInfo, error) {
	names, err := t.List("pending")
	if err != nil {
		return nil, err
	}
	var out []DeviceInfo
	for _, n := range names {
		raw, err := t.Read("pending/" + n)
		if err != nil {
			continue
		}
		var i DeviceInfo
		if json.Unmarshal(raw, &i) == nil && ValidName(i.Name) && n == i.Name+".json" {
			out = append(out, i)
		}
	}
	return out, nil
}

// FinishJoin pins the vault's roster on a joining device, after the person confirmed the vault fingerprint. The roster must
// enrol this device.
func FinishJoin(t Transport, dev *Device, fingerprint string) (*Roster, error) {
	r, err := Inspect(t)
	if err != nil {
		return nil, err
	}
	if _, ok := r.Device(dev.Name); !ok {
		return nil, errors.New("vault: this device has not been approved yet")
	}
	if r.Fingerprint() != fingerprint {
		return nil, fmt.Errorf("vault: the vault fingerprint is %s, not %s: this is not the vault you approved on the other device", r.Fingerprint(), fingerprint)
	}
	return r, nil
}

func (v *Vault) recipients(r *Roster) ([]age.Recipient, error) {
	var out []age.Recipient
	for _, d := range r.Devices {
		rc, err := age.ParseX25519Recipient(d.Age)
		if err != nil {
			return nil, fmt.Errorf("vault: device %s has a damaged key", d.Name)
		}
		out = append(out, rc)
	}
	return out, nil
}

func encrypt(plain []byte, rs []age.Recipient) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rs...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (v *Vault) decrypt(ct []byte) ([]byte, error) {
	id, err := v.Dev.identity()
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(ct), id)
	if err != nil {
		return nil, fmt.Errorf("vault: cannot decrypt (this device is not a recipient, or the data was altered): %w", err)
	}
	return io.ReadAll(io.LimitReader(r, maxFileSize+1))
}

// readIndex loads, decrypts and verifies the index.
func (v *Vault) readIndex() (*Index, error) {
	ct, err := v.T.Read(indexName)
	if err != nil {
		return nil, err
	}
	plain, err := v.decrypt(ct)
	if err != nil {
		return nil, err
	}
	var sd Signed
	if err := json.Unmarshal(plain, &sd); err != nil {
		return nil, errors.New("vault: the index is damaged")
	}
	signer, ok := v.roster.Device(sd.By)
	if !ok || !verifyDoc("index", sd, signer.Sign) {
		return nil, fmt.Errorf("%w: the index", ErrForged)
	}
	var idx Index
	if err := json.Unmarshal([]byte(sd.Body), &idx); err != nil {
		return nil, errors.New("vault: the index is damaged")
	}
	if idx.Vault != v.roster.Vault {
		return nil, fmt.Errorf("%w: the index belongs to another vault", ErrForged)
	}
	if idx.Roster != v.roster.Version {
		return nil, fmt.Errorf("vault: the index was written under roster %d but the roster is at %d: a device change is in progress or was interrupted; an enrolled device can run `rigfile sync rekey`", idx.Roster, v.roster.Version)
	}
	if idx.Counters == nil {
		idx.Counters = map[string]int{}
	}
	if idx.Files == nil {
		idx.Files = map[string]Entry{}
	}
	return &idx, nil
}

func (v *Vault) writeIndex(idx *Index) error {
	idx.Roster, idx.Writer = v.roster.Version, v.Dev.Name
	idx.Counters[v.Dev.Name]++
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	sd, err := signDoc("index", body, v.Dev)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(sd)
	rs, err := v.recipients(v.roster)
	if err != nil {
		return err
	}
	ct, err := encrypt(raw, rs)
	if err != nil {
		return err
	}
	return v.T.Write(indexName, ct)
}

func (v *Vault) putObject(plain []byte, rs []age.Recipient) (string, error) {
	ct, err := encrypt(plain, rs)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(ct)
	name := fmt.Sprintf(objectFmt, hex.EncodeToString(h[:]))
	return name, v.T.Write(name, ct)
}

func (v *Vault) getObject(e Entry) ([]byte, error) {
	ct, err := v.T.Read(e.Obj)
	if err != nil {
		return nil, fmt.Errorf("vault: the object for a file is missing from the storage: %w", err)
	}
	plain, err := v.decrypt(ct)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(plain)
	if hex.EncodeToString(h[:]) != e.SHA256 || int64(len(plain)) != e.Size {
		return nil, fmt.Errorf("%w: an object does not match the signed index", ErrForged)
	}
	return plain, nil
}

func (v *Vault) writeRoster(r Roster) error {
	sd, err := signDoc("roster", r.canonical(), v.Dev)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(sd)
	if err := v.T.Write(fmt.Sprintf(rosterFmt, r.Version), raw); err != nil {
		return err
	}
	v.roster, v.Pin = &r, &r
	v.hist = append(v.hist, r)
	return nil
}

// Approve enrols a pending device. fingerprint is what the person read off the JOINING device's screen: an attacker who can
// write to the storage can post a join request, but cannot make its fingerprint match.
func (v *Vault) Approve(name, fingerprint string) error {
	pend, err := Pending(v.T)
	if err != nil {
		return err
	}
	var info *DeviceInfo
	for i := range pend {
		if pend[i].Name == name {
			info = &pend[i]
		}
	}
	if info == nil {
		return fmt.Errorf("vault: there is no join request from %q", name)
	}
	if info.Fingerprint() != fingerprint {
		return fmt.Errorf("vault: the request from %q has fingerprint %s, not %s: do NOT approve a device whose fingerprint differs from the one on its own screen", name, info.Fingerprint(), fingerprint)
	}
	if _, exists := v.roster.Device(name); exists {
		return fmt.Errorf("vault: %q is already enrolled", name)
	}
	next := Roster{Vault: v.roster.Vault, Version: v.roster.Version + 1, Prev: v.roster.Hash(), Devices: append(append([]DeviceInfo(nil), v.roster.Devices...), *info)}
	sort.Slice(next.Devices, func(i, j int) bool { return next.Devices[i].Name < next.Devices[j].Name })
	if err := v.writeRoster(next); err != nil {
		return err
	}
	if err := v.Rekey(); err != nil {
		return err
	}
	return v.T.Remove(fmt.Sprintf(pendingFmt, name))
}

// Revoke removes a device: everything is re-encrypted to the remaining devices. What the revoked device already decrypted
// stays known to it (and any earlier copy of the storage, such as git history, stays readable by it): treat it as exposed.
func (v *Vault) Revoke(name string) error {
	if _, ok := v.roster.Device(name); !ok {
		return fmt.Errorf("vault: %q is not enrolled", name)
	}
	if name == v.Dev.Name {
		return errors.New("vault: a device cannot revoke itself; revoke it from another enrolled device")
	}
	var rest []DeviceInfo
	for _, d := range v.roster.Devices {
		if d.Name != name {
			rest = append(rest, d)
		}
	}
	next := Roster{Vault: v.roster.Vault, Version: v.roster.Version + 1, Prev: v.roster.Hash(), Devices: rest}
	if err := v.writeRoster(next); err != nil {
		return err
	}
	return v.Rekey()
}

// Rekey re-encrypts the index and every object to the current roster (idempotent; repairs an interrupted device change). The
// old objects are removed.
func (v *Vault) Rekey() error {
	// the index may still be under an older roster: read it under that roster's rules by trusting only our own signature chain
	ct, err := v.T.Read(indexName)
	if err != nil {
		return err
	}
	plain, err := v.decrypt(ct)
	if err != nil {
		return err
	}
	var sd Signed
	if err := json.Unmarshal(plain, &sd); err != nil {
		return errors.New("vault: the index is damaged")
	}
	var idx Index
	if err := json.Unmarshal([]byte(sd.Body), &idx); err != nil {
		return errors.New("vault: the index is damaged")
	}
	if idx.Vault != v.roster.Vault {
		return fmt.Errorf("%w: the index belongs to another vault", ErrForged)
	}
	// authenticate it against the roster version it names (a past roster in the verified chain)
	past, err := v.rosterAt(idx.Roster)
	if err != nil {
		return err
	}
	signer, ok := past.Device(sd.By)
	if !ok || !verifyDoc("index", sd, signer.Sign) {
		return fmt.Errorf("%w: the index", ErrForged)
	}
	rs, err := v.recipients(v.roster)
	if err != nil {
		return err
	}
	var old []string
	for logical, e := range idx.Files {
		obj, err := v.T.Read(e.Obj)
		if err != nil {
			return fmt.Errorf("vault: object for %s is missing: %w", logical, err)
		}
		pt, err := v.decrypt(obj)
		if err != nil {
			return err
		}
		h := sha256.Sum256(pt)
		if hex.EncodeToString(h[:]) != e.SHA256 {
			return fmt.Errorf("%w: an object does not match the signed index", ErrForged)
		}
		name, err := v.putObject(pt, rs)
		if err != nil {
			return err
		}
		old = append(old, e.Obj)
		e.Obj = name
		idx.Files[logical] = e
	}
	if idx.Counters == nil {
		idx.Counters = map[string]int{}
	}
	if err := v.writeIndex(&idx); err != nil {
		return err
	}
	for _, o := range old {
		newSet := false
		for _, e := range idx.Files {
			if e.Obj == o {
				newSet = true
			}
		}
		if !newSet {
			_ = v.T.Remove(o)
		}
	}
	return nil
}

// rosterAt returns a roster version from the verified history (never re-read from the storage).
func (v *Vault) rosterAt(version int) (*Roster, error) {
	if version < 1 || version > len(v.hist) {
		return nil, fmt.Errorf("vault: the index names roster %d, which is not in the verified history", version)
	}
	r := v.hist[version-1]
	return &r, nil
}
