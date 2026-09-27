package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// contract is what every backend must do.
func contract(t *testing.T, s Store) {
	ctx := context.Background()
	data := []byte("hello rig tarball")
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	sha, n, err := s.Put(ctx, bytes.NewReader(data), 1<<20)
	if err != nil || sha != want || n != int64(len(data)) {
		t.Fatalf("put: %s %d %v", sha, n, err)
	}
	// the same bytes again are a no-op that yields the same key
	if sha2, _, err := s.Put(ctx, bytes.NewReader(data), 1<<20); err != nil || sha2 != want {
		t.Fatalf("second put: %v", err)
	}
	if ok, err := s.Has(ctx, want); err != nil || !ok {
		t.Fatalf("has: %v %v", ok, err)
	}
	rc, size, err := s.Get(ctx, want)
	if err != nil || size != int64(len(data)) {
		t.Fatalf("get: size=%d want %d err=%v", size, len(data), err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("content differs")
	}
	// missing, malformed and traversal-looking keys
	for _, k := range []string{strings.Repeat("0", 64), "short", "../../etc/passwd", strings.ToUpper(want), want + "00"} {
		if _, _, err := s.Get(ctx, k); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", k, err)
		}
		if ok, _ := s.Has(ctx, k); ok {
			t.Errorf("Has(%q)", k)
		}
	}
	// the size cap
	if _, _, err := s.Put(ctx, bytes.NewReader(make([]byte, 101)), 100); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, _, err := s.Put(ctx, bytes.NewReader(make([]byte, 100)), 100); err != nil {
		t.Errorf("exactly at the cap: %v", err)
	}
	// a cancelled context stops a put
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.Put(cctx, bytes.NewReader(data), 1<<20); err == nil {
		t.Error("cancelled context")
	}
	// concurrent identical puts all succeed with one key
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sha, _, err := s.Put(ctx, bytes.NewReader([]byte("concurrent")), 1<<20)
			if err == nil && sha == "" {
				err = errors.New("empty key")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	// Delete: removes a blob (owner decision 2026-09-26: a rejected upload's archive is deleted at once), is a no-op on
	// one that never existed, and idempotent.
	if err := s.Delete(ctx, want); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok, err := s.Has(ctx, want); err != nil || ok {
		t.Fatalf("has after delete: %v %v", ok, err)
	}
	if err := s.Delete(ctx, want); err != nil {
		t.Fatalf("delete again: %v", err)
	}
	if err := s.Delete(ctx, strings.Repeat("0", 64)); err != nil {
		t.Fatalf("delete something never put: %v", err)
	}
	for _, k := range []string{"short", "../../etc/passwd"} {
		if err := s.Delete(ctx, k); err != nil {
			t.Errorf("delete(%q) must be a quiet no-op, not an error: %v", k, err)
		}
	}
}

func TestFS(t *testing.T) { contract(t, FS{Root: t.TempDir()}) }

func TestOpen(t *testing.T) {
	if _, err := Open("fs:" + t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := Open("ftp://x"); err == nil {
		t.Fatal("unknown backend")
	}
}

// fakeS3 serves path-style PUT/GET/HEAD for one bucket, enough for the client the S3 backend uses.
func fakeS3(t *testing.T) (S3Config, *int) {
	objs := map[string][]byte{}
	var mu sync.Mutex
	auth := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			auth++
		}
		key := strings.TrimPrefix(r.URL.Path, "/testbucket/")
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			objs[key] = b
			w.Header().Set("ETag", `"x"`)
		case http.MethodGet, http.MethodHead:
			b, ok := objs[key]
			if !ok {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`))
				return
			}
			w.Header().Set("Content-Length", itoa(len(b)))
			w.Header().Set("ETag", `"x"`)
			w.Header().Set("Last-Modified", "Sat, 26 Sep 2026 12:00:00 GMT")
			if r.Method == http.MethodGet {
				_, _ = w.Write(b)
			}
		case http.MethodDelete:
			delete(objs, key)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(srv.Close)
	return S3Config{Endpoint: strings.TrimPrefix(srv.URL, "https://"), Bucket: "testbucket", AccessKey: "k", SecretKey: "s", Region: "auto", Prefix: "blobs/sha256/",
		Secure: true, Transport: srv.Client().Transport}, &auth
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestS3(t *testing.T) {
	cfg, signed := fakeS3(t)
	s, err := OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	contract(t, s)
	if *signed == 0 {
		t.Fatal("requests must be signed (AWS Signature V4)")
	}
	if _, err := OpenS3(S3Config{}); err == nil {
		t.Fatal("an s3 config needs an endpoint and a bucket")
	}
}
