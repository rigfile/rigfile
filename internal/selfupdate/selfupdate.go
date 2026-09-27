// Package selfupdate replaces the running rigfile with a newer release, but only after verifying it (docs/sharing.md §9):
// the release's SHA256SUMS must carry a valid minisign signature from the embedded key, and its trusted comment must
// name the version being installed (so old signed checksums cannot be replayed under a newer tag); then the archive's
// SHA-256 must match the signed list. Anything else is refused and the installed binary is left untouched.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rigfile/rigfile/internal/minisign"
)

// Repo is where releases are published.
const Repo = "rigfile/rigfile"

// PublicKey is the minisign public key releases are signed with. It is empty until the owner generates the release
// key (docs/stage-4-owner-checks.md); a build without it refuses to self-update instead of trusting anything.
var PublicKey = ""

// ErrNoKey means this build carries no release signing key.
var ErrNoKey = errors.New("this build has no release signing key, so it cannot verify an update; releases are not signed yet (see docs/stage-4-owner-checks.md)")

// Options describes one update attempt. The zero values of the URL fields mean the real GitHub endpoints.
type Options struct {
	Current        string // running version, e.g. "1.2.0"
	GOOS, GOARCH   string
	Exe            string // the binary to replace
	PubKey         string // "" = PublicKey
	Client         *http.Client
	APIURL         string   // default https://api.github.com
	DownloadBase   string   // default https://github.com/<Repo>/releases/download ; the tag is appended
	AllowedHosts   []string // extra redirect hosts (tests)
	CheckOnly      bool     // verify and report, replace nothing
	AllowDowngrade bool
}

// Result says what happened.
type Result struct {
	Latest   string
	Updated  bool
	UpToDate bool
}

const maxSmall, maxArchive = 1 << 20, 200 << 20

// Run performs the update.
func Run(ctx context.Context, o Options) (*Result, error) {
	key := o.PubKey
	if key == "" {
		key = PublicKey
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrNoKey
	}
	pk, err := minisign.ParsePublicKey(key)
	if err != nil {
		return nil, err
	}
	if o.APIURL == "" {
		o.APIURL = "https://api.github.com"
	}
	if o.DownloadBase == "" {
		o.DownloadBase = "https://github.com/" + Repo + "/releases/download"
	}
	f := &fetcher{client: o.Client, extra: o.AllowedHosts}

	body, err := f.get(ctx, o.APIURL+"/repos/"+Repo+"/releases/latest", maxSmall)
	if err != nil {
		return nil, err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.Tag == "" || strings.ContainsAny(rel.Tag, "/\\ ?#") {
		return nil, errors.New("selfupdate: could not read the latest release")
	}
	res := &Result{Latest: rel.Tag}
	cmp := compareVersions(rel.Tag, o.Current)
	switch {
	case cmp == 0:
		res.UpToDate = true
		return res, nil
	case cmp < 0 && !o.AllowDowngrade:
		return nil, fmt.Errorf("selfupdate: the latest release (%s) is older than this build (%s); refusing to downgrade", rel.Tag, o.Current)
	}

	base := strings.TrimRight(o.DownloadBase, "/") + "/" + rel.Tag
	sums, err := f.get(ctx, base+"/SHA256SUMS", maxSmall)
	if err != nil {
		return nil, err
	}
	sig, err := f.get(ctx, base+"/SHA256SUMS.minisig", maxSmall)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: the release is not signed: %w", err)
	}
	if err := minisign.Verify(pk, sums, string(sig)); err != nil {
		return nil, fmt.Errorf("selfupdate: refusing the release: %w", err)
	}
	parsed, err := minisign.ParseSignature(string(sig))
	if err != nil || !strings.Contains(parsed.TrustedComment, rel.Tag) {
		return nil, fmt.Errorf("selfupdate: the signed checksums are not for %s (they were signed for: %q); refusing", rel.Tag, parsed.TrustedComment)
	}

	goos, goarch := o.GOOS, o.GOARCH
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	archive := fmt.Sprintf("rigfile_%s_%s_%s%s", strings.TrimPrefix(rel.Tag, "v"), goos, goarch, ext)
	want := ""
	for _, l := range strings.Split(string(sums), "\n") {
		if fs := strings.Fields(l); len(fs) == 2 && strings.TrimPrefix(fs[1], "*") == archive {
			want = strings.ToLower(fs[0])
		}
	}
	if want == "" {
		return nil, fmt.Errorf("selfupdate: %s has no build for %s/%s", rel.Tag, goos, goarch)
	}
	if o.CheckOnly {
		return res, nil
	}
	data, err := f.get(ctx, base+"/"+archive, maxArchive)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return nil, errors.New("selfupdate: the downloaded archive does not match the signed checksum; refusing")
	}
	bin, err := extractBinary(data, archive)
	if err != nil {
		return nil, err
	}
	if err := replace(o.Exe, bin); err != nil {
		return nil, err
	}
	res.Updated = true
	return res, nil
}

// replace swaps the executable: new file next to it, old one moved aside, new one moved in; the old one is restored
// if the last step fails. (Windows allows renaming a running executable but not overwriting it.)
func replace(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".rigfile-new-*")
	if err != nil {
		return fmt.Errorf("selfupdate: cannot write next to %s (is it in a protected directory?): %w", exe, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return err
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("selfupdate: cannot move the current binary aside: %w", err)
	}
	if err := os.Rename(name, exe); err != nil {
		_ = os.Rename(old, exe)
		return fmt.Errorf("selfupdate: cannot put the new binary in place (the old one was restored): %w", err)
	}
	return nil
}

func extractBinary(data []byte, archive string) ([]byte, error) {
	want := "rigfile"
	if strings.HasSuffix(archive, ".zip") {
		want = "rigfile.exe"
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == want && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return readCapped(rc)
			}
		}
		return nil, fmt.Errorf("selfupdate: %s does not contain %s", archive, want)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("selfupdate: %s does not contain %s", archive, want)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == want {
			return readCapped(tr)
		}
	}
}

func readCapped(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, 300<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 300<<20 {
		return nil, errors.New("selfupdate: the binary is implausibly large")
	}
	return b, nil
}

// compareVersions compares "v1.2.3" style versions numerically (a pre-release sorts below its release; unparsable
// input sorts lowest so it is never mistaken for newer).
func compareVersions(a, b string) int {
	pa, oka := parse(a)
	pb, okb := parse(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa[3] == pb[3]:
		return 0
	case pa[3] > pb[3]: // a is a pre-release (1), b is not (0)
		return -1
	}
	return 1
}

func parse(v string) (out [4]int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	pre := 0
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		if v[i] == '-' {
			pre = 1
		}
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	out[3] = pre
	return out, true
}

// fetcher does HTTPS GETs with a size cap and a redirect policy: HTTPS only, to GitHub's own hosts.
type fetcher struct {
	client *http.Client
	extra  []string
}

func (f *fetcher) hostOK(u *url.URL) bool {
	h := strings.ToLower(u.Host)
	if u.Scheme != "https" {
		return false
	}
	for _, e := range f.extra {
		if strings.EqualFold(h, e) {
			return true
		}
	}
	switch {
	case h == "github.com", h == "api.github.com", strings.HasSuffix(h, ".githubusercontent.com"):
		return true
	}
	return false
}

func (f *fetcher) get(ctx context.Context, raw string, limit int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || !f.hostOK(u) {
		return nil, fmt.Errorf("selfupdate: refusing to contact %s", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rigfile-selfupdate")
	c := http.Client{Timeout: 5 * time.Minute}
	if f.client != nil {
		c = *f.client
	}
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || !f.hostOK(r.URL) {
			return fmt.Errorf("selfupdate: refusing a redirect to %s", r.URL.Host)
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("selfupdate: %s answered %s", u.Path, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("selfupdate: %s is larger than %d bytes", u.Path, limit)
	}
	return b, nil
}
