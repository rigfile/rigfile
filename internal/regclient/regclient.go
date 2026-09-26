// Package regclient is the CLI's client for the Rigfile registry API (docs/registry.md §3). It sends the bearer token
// only to the registry's own host, refuses plain HTTP except to loopback, and never follows a redirect to another host.
package regclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one registry.
type Client struct {
	Base  string        // https://registry.example.org
	Token func() string // "" = anonymous; called per request so a login mid-session is picked up
	HTTP  *http.Client
}

// Version is one release as the registry reports it.
type Version struct {
	Version    string    `json:"version"`
	Status     string    `json:"status"`
	SHA256     string    `json:"tarball_sha256"`
	Size       int64     `json:"size"`
	YankReason string    `json:"yank_reason"`
	Targets    []string  `json:"targets"`
	Secrets    []string  `json:"needs_secrets"`
	Logins     []string  `json:"needs_logins"`
	Layers     []string  `json:"layers"`
	Findings   []Finding `json:"findings"`
	Warnings   []Finding `json:"warnings"`
}

// Finding is a scan result (never contains a value).
type Finding struct {
	Kind    string `json:"kind"`
	Rule    string `json:"rule"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// RigInfo is a rig's metadata.
type RigInfo struct {
	Owner       string    `json:"owner"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"`
	Stars       int       `json:"stars"`
	Latest      string    `json:"latest"`
	Versions    []Version `json:"versions"`
}

// APIError is a refusal from the registry.
type APIError struct {
	Status   int
	Msg      string
	Problems []string
}

func (e *APIError) Error() string {
	s := e.Msg
	if s == "" {
		s = http.StatusText(e.Status)
	}
	if len(e.Problems) > 0 {
		s += ":\n  - " + strings.Join(e.Problems, "\n  - ")
	}
	return s
}

// ValidateBase checks a registry URL: an origin, https (or http to loopback).
func ValidateBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return "", fmt.Errorf("registry: %q is not a registry address like https://registry.example.org", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		ip := net.ParseIP(u.Hostname())
		if !(u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
			return "", fmt.Errorf("registry: %s must use https (plain http is only allowed for localhost)", u.Host)
		}
	default:
		return "", fmt.Errorf("registry: %q must start with https://", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func (c *Client) http() *http.Client {
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 2 * time.Minute}
	}
	cc := *h
	base, _ := url.Parse(c.Base)
	cc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Host != base.Host || req.URL.Scheme != base.Scheme {
			return fmt.Errorf("registry: refusing a redirect to %s", req.URL.Host)
		}
		return nil
	}
	return &cc
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, ctype string) (*http.Response, error) {
	base, err := ValidateBase(c.Base)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("User-Agent", "rigfile")
	if c.Token != nil {
		if t := c.Token(); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
	}
	return c.http().Do(req)
}

func readAPIError(resp *http.Response) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var e struct {
		Error    string   `json:"error"`
		Problems []string `json:"problems"`
	}
	_ = json.Unmarshal(b, &e)
	return &APIError{Status: resp.StatusCode, Msg: e.Error, Problems: e.Problems}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return readAPIError(resp)
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func rigPath(owner, name string) string {
	return "/v1/rigs/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// Rig fetches a rig's metadata and the versions the caller may see.
func (c *Client) Rig(ctx context.Context, owner, name string) (*RigInfo, error) {
	var r RigInfo
	if err := c.getJSON(ctx, rigPath(owner, name), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Resolve finds the newest published version satisfying rng ("" = any); an exact version may be yanked.
func (c *Client) Resolve(ctx context.Context, owner, name, rng string) (*Version, error) {
	var v Version
	if err := c.getJSON(ctx, rigPath(owner, name)+"/resolve?range="+url.QueryEscape(rng), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// VersionInfo fetches one version's status (findings only for the owner).
func (c *Client) VersionInfo(ctx context.Context, owner, name, version string) (*Version, error) {
	var v Version
	if err := c.getJSON(ctx, rigPath(owner, name)+"/versions/"+url.PathEscape(version), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// MaxTarball caps a download.
const MaxTarball = 100 << 20

// Download opens a version's tarball. The caller must verify the hash it expects (Version.SHA256): the header the
// registry sends is returned too, but only a locally computed hash proves anything.
func (c *Client) Download(ctx context.Context, owner, name, version string) (io.ReadCloser, string, error) {
	resp, err := c.do(ctx, http.MethodGet, rigPath(owner, name)+"/versions/"+url.PathEscape(version)+"/tarball", nil, "")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", readAPIError(resp)
	}
	return &limited{ReadCloser: resp.Body, left: MaxTarball}, resp.Header.Get("X-Rigfile-SHA256"), nil
}

type limited struct {
	io.ReadCloser
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, errors.New("regclient: the download is larger than the limit")
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.ReadCloser.Read(p)
	l.left -= int64(n)
	return n, err
}

// Upload publishes a tarball as a new (pending) version.
func (c *Client) Upload(ctx context.Context, owner, name string, tarball []byte) (*Version, error) {
	resp, err := c.do(ctx, http.MethodPost, rigPath(owner, name)+"/versions", tarball, "application/gzip")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusAccepted {
		return nil, readAPIError(resp)
	}
	defer resp.Body.Close()
	var v Version
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) error {
	resp, err := c.do(ctx, http.MethodPost, path, []byte(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return readAPIError(resp)
	}
	resp.Body.Close()
	return nil
}

// SetVisibility makes a rig public or private.
func (c *Client) SetVisibility(ctx context.Context, owner, name, vis string) error {
	return c.postForm(ctx, rigPath(owner, name)+"/visibility", url.Values{"visibility": {vis}})
}

// Yank hides a version from resolution.
func (c *Client) Yank(ctx context.Context, owner, name, version, reason string) error {
	return c.postForm(ctx, rigPath(owner, name)+"/versions/"+url.PathEscape(version)+"/yank", url.Values{"reason": {reason}})
}

// Me returns the signed-in account.
func (c *Client) Me(ctx context.Context) (string, error) {
	var m struct {
		Login string `json:"login"`
	}
	if err := c.getJSON(ctx, "/v1/me", &m); err != nil {
		return "", err
	}
	return m.Login, nil
}

// Revoke ends this token on the server (logout).
func (c *Client) Revoke(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v1/tokens/current", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusUnauthorized {
		return readAPIError(resp)
	}
	return nil
}
