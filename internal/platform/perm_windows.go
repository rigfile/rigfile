//go:build windows

package platform

// WritePrivate is stubbed on Windows until Stage 3 (needs a user-only ACL, plan §7.3 rule 5).
// Failing closed is deliberate: better no secret file than a world-readable one.
func WritePrivate(path string, data []byte) error {
	return ErrNotSupported
}
