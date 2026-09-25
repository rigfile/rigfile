// Package secrets stores and retrieves secret values for `secret://` refs (plan §7, Level 1).
//
// Backends: the OS keychain (macOS Keychain, Linux Secret Service, Windows Credential Manager via
// zalando/go-keyring) and, where no keychain exists (headless Linux), an age-encrypted file.
//
// Threat notes:
//   - Values never appear in argv, logs or errors from this package. There is no "get" CLI command:
//     values are only read in-process by `rigfile exec` to build a child's environment.
//   - The file backend is scrypt-encrypted (age); the file is 0600 and written atomically.
//   - Refs are validated so a ref can't be used to smuggle path or shell metacharacters.
//   - The Rigfile server never receives secrets (plan §7.3 rule 1): nothing here touches the network.
package secrets

import (
	"errors"
	"fmt"
	"regexp"
)

// Service is the keychain service name all Rigfile secrets live under (plan §7.2).
const Service = "rigfile"

// ErrNotFound means no secret is stored for the ref.
var ErrNotFound = errors.New("secrets: not found")

// ErrBadRef means the ref is not a valid secret path.
var ErrBadRef = errors.New("secrets: invalid ref")

var refRe = regexp.MustCompile(`^[a-z0-9_-]+(?:/[a-z0-9_-]+)+$`)

// ValidateRef checks a ref path such as "alpaca/api_key" (no "secret://" scheme).
func ValidateRef(ref string) error {
	if len(ref) > 200 || !refRe.MatchString(ref) {
		return fmt.Errorf("%w: %q (want lowercase path like alpaca/api_key)", ErrBadRef, ref)
	}
	return nil
}

// Store is a secret backend.
type Store interface {
	// Set stores value for ref, replacing any existing value.
	Set(ref string, value []byte) error
	// Get returns the value, or ErrNotFound.
	Get(ref string) ([]byte, error)
	// Delete removes the secret; deleting a missing ref is not an error.
	Delete(ref string) error
	// Kind names the backend for `doctor` ("keychain" or "encrypted-file").
	Kind() string
}
