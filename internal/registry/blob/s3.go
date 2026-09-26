package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures an S3-compatible bucket (AWS S3, Cloudflare R2, MinIO).
type S3Config struct {
	Endpoint  string // host[:port], no scheme (e.g. "<account>.r2.cloudflarestorage.com")
	Bucket    string
	AccessKey string
	SecretKey string
	Secure    bool   // https
	Region    string // "auto" for R2
	Prefix    string // key prefix, e.g. "blobs/sha256/"
	// Transport overrides the HTTP transport (tests; nil = the default).
	Transport http.RoundTripper
}

// S3 keeps blobs in an S3-compatible bucket under Prefix+<aa>/<sha>. Tarballs are small (capped at upload), so Put
// buffers to memory to know the hash before choosing the key.
type S3 struct {
	c   *minio.Client
	cfg S3Config
}

// OpenS3 connects (lazily: no network call is made here).
func OpenS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("blob: s3 needs an endpoint and a bucket")
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: cfg.Secure, Region: cfg.Region, Transport: cfg.Transport})
	if err != nil {
		return nil, err
	}
	return &S3{c: c, cfg: cfg}, nil
}

func (s *S3) key(sha string) string { return s.cfg.Prefix + sha[:2] + "/" + sha }

// Put implements Store.
func (s *S3) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	b, err := io.ReadAll(io.LimitReader(&ctxReader{ctx, r}, max+1))
	if err != nil {
		return "", 0, err
	}
	if int64(len(b)) > max {
		return "", 0, ErrTooLarge
	}
	sum := sha256.Sum256(b)
	sha := hex.EncodeToString(sum[:])
	if ok, err := s.Has(ctx, sha); err != nil {
		return "", 0, err
	} else if ok {
		return sha, int64(len(b)), nil
	}
	if _, err := s.c.PutObject(ctx, s.cfg.Bucket, s.key(sha), bytes.NewReader(b), int64(len(b)), minio.PutObjectOptions{ContentType: "application/gzip"}); err != nil {
		return "", 0, fmt.Errorf("blob: s3 put: %w", err)
	}
	return sha, int64(len(b)), nil
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, sha string) (io.ReadCloser, int64, error) {
	if !ValidKey(sha) {
		return nil, 0, ErrNotFound
	}
	o, err := s.c.GetObject(ctx, s.cfg.Bucket, s.key(sha), minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	st, err := o.Stat()
	if err != nil {
		o.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return o, st.Size, nil
}

// Has implements Store.
func (s *S3) Has(ctx context.Context, sha string) (bool, error) {
	if !ValidKey(sha) {
		return false, nil
	}
	_, err := s.c.StatObject(ctx, s.cfg.Bucket, s.key(sha), minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" || minio.ToErrorResponse(err).StatusCode == 404 {
		return false, nil
	}
	return false, err
}
