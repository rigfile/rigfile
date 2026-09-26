package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxDownload caps an archive download.
const MaxDownload = 50 << 20

// HTTPS fetches GitHub and GitLab sources as tarballs over HTTPS, without needing git. The zero value talks to the
// real services; tests point the endpoints at a local TLS server and list its host in AllowedHosts.
type HTTPS struct {
	Client         *http.Client
	Getenv         func(string) string
	GitHubAPI      string   // default https://api.github.com
	GitLabAPI      string   // default https://gitlab.com/api/v4
	AllowedHosts   []string // redirect targets that are acceptable (host:port); default the providers' own hosts
	Limits         Limits
	tokenHostCheck bool
}

func (h *HTTPS) defaults() {
	if h.GitHubAPI == "" {
		h.GitHubAPI = "https://api.github.com"
	}
	if h.GitLabAPI == "" {
		h.GitLabAPI = "https://gitlab.com/api/v4"
	}
	if len(h.AllowedHosts) == 0 {
		h.AllowedHosts = []string{"api.github.com", "codeload.github.com", "gitlab.com"}
	}
	if h.Getenv == nil {
		h.Getenv = func(string) string { return "" }
	}
	if h.Client == nil {
		h.Client = &http.Client{Timeout: 2 * time.Minute}
	}
}

func (h *HTTPS) allowed(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	for _, a := range h.AllowedHosts {
		if strings.EqualFold(u.Host, a) {
			return true
		}
	}
	return false
}

// get performs one GET with the redirect policy: HTTPS only, and only to the provider's own hosts.
func (h *HTTPS) get(ctx context.Context, rawURL string, hdr map[string]string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !h.allowed(u) {
		return nil, fmt.Errorf("source: refusing to contact %s", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "rigfile")
	c := *h.Client
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("source: too many redirects")
		}
		if !h.allowed(r.URL) {
			return fmt.Errorf("source: refusing a redirect to %s://%s", r.URL.Scheme, r.URL.Host)
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("source: %s was not found (private repositories need GITHUB_TOKEN or GITLAB_TOKEN)", u.Path)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("source: %s answered %s", u.Host, resp.Status)
	}
	return resp, nil
}

func (h *HTTPS) auth(spec Spec) map[string]string {
	switch spec.Kind {
	case GitHub:
		if t := h.Getenv("GITHUB_TOKEN"); t != "" {
			return map[string]string{"Authorization": "Bearer " + t} // sent to the API host only: Go drops it on a cross-host redirect
		}
	case GitLab:
		if t := h.Getenv("GITLAB_TOKEN"); t != "" {
			return map[string]string{"PRIVATE-TOKEN": t}
		}
	}
	return nil
}

// Resolve turns spec.Ref into a commit id.
func (h *HTTPS) Resolve(ctx context.Context, spec Spec) (string, error) {
	h.defaults()
	if IsCommit(spec.Ref) {
		return spec.Ref, nil
	}
	switch spec.Kind {
	case GitHub:
		ref := spec.Ref
		if ref == "" {
			ref = "HEAD"
		}
		hdr := h.auth(spec)
		if hdr == nil {
			hdr = map[string]string{}
		}
		hdr["Accept"] = "application/vnd.github.sha"
		resp, err := h.get(ctx, h.GitHubAPI+"/repos/"+spec.Path+"/commits/"+url.PathEscape(ref), hdr)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		sha := strings.TrimSpace(string(b))
		if !IsCommit(sha) {
			return "", fmt.Errorf("source: could not resolve %s to a commit", spec)
		}
		return sha, nil
	case GitLab:
		id := url.PathEscape(spec.Path)
		ref := spec.Ref
		if ref == "" {
			resp, err := h.get(ctx, h.GitLabAPI+"/projects/"+id, h.auth(spec))
			if err != nil {
				return "", err
			}
			var pr struct {
				DefaultBranch string `json:"default_branch"`
			}
			err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&pr)
			resp.Body.Close()
			if err != nil || pr.DefaultBranch == "" {
				return "", fmt.Errorf("source: could not read the default branch of %s", spec)
			}
			ref = pr.DefaultBranch
		}
		resp, err := h.get(ctx, h.GitLabAPI+"/projects/"+id+"/repository/commits/"+url.PathEscape(ref), h.auth(spec))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var c struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&c); err != nil || !IsCommit(c.ID) {
			return "", fmt.Errorf("source: could not resolve %s to a commit", spec)
		}
		return c.ID, nil
	}
	return "", fmt.Errorf("source: %s is not a GitHub or GitLab source", spec)
}

// Fetch downloads exactly `commit` and unpacks it into dest.
func (h *HTTPS) Fetch(ctx context.Context, spec Spec, commit, dest string) error {
	h.defaults()
	if !IsCommit(commit) {
		return errors.New("source: fetch needs a full commit id")
	}
	var u string
	switch spec.Kind {
	case GitHub:
		u = h.GitHubAPI + "/repos/" + spec.Path + "/tarball/" + commit
	case GitLab:
		u = h.GitLabAPI + "/projects/" + url.PathEscape(spec.Path) + "/repository/archive.tar.gz?sha=" + commit
	default:
		return fmt.Errorf("source: %s is not a GitHub or GitLab source", spec)
	}
	resp, err := h.get(ctx, u, h.auth(spec))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body := &capReader{r: resp.Body, left: MaxDownload}
	return Extract(body, dest, true, h.Limits)
}

type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, fmt.Errorf("source: the download is larger than %d bytes", MaxDownload)
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}
