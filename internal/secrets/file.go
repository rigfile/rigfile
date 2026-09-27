package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"filippo.io/age"

	"github.com/rigfile/rigfile/internal/platform"
)

var (
	// ErrBadPassphrase means decryption failed: wrong passphrase or a corrupted/tampered file.
	ErrBadPassphrase = errors.New("secrets: cannot decrypt secrets file (wrong passphrase or corrupted file)")
	// ErrWeakPassphrase means a new store's passphrase is too short.
	ErrWeakPassphrase = errors.New("secrets: passphrase must be at least 12 characters")
)

// MinPassphraseLen is enforced when the file is created or re-encrypted.
const MinPassphraseLen = 12

// FileStore keeps all secrets in one age-encrypted (scrypt passphrase) file, for machines without a
// keychain (headless Linux, plan §7.2). The whole file is decrypted per operation; it is small.
//
// Writers (Set, Delete) hold an advisory lock (<path>.lock) across their read-modify-write, so
// concurrent `rigfile secrets set` runs cannot lose each other's updates (unix; Windows in Stage 3).
type FileStore struct {
	Path       string
	Passphrase func() (string, error) // asked on every operation; never cached in the struct
	WorkFactor int                    // scrypt log2(N); 0 = age default. Lowered only by tests.
}

// Kind implements Store.
func (*FileStore) Kind() string { return "encrypted-file" }

func (f *FileStore) load() (map[string]string, error) {
	blob, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	pass, err := f.Passphrase()
	if err != nil {
		return nil, err
	}
	id, err := age.NewScryptIdentity(pass)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(blob), id)
	if err != nil {
		return nil, ErrBadPassphrase
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, ErrBadPassphrase
	}
	defer wipe(plain)
	m := map[string]string{}
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, fmt.Errorf("secrets file has unexpected contents: %w", err)
	}
	return m, nil
}

func (f *FileStore) save(m map[string]string) error {
	pass, err := f.Passphrase()
	if err != nil {
		return err
	}
	if len(pass) < MinPassphraseLen {
		return ErrWeakPassphrase
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	defer wipe(plain)
	rcp, err := age.NewScryptRecipient(pass)
	if err != nil {
		return err
	}
	if f.WorkFactor > 0 {
		rcp.SetWorkFactor(f.WorkFactor)
	}
	var out bytes.Buffer
	w, err := age.Encrypt(&out, rcp)
	if err != nil {
		return err
	}
	if _, err := w.Write(plain); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return platform.WritePrivate(f.Path, out.Bytes())
}

// Set implements Store.
func (f *FileStore) Set(ref string, value []byte) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	unlock, err := lockFile(f.Path)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[ref] = string(value)
	return f.save(m)
}

// Get implements Store.
func (f *FileStore) Get(ref string) ([]byte, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	m, err := f.load()
	if err != nil {
		return nil, err
	}
	v, ok := m[ref]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(v), nil
}

// Delete implements Store.
func (f *FileStore) Delete(ref string) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	unlock, err := lockFile(f.Path)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[ref]; !ok {
		return nil
	}
	delete(m, ref)
	return f.save(m)
}

// Refs lists stored refs (names only) so `secrets status` can work without a keychain index.
func (f *FileStore) Refs() ([]string, error) {
	m, err := f.load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
