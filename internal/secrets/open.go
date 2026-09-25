package secrets

import (
	"errors"
	"fmt"
)

// OpenOptions selects a backend.
type OpenOptions struct {
	// FilePath and Passphrase configure the encrypted-file fallback. Both are needed for it to be used.
	FilePath   string
	Passphrase func() (string, error)
	// WorkFactor is passed to the file backend (tests only).
	WorkFactor int
	// KeyringProbe overrides KeyringAvailable (tests).
	KeyringProbe func() error
}

// Open returns the OS keychain if it is usable, otherwise the encrypted-file fallback.
// `doctor` must warn when the returned Kind() is "encrypted-file" (plan §7.2).
func Open(o OpenOptions) (Store, error) {
	probe := o.KeyringProbe
	if probe == nil {
		probe = KeyringAvailable
	}
	kerr := probe()
	if kerr == nil {
		return KeyringStore{}, nil
	}
	if o.FilePath == "" || o.Passphrase == nil {
		return nil, fmt.Errorf("no usable secret store: the OS keychain is unavailable (%v) and no encrypted-file fallback is configured", kerr)
	}
	return &FileStore{Path: o.FilePath, Passphrase: o.Passphrase, WorkFactor: o.WorkFactor}, nil
}

// IsNotFound reports whether err means "no such secret".
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
