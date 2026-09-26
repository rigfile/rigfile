package secrets

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/zalando/go-keyring"
)

// KeyringStore is the OS keychain backend.
type KeyringStore struct{}

// Kind implements Store.
func (KeyringStore) Kind() string { return "keychain" }

// Set implements Store. Values must be valid UTF-8 without NUL (the keychain APIs are text-based).
func (KeyringStore) Set(ref string, value []byte) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	if !utf8.Valid(value) || containsNUL(value) {
		return errors.New("secrets: value must be UTF-8 text without NUL bytes")
	}
	if err := keyring.Set(Service, ref, string(value)); err != nil {
		if errors.Is(err, keyring.ErrSetDataTooBig) {
			// Windows Credential Manager caps a secret at 2560 bytes (macOS at ~3000): a certificate or a JSON key file needs the file itself kept outside Rigfile.
			return fmt.Errorf("keychain set %s: the value is too large for the OS keychain (about 2.5 KB); store a shorter value (a token rather than a whole key file)", ref)
		}
		return fmt.Errorf("keychain set %s: %w", ref, redact(err))
	}
	return nil
}

// Get implements Store.
func (KeyringStore) Get(ref string) ([]byte, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	v, err := keyring.Get(Service, ref)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("keychain get %s: %w", ref, redact(err))
	}
	return []byte(v), nil
}

// Delete implements Store.
func (KeyringStore) Delete(ref string) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	err := keyring.Delete(Service, ref)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("keychain delete %s: %w", ref, redact(err))
}

// KeyringAvailable reports whether the OS keychain is usable, by reading a key that does not exist:
// a working backend answers "not found"; anything else (no D-Bus session, no Secret Service, locked
// and non-interactive, ...) means unavailable. It never writes.
func KeyringAvailable() error {
	_, err := keyring.Get(Service, "rigfile/availability-probe")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

func containsNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// redact strips anything that could echo a value. go-keyring errors don't include values, but we
// wrap defensively so a future change can't leak one through an error string.
func redact(err error) error { return errors.New(err.Error()) }
