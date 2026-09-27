// Package blob stores immutable, content-addressed blobs (rig tarballs). The key is the SHA-256 of the content, so a
// blob can never change: putting the same bytes twice is a no-op, and a reader can always re-check what it got.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// ErrNotFound means the blob does not exist.
var ErrNotFound = errors.New("blob: not found")

var keyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store is a content-addressed blob store.
type Store interface {
	// Put stores everything r yields and returns its SHA-256 and size. At most max bytes are accepted (ErrTooLarge).
	Put(ctx context.Context, r io.Reader, max int64) (sha string, size int64, err error)
	// Get opens a blob; the caller closes it.
	Get(ctx context.Context, sha string) (io.ReadCloser, int64, error)
	// Has reports whether the blob exists.
	Has(ctx context.Context, sha string) (bool, error)
	// Delete removes a blob. Deleting one that does not exist is not an error (the caller need not check Has first).
	Delete(ctx context.Context, sha string) error
}

// ErrTooLarge means the input exceeded the limit.
var ErrTooLarge = errors.New("blob: too large")

// ValidKey reports whether s is a well-formed blob key (64 lowercase hex characters).
func ValidKey(s string) bool { return keyRe.MatchString(s) }

// FS keeps blobs in a directory tree: <root>/<aa>/<sha>. Writes go to a temporary file first and are renamed into
// place, so a reader never sees a partial blob.
type FS struct{ Root string }

func (f FS) path(sha string) string { return filepath.Join(f.Root, sha[:2], sha) }

// Put implements Store.
func (f FS) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	if err := os.MkdirAll(f.Root, 0o700); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(f.Root, ".put-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(&ctxReader{ctx, r}, max+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	if n > max {
		return "", 0, ErrTooLarge
	}
	sha := hex.EncodeToString(h.Sum(nil))
	dst := f.path(sha)
	if _, err := os.Stat(dst); err == nil {
		return sha, n, nil // already there: same bytes by construction
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		if _, serr := os.Stat(dst); serr == nil {
			return sha, n, nil // a concurrent identical put won the race
		}
		return "", 0, err
	}
	return sha, n, nil
}

// Get implements Store.
func (f FS) Get(_ context.Context, sha string) (io.ReadCloser, int64, error) {
	if !ValidKey(sha) {
		return nil, 0, ErrNotFound
	}
	fh, err := os.Open(f.path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	st, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, 0, err
	}
	return fh, st.Size(), nil
}

// Delete implements Store.
func (f FS) Delete(_ context.Context, sha string) error {
	if !ValidKey(sha) {
		return nil
	}
	if err := os.Remove(f.path(sha)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Has implements Store.
func (f FS) Has(_ context.Context, sha string) (bool, error) {
	if !ValidKey(sha) {
		return false, nil
	}
	_, err := os.Stat(f.path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Open parses a blob backend spec: "fs:/path/to/dir" (S3 is opened by OpenS3 with its own settings).
func Open(spec string) (Store, error) {
	if len(spec) > 3 && spec[:3] == "fs:" {
		return FS{Root: spec[3:]}, nil
	}
	return nil, fmt.Errorf("blob: unknown backend %q (use fs:/path, or configure S3)", spec)
}
