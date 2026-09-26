// Package pkgcheck looks up the packages a rig makes an agent run (MCP servers started through npx, uvx and pipx) in
// OSV, the open vulnerability and malicious-package database (docs/trust.md §6). Only exact, pinned versions are checked.
// The registry calls it during a scan; an unreachable or malformed answer is reported as "unavailable", never as "clean".
package pkgcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

// Package is one pinned package.
type Package struct {
	Ecosystem string // "npm" | "PyPI"
	Name      string
	Version   string
}

func (p Package) String() string { return p.Ecosystem + " " + p.Name + "@" + p.Version }

var (
	npmName  = regexp.MustCompile(`^(@[a-z0-9~][a-z0-9._~-]*/)?[a-z0-9~][a-z0-9._~-]*$`)
	pyName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	verRe    = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)
	MaxCheck = 20 // packages looked up per version
)

// Extract lists the pinned packages the manifest's stdio MCP servers run. Unpinned packages are not returned (the
// pinning rule handles those); anything that does not look like a package name is skipped.
func Extract(m *manifest.Manifest) []Package {
	var out []Package
	seen := map[Package]bool{}
	add := func(p Package) {
		if !validPackage(p) || seen[p] || len(out) >= MaxCheck {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, s := range m.MCPServers {
		if s.IsRemote() {
			continue
		}
		base := strings.ToLower(path.Base(strings.ReplaceAll(s.Command, `\`, "/")))
		base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".cmd")
		args := s.Args
		switch base {
		case "npx", "bunx":
			if a := firstArg(args); a != "" {
				if n, v, ok := splitAt(a); ok {
					add(Package{"npm", n, v})
				}
			}
		case "pnpm", "yarn":
			if len(args) > 0 && args[0] == "dlx" {
				if a := firstArg(args[1:]); a != "" {
					if n, v, ok := splitAt(a); ok {
						add(Package{"npm", n, v})
					}
				}
			}
		case "uvx":
			if a := firstArg(args); a != "" {
				if n, v, ok := splitPy(a); ok {
					add(Package{"PyPI", n, v})
				}
			}
		case "pipx", "uv":
			for i, a := range args {
				if a == "run" && i+1 < len(args) {
					if p := firstArg(args[i+1:]); p != "" {
						if n, v, ok := splitPy(p); ok {
							add(Package{"PyPI", n, v})
						}
					}
					break
				}
			}
		}
	}
	return out
}

func firstArg(args []string) string {
	for _, a := range args {
		if a == "--" || strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// splitAt parses npm's name@version (scoped names start with @).
func splitAt(spec string) (name, version string, ok bool) {
	i := strings.LastIndex(spec, "@")
	if i <= 0 {
		return "", "", false
	}
	return spec[:i], spec[i+1:], true
}

// splitPy parses name==version or name@version.
func splitPy(spec string) (name, version string, ok bool) {
	if n, v, found := strings.Cut(spec, "=="); found {
		return n, v, true
	}
	if n, v, found := strings.Cut(spec, "@"); found {
		return n, v, true
	}
	return "", "", false
}

func validPackage(p Package) bool {
	if len(p.Name) == 0 || len(p.Name) > 214 || !verRe.MatchString(p.Version) {
		return false
	}
	switch p.Ecosystem {
	case "npm":
		return npmName.MatchString(p.Name)
	case "PyPI":
		return pyName.MatchString(p.Name)
	}
	return false
}

// Advisory is one OSV record about a package.
type Advisory struct {
	ID       string
	Summary  string
	Severity string
}

// Malicious reports whether the advisory says the package itself is malicious: OpenSSF's malicious-packages data appears in
// OSV with ids starting "MAL-" (UNVERIFIED against the live API; see docs/stage-6-owner-checks.md).
func (a Advisory) Malicious() bool { return strings.HasPrefix(a.ID, "MAL-") }

// Client queries OSV.
type Client struct {
	BaseURL string // default https://api.osv.dev
	HTTP    *http.Client
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return "https://api.osv.dev"
	}
	return strings.TrimRight(c.BaseURL, "/")
}

// ErrUnavailable means OSV could not be asked or answered nonsense.
var ErrUnavailable = errors.New("package check unavailable")

// Check looks one package up. The first page of results is enough: any MAL- record or a first page full of advisories
// already decides what the reader is told.
func (c *Client) Check(ctx context.Context, p Package) ([]Advisory, error) {
	if !validPackage(p) {
		return nil, fmt.Errorf("%w: not a valid package reference", ErrUnavailable)
	}
	body, _ := json.Marshal(map[string]any{"version": p.Version, "package": map[string]string{"name": p.Name, "ecosystem": p.Ecosystem}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: OSV answered %s", ErrUnavailable, resp.Status)
	}
	var out struct {
		Vulns []struct {
			ID               string `json:"id"`
			Summary          string `json:"summary"`
			DatabaseSpecific struct {
				Severity string `json:"severity"`
			} `json:"database_specific"`
		} `json:"vulns"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: unreadable answer", ErrUnavailable)
	}
	var adv []Advisory
	for i, v := range out.Vulns {
		if i >= 50 {
			break
		}
		adv = append(adv, Advisory{ID: clean(v.ID, 60), Summary: clean(v.Summary, 160), Severity: clean(v.DatabaseSpecific.Severity, 20)})
	}
	return adv, nil
}

// clean makes text from a third party safe to store and show: printable characters only, bounded.
func clean(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) && r != '�' {
			b.WriteRune(r)
		}
		if b.Len() >= max {
			break
		}
	}
	return b.String()
}
