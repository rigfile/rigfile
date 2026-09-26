package registry

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/registry/blob"
)

// Config is the service configuration. It comes from environment variables (never flags, so secrets do not appear in
// process listings): see ConfigFromEnv.
type Config struct {
	Listen       string // RIGFILE_REGISTRY_LISTEN, default 127.0.0.1:8080
	PublicURL    string // RIGFILE_REGISTRY_PUBLIC_URL, e.g. https://registry.example.org (scheme decides Secure cookies and HSTS)
	DatabaseURL  string // RIGFILE_REGISTRY_DATABASE_URL
	Blob         string // RIGFILE_REGISTRY_BLOB: "fs:/var/lib/rigfile/blobs" or "s3"
	S3           blob.S3Config
	GitHubID     string   // RIGFILE_REGISTRY_GITHUB_CLIENT_ID
	GitHubSecret string   // RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET (or ..._FILE)
	GitHubWeb    string   // default https://github.com
	GitHubAPI    string   // default https://api.github.com
	Admins       []string // RIGFILE_REGISTRY_ADMINS: comma-separated GitHub logins
	TrustProxy   bool     // RIGFILE_REGISTRY_TRUST_PROXY=1: take the client address from the last X-Forwarded-For entry

	SessionTTL     time.Duration
	TokenTTL       time.Duration
	MaxUpload      int64 // compressed bytes
	ScanWorkers    int
	DeviceInterval int // seconds between device-flow polls (default 5)
	// SigstoreRoot is a trusted_root.json to verify signatures against ('' = fetch the public-good root through TUF on first use).
	SigstoreRoot string
	// PopularStars: a public rig at or above this many stars needs every new version signed by the publisher's own GitHub
	// Actions identity and a verified publisher (0 = off; docs/trust.md §5).
	PopularStars int
}

// Origin returns scheme://host of the public URL.
func (c Config) Origin() string {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Secure reports whether the service is served over HTTPS.
func (c Config) Secure() bool { return strings.HasPrefix(c.PublicURL, "https://") }

// IsAdmin reports whether login is in the configured admin list.
func (c Config) IsAdmin(login string) bool {
	for _, a := range c.Admins {
		if strings.EqualFold(a, login) {
			return true
		}
	}
	return false
}

// ConfigFromEnv reads the configuration. getenv is os.Getenv in production; readFile reads *_FILE variables.
func ConfigFromEnv(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	c := Config{
		Listen: def(getenv("RIGFILE_REGISTRY_LISTEN"), "127.0.0.1:8080"), PublicURL: strings.TrimRight(getenv("RIGFILE_REGISTRY_PUBLIC_URL"), "/"),
		DatabaseURL: getenv("RIGFILE_REGISTRY_DATABASE_URL"), Blob: getenv("RIGFILE_REGISTRY_BLOB"),
		GitHubID: getenv("RIGFILE_REGISTRY_GITHUB_CLIENT_ID"), GitHubSecret: getenv("RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET"),
		GitHubWeb: def(getenv("RIGFILE_REGISTRY_GITHUB_WEB"), "https://github.com"), GitHubAPI: def(getenv("RIGFILE_REGISTRY_GITHUB_API"), "https://api.github.com"),
		TrustProxy: getenv("RIGFILE_REGISTRY_TRUST_PROXY") == "1",
		SessionTTL: 7 * 24 * time.Hour, TokenTTL: 30 * 24 * time.Hour, MaxUpload: 20 << 20, ScanWorkers: 2, DeviceInterval: 5,
	}
	if f := getenv("RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET_FILE"); f != "" && readFile != nil {
		b, err := readFile(f)
		if err != nil {
			return c, fmt.Errorf("registry: reading the GitHub client secret file: %w", err)
		}
		c.GitHubSecret = strings.TrimSpace(string(b))
	}
	for _, a := range strings.Split(getenv("RIGFILE_REGISTRY_ADMINS"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			c.Admins = append(c.Admins, strings.ToLower(a))
		}
	}
	if v := getenv("RIGFILE_REGISTRY_MAX_UPLOAD_MB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return c, errors.New("registry: RIGFILE_REGISTRY_MAX_UPLOAD_MB must be 1-200")
		}
		c.MaxUpload = int64(n) << 20
	}
	c.SigstoreRoot = getenv("RIGFILE_REGISTRY_SIGSTORE_ROOT")
	if v := getenv("RIGFILE_REGISTRY_POPULAR_STARS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c, errors.New("registry: RIGFILE_REGISTRY_POPULAR_STARS must be a number, 0 to disable")
		}
		c.PopularStars = n
	}
	if c.Blob == "s3" {
		c.S3 = blob.S3Config{Endpoint: getenv("RIGFILE_REGISTRY_S3_ENDPOINT"), Bucket: getenv("RIGFILE_REGISTRY_S3_BUCKET"),
			AccessKey: getenv("RIGFILE_REGISTRY_S3_ACCESS_KEY"), SecretKey: getenv("RIGFILE_REGISTRY_S3_SECRET_KEY"),
			Secure: getenv("RIGFILE_REGISTRY_S3_INSECURE") != "1", Region: def(getenv("RIGFILE_REGISTRY_S3_REGION"), "auto"), Prefix: "blobs/sha256/"}
	}
	return c, c.Validate()
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// Validate refuses a configuration that would run insecurely or not at all.
func (c Config) Validate() error {
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "" {
		return errors.New("registry: RIGFILE_REGISTRY_PUBLIC_URL must be an origin such as https://registry.example.org")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return errors.New("registry: RIGFILE_REGISTRY_PUBLIC_URL must be https (plain http is only allowed for localhost)")
	}
	if c.DatabaseURL == "" {
		return errors.New("registry: RIGFILE_REGISTRY_DATABASE_URL is required")
	}
	if c.Blob == "" {
		return errors.New("registry: RIGFILE_REGISTRY_BLOB is required (fs:/path or s3)")
	}
	if c.GitHubID == "" || c.GitHubSecret == "" {
		return errors.New("registry: the GitHub OAuth client id and secret are required")
	}
	return nil
}
