package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestValidateRef(t *testing.T) {
	good := []string{"alpaca/api_key", "a/b/c", "gh/token-1"}
	bad := []string{"", "noslash", "Upper/case", "a/../b", "a/b c", "secret://a/b", "a/b;rm", "/abs/path", "a//b", strings.Repeat("a/", 120)}
	for _, r := range good {
		if err := ValidateRef(r); err != nil {
			t.Errorf("%q should be valid: %v", r, err)
		}
	}
	for _, r := range bad {
		if err := ValidateRef(r); !errors.Is(err, ErrBadRef) {
			t.Errorf("%q should be rejected, got %v", r, err)
		}
	}
}

// --- keychain backend (mock: never touches the real Keychain) -----------------------------------

func TestKeyringStoreRoundTrip(t *testing.T) {
	keyring.MockInit()
	s := KeyringStore{}
	if _, err := s.Get("alpaca/api_key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.Set("alpaca/api_key", []byte("FAKE-VALUE-1")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("alpaca/api_key")
	if err != nil || string(got) != "FAKE-VALUE-1" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if err := s.Set("alpaca/api_key", []byte("FAKE-VALUE-2")); err != nil { // overwrite
		t.Fatal(err)
	}
	if got, _ := s.Get("alpaca/api_key"); string(got) != "FAKE-VALUE-2" {
		t.Fatalf("overwrite failed: %q", got)
	}
	if err := s.Delete("alpaca/api_key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("alpaca/api_key"); err != nil { // deleting a missing ref is fine
		t.Fatalf("delete of missing ref: %v", err)
	}
	if _, err := s.Get("alpaca/api_key"); !errors.Is(err, ErrNotFound) {
		t.Fatal("still present after delete")
	}
}

func TestKeyringStoreRejectsBadInput(t *testing.T) {
	keyring.MockInit()
	s := KeyringStore{}
	if err := s.Set("bad ref", []byte("v")); !errors.Is(err, ErrBadRef) {
		t.Fatalf("bad ref: %v", err)
	}
	if err := s.Set("a/b", []byte("has\x00nul")); err == nil {
		t.Fatal("NUL bytes must be rejected")
	}
	if err := s.Set("a/b", []byte{0xff, 0xfe}); err == nil {
		t.Fatal("invalid UTF-8 must be rejected")
	}
}

func TestKeyringErrorsDoNotContainValues(t *testing.T) {
	keyring.MockInitWithError(errors.New("backend down"))
	err := KeyringStore{}.Set("a/b", []byte("FAKE-SECRET-VALUE"))
	if err == nil || strings.Contains(err.Error(), "FAKE-SECRET-VALUE") {
		t.Fatalf("error must exist and must not echo the value: %v", err)
	}
	if KeyringAvailable() == nil {
		t.Fatal("a failing backend must be reported unavailable")
	}
}

func TestKeyringAvailableWithWorkingBackend(t *testing.T) {
	keyring.MockInit()
	if err := KeyringAvailable(); err != nil {
		t.Fatalf("mock backend answers not-found; must count as available: %v", err)
	}
}

// --- encrypted file backend --------------------------------------------------------------------

func fileStore(t *testing.T, pass string) *FileStore {
	t.Helper()
	return &FileStore{
		Path:       filepath.Join(t.TempDir(), "state", "secrets.age"),
		Passphrase: func() (string, error) { return pass, nil },
		WorkFactor: 10, // fast scrypt for tests only
	}
}

func TestFileStoreRoundTripAndAtRestProperties(t *testing.T) {
	f := fileStore(t, "correct horse battery staple")
	if _, err := f.Get("a/b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty store: %v", err)
	}
	if err := f.Set("alpaca/api_key", []byte("FAKE-VALUE-ABC")); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("alpaca/secret_key", []byte("FAKE-VALUE-XYZ")); err != nil {
		t.Fatal(err)
	}
	got, err := f.Get("alpaca/api_key")
	if err != nil || string(got) != "FAKE-VALUE-ABC" {
		t.Fatalf("got %q err=%v", got, err)
	}
	blob, _ := os.ReadFile(f.Path)
	if bytes.Contains(blob, []byte("FAKE-VALUE")) || bytes.Contains(blob, []byte("alpaca")) {
		t.Fatal("plaintext (value or ref name) found in the encrypted file")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(f.Path); st.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %v, want 0600", st.Mode().Perm())
		}
	}
	refs, _ := f.Refs()
	if len(refs) != 2 || refs[0] != "alpaca/api_key" {
		t.Fatalf("refs = %v", refs)
	}
	if err := f.Delete("alpaca/api_key"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("alpaca/api_key"); !errors.Is(err, ErrNotFound) {
		t.Fatal("still present after delete")
	}
	if got, _ := f.Get("alpaca/secret_key"); string(got) != "FAKE-VALUE-XYZ" {
		t.Fatal("unrelated secret lost")
	}
}

func TestFileStoreWrongPassphraseAndTamperingFailClosed(t *testing.T) {
	f := fileStore(t, "correct horse battery staple")
	_ = f.Set("a/b", []byte("FAKE-VALUE"))

	wrong := &FileStore{Path: f.Path, Passphrase: func() (string, error) { return "totally wrong passphrase", nil }, WorkFactor: 10}
	if _, err := wrong.Get("a/b"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	// A failed read must not destroy the file, and a Set with the wrong passphrase must not overwrite it.
	if err := wrong.Set("c/d", []byte("v")); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("set with wrong passphrase: %v", err)
	}
	if got, err := f.Get("a/b"); err != nil || string(got) != "FAKE-VALUE" {
		t.Fatalf("file damaged by a failed attempt: %q %v", got, err)
	}

	blob, _ := os.ReadFile(f.Path)
	blob[len(blob)-5] ^= 0xff // flip a bit in the authenticated ciphertext
	_ = os.WriteFile(f.Path, blob, 0o600)
	if _, err := f.Get("a/b"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("tampered file must fail authentication, got %v", err)
	}
}

func TestFileStoreWeakPassphraseIsRejected(t *testing.T) {
	f := fileStore(t, "short")
	if err := f.Set("a/b", []byte("v")); !errors.Is(err, ErrWeakPassphrase) {
		t.Fatalf("want ErrWeakPassphrase, got %v", err)
	}
	if _, err := os.Stat(f.Path); err == nil {
		t.Fatal("no file may be written with a weak passphrase")
	}
}

func TestFileStorePassphraseErrorPropagates(t *testing.T) {
	f := fileStore(t, "correct horse battery staple")
	_ = f.Set("a/b", []byte("v"))
	f.Passphrase = func() (string, error) { return "", errors.New("no terminal") }
	if _, err := f.Get("a/b"); err == nil || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("got %v", err)
	}
}

// --- backend selection ---------------------------------------------------------------------------

func TestOpenPrefersKeychainThenFallsBackToFile(t *testing.T) {
	pass := func() (string, error) { return "correct horse battery staple", nil }
	path := filepath.Join(t.TempDir(), "secrets.age")

	keyring.MockInit()
	s, err := Open(OpenOptions{FilePath: path, Passphrase: pass})
	if err != nil || s.Kind() != "keychain" {
		t.Fatalf("want keychain, got %v %v", s, err)
	}

	keyring.MockInitWithError(errors.New("no secret service"))
	s, err = Open(OpenOptions{FilePath: path, Passphrase: pass, WorkFactor: 10})
	if err != nil || s.Kind() != "encrypted-file" {
		t.Fatalf("want encrypted-file fallback, got %v %v", s, err)
	}

	if _, err := Open(OpenOptions{}); err == nil || !strings.Contains(err.Error(), "no usable secret store") {
		t.Fatalf("no backend at all must be an explicit error, got %v", err)
	}
}

func TestConcurrentWritersDoNotLoseUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.age")
	newStore := func() *FileStore {
		return &FileStore{Path: path, WorkFactor: 10, Passphrase: func() (string, error) { return "correct horse battery staple", nil }}
	}
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- newStore().Set(fmt.Sprintf("app/key%d", i), []byte("v"))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	refs, err := newStore().Refs()
	if err != nil || len(refs) != n {
		t.Fatalf("lost updates: got %d of %d refs (%v)", len(refs), n, err)
	}
}
