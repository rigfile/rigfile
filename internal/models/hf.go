package models

import (
	"context"
	"crypto/sha1" //nolint:gosec // git blob ids are SHA-1; used only to verify files the API describes, never for security decisions alone
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// HFFile is one file of a Hugging Face repository at a revision, as the API describes it.
type HFFile struct {
	Path    string
	Size    int64
	SHA256  string // large files (LFS): the content hash
	GitSHA1 string // small files: the git blob id
}

// HF downloads a model at an exact revision and verifies every byte against what the API reported for that revision. The
// revision is a commit, so the listing and the bytes cannot drift between the check and the download.
type HF struct {
	Base  string // default https://huggingface.co
	HTTP  *http.Client
	Token func() string // optional read token (private repos); sent only to Base's own host
	// RepoPrefix is put before <org>/<name> in download URLs ("" for the real service; tests use a prefix to keep routes apart).
	RepoPrefix string
}

const maxRepoBytes = 200 << 30

var (
	pickleExt = map[string]bool{".bin": true, ".pt": true, ".pth": true, ".pkl": true, ".pickle": true, ".ckpt": true, ".npy": true, ".npz": true}
	commitRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func (h *HF) base() string {
	if h.Base != "" {
		return strings.TrimRight(h.Base, "/")
	}
	return "https://huggingface.co"
}

func (h *HF) client() *http.Client {
	c := h.HTTP
	if c == nil {
		c = &http.Client{Timeout: 0} // downloads are large; per-request contexts bound them
	}
	cc := *c
	base, _ := url.Parse(h.base())
	cc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 6 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && !(base.Scheme == "http" && req.URL.Hostname() == base.Hostname()) {
			return fmt.Errorf("refusing a redirect to %s (not https)", req.URL.Redacted())
		}
		if req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization") // Go keeps it for sibling subdomains and other ports; the token belongs to one host
		}
		return nil
	}
	return &cc
}

func (h *HF) get(ctx context.Context, u string, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rigfile")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if h.Token != nil {
		if t := h.Token(); t != "" {
			if pu, _ := url.Parse(u); pu != nil && pu.Host == mustHost(h.base()) {
				req.Header.Set("Authorization", "Bearer "+t) // Go drops it on a redirect to another host
			}
		}
	}
	return h.client().Do(req)
}

func mustHost(s string) string { u, _ := url.Parse(s); return u.Host }

// List returns every file of repo at the commit, following the API's pagination.
func (h *HF) List(ctx context.Context, repo, rev string) ([]HFFile, error) {
	if !hfRepo.MatchString(repo) {
		return nil, fmt.Errorf("%q is not a Hugging Face repo id", repo)
	}
	if !commitRe.MatchString(rev) {
		return nil, errors.New("the revision must be a 40-character commit")
	}
	next := fmt.Sprintf("%s/api/models/%s/tree/%s?recursive=true", h.base(), repo, rev)
	var files []HFFile
	for pages := 0; next != "" && pages < 50; pages++ {
		resp, err := h.get(ctx, next, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("listing %s@%s: %s", repo, rev[:12], resp.Status)
		}
		var page []struct {
			Type string `json:"type"`
			OID  string `json:"oid"`
			Size int64  `json:"size"`
			Path string `json:"path"`
			LFS  *struct {
				OID  string `json:"oid"`
				Size int64  `json:"size"`
			} `json:"lfs"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&page)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", repo, err)
		}
		for _, e := range page {
			if e.Type != "file" {
				continue
			}
			f := HFFile{Path: e.Path, Size: e.Size, GitSHA1: e.OID}
			if e.LFS != nil {
				f.SHA256, f.Size, f.GitSHA1 = e.LFS.OID, e.LFS.Size, ""
			}
			files = append(files, f)
		}
		next = nextLink(link)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s@%s lists no files", repo, rev[:12])
	}
	return files, nil
}

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(h string) string {
	if m := linkNext.FindStringSubmatch(h); m != nil {
		return m[1]
	}
	return ""
}

// CheckSnapshot refuses a listing that could run code or carries no usable weights: pickle-based files, Python source (a
// `trust_remote_code` model), unsafe paths, or none of the format the rig declared.
func CheckSnapshot(files []HFFile, format string) error {
	var total int64
	weights := 0
	for _, f := range files {
		clean := path.Clean(f.Path)
		if f.Path == "" || clean != f.Path || !filepath.IsLocal(filepath.FromSlash(clean)) || strings.HasPrefix(clean, ".git") {
			return fmt.Errorf("the repository lists an unsafe file path %q", f.Path)
		}
		ext := strings.ToLower(path.Ext(f.Path))
		switch {
		case pickleExt[ext]:
			return fmt.Errorf("the repository contains %s, a pickle-based format that can run code when loaded; refusing", f.Path)
		case ext == ".py":
			return fmt.Errorf("the repository contains Python source (%s): a trust_remote_code model, refused unless you opt in for this model", f.Path)
		}
		if f.SHA256 == "" && f.GitSHA1 == "" {
			return fmt.Errorf("%s has no hash to verify against", f.Path)
		}
		if f.Size < 0 {
			return fmt.Errorf("%s has an invalid size", f.Path)
		}
		total += f.Size
		if (format == "gguf" && ext == ".gguf") || ((format == "safetensors" || format == "mlx-safetensors" || format == "") && ext == ".safetensors") {
			weights++
		}
	}
	if total > maxRepoBytes {
		return errors.New("the repository is implausibly large; refusing")
	}
	if weights == 0 {
		return fmt.Errorf("the repository has no %s weights", orDefault(format, "safetensors"))
	}
	return nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// gitBlobSHA1 is git's object id for file content, which is what the API reports for small (non-LFS) files.
func gitBlobSHA1(content io.Reader, size int64) (string, error) {
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "blob %d\x00", size)
	if _, err := io.Copy(h, content); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyFile(p string, f HFFile) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if st.Size() != f.Size {
		return fmt.Errorf("size %d, expected %d", st.Size(), f.Size)
	}
	fh, err := os.Open(p)
	if err != nil {
		return err
	}
	defer fh.Close()
	if f.SHA256 != "" {
		s := sha256.New()
		if _, err := io.Copy(s, fh); err != nil {
			return err
		}
		if got := hex.EncodeToString(s.Sum(nil)); got != f.SHA256 {
			return fmt.Errorf("sha256 %s…, expected %s…", got[:12], f.SHA256[:12])
		}
		return nil
	}
	got, err := gitBlobSHA1(fh, f.Size)
	if err != nil {
		return err
	}
	if got != f.GitSHA1 {
		return fmt.Errorf("git blob id %s…, expected %s…", got[:12], f.GitSHA1[:12])
	}
	return nil
}

// Progress reports bytes done of total.
type Progress func(done, total int64)

// Fetch downloads a verified snapshot of repo@rev into dest (created if needed) and returns the files. Files already
// present and matching are skipped; interrupted downloads resume; nothing is left at its final name unless it verified.
func (h *HF) Fetch(ctx context.Context, repo, rev, format, dest string, progress Progress) ([]HFFile, error) {
	files, err := h.List(ctx, repo, rev)
	if err != nil {
		return nil, err
	}
	if err := CheckSnapshot(files, format); err != nil {
		return nil, err
	}
	var total, done int64
	for _, f := range files {
		total += f.Size
	}
	// config.json first: a model that asks for remote code says so there
	ordered := append([]HFFile(nil), files...)
	for i, f := range ordered {
		if f.Path == "config.json" {
			ordered[0], ordered[i] = ordered[i], ordered[0]
		}
	}
	for _, f := range ordered {
		final := filepath.Join(dest, filepath.FromSlash(f.Path))
		if verifyFile(final, f) == nil {
			done += f.Size
			if progress != nil {
				progress(done, total)
			}
			continue
		}
		if err := h.download(ctx, repo, rev, f, final, func(n int64) {
			if progress != nil {
				progress(done+n, total)
			}
		}); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		done += f.Size
		if f.Path == "config.json" {
			if err := refuseRemoteCode(final); err != nil {
				_ = os.Remove(final)
				return nil, err
			}
		}
	}
	return files, nil
}

// refuseRemoteCode rejects a config that asks the loader to import code from the repository (`auto_map`).
func refuseRemoteCode(configPath string) error {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(b, &cfg) != nil {
		return nil // not JSON we understand: no claim either way (the .py check already ran)
	}
	if _, ok := cfg["auto_map"]; ok {
		return errors.New("config.json has auto_map: the model wants to run code from its repository (trust_remote_code); refused unless you opt in for this model")
	}
	return nil
}

func (h *HF) download(ctx context.Context, repo, rev string, f HFFile, final string, onBytes func(int64)) error {
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	part := final + ".part"
	var have int64
	hasher := newHasher(f)
	if st, err := os.Stat(part); err == nil && st.Size() > 0 && st.Size() < f.Size {
		if pf, err := os.Open(part); err == nil {
			n, cerr := io.Copy(hasher, pf)
			pf.Close()
			if cerr == nil {
				have = n
			}
		}
	}
	if have == 0 {
		hasher = newHasher(f)
	}
	u := fmt.Sprintf("%s%s/%s/resolve/%s/%s", h.base(), h.RepoPrefix, repo, rev, escapePath(f.Path))
	hdr := map[string]string{}
	if have > 0 {
		hdr["Range"] = fmt.Sprintf("bytes=%d-", have)
	}
	resp, err := h.get(ctx, u, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		have, hasher = 0, newHasher(f) // the server sent the whole file (no resume): start over
		flags |= os.O_TRUNC
	default:
		return fmt.Errorf("download: %s", resp.Status)
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	written := have
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			return err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if written+int64(n) > f.Size {
				out.Close()
				_ = os.Remove(part)
				return fmt.Errorf("the server sent more than the %d bytes listed", f.Size)
			}
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			hasher.Write(buf[:n])
			written += int64(n)
			onBytes(written)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr // the .part file stays, so a retry resumes
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	if written != f.Size {
		return fmt.Errorf("received %d bytes, expected %d (run again to resume)", written, f.Size)
	}
	if err := hasher.check(f, written); err != nil {
		_ = os.Remove(part) // corrupt: never resume from it
		return err
	}
	return os.Rename(part, final)
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// verifier hashes what is downloaded. For git-blob verification the size prefix is fixed up front.
type verifier struct {
	h      hash.Hash
	sha256 bool
}

func newHasher(f HFFile) *verifier {
	if f.SHA256 != "" {
		return &verifier{h: sha256.New(), sha256: true}
	}
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "blob %d\x00", f.Size)
	return &verifier{h: h}
}

func (v *verifier) Write(p []byte) (int, error) { return v.h.Write(p) }

func (v *verifier) check(f HFFile, _ int64) error {
	got := hex.EncodeToString(v.h.Sum(nil))
	want := f.GitSHA1
	if v.sha256 {
		want = f.SHA256
	}
	if got != want {
		return fmt.Errorf("verification failed: content hash %s… does not match the %s… the repository lists; the file was discarded", got[:12], want[:12])
	}
	return nil
}
