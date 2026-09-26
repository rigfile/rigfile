// Package source fetches rigs from git hosts (docs/sharing.md): parse a source string, resolve its ref to a commit,
// download exactly that commit's files through a hardened extractor, hash the tree, and keep it in a
// content-addressed cache. Nothing here runs anything from the fetched rig.
package source

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/regclient"
)

// Kinds of source.
const (
	GitHub = "github"
	GitLab = "gitlab"
	Git    = "git" // any other git URL, fetched with the git binary
	// Registry is a rig published to a Rigfile registry; Spec.URL is the registry's origin and Ref a version or range.
	Registry = "registry"
)

// Spec is a parsed source string.
type Spec struct {
	Raw    string
	Kind   string
	Path   string // owner/repo (GitHub), group/.../project (GitLab)
	URL    string // Git kind only: the clone URL
	Ref    string // tag, branch or commit; "" = the default branch
	Subdir string // directory inside the repository that holds rigfile.yaml; "" = the root
}

var (
	segRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]*$`)
	shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// a registry ref is a version or a range (^1, ~1.2, 1.2.3, 1.0.0-rc1)
	rangeRe = regexp.MustCompile(`^[\^~]?[0-9A-Za-z][0-9A-Za-z._+-]*$`)
)

// IsCommit reports whether ref is a full 40-hex commit id.
func IsCommit(ref string) bool { return shaRe.MatchString(ref) }

// String is the canonical form of the source (what state.json and the plan screen show).
func (s Spec) String() string {
	var b strings.Builder
	switch s.Kind {
	case GitHub:
		b.WriteString("github.com/" + s.Path)
	case GitLab:
		b.WriteString("gitlab.com/" + s.Path)
	case Registry:
		b.WriteString("rigfile+" + s.URL + "/" + s.Path)
	default:
		b.WriteString(s.URL)
	}
	if s.Ref != "" {
		b.WriteString("@" + s.Ref)
	}
	if s.Subdir != "" {
		b.WriteString("//" + s.Subdir)
	}
	return b.String()
}

// Looks reports whether arg is written like a remote source rather than a local directory.
func Looks(arg string) bool {
	if strings.Contains(arg, "://") {
		return true
	}
	return strings.HasPrefix(arg, "github.com/") || strings.HasPrefix(arg, "gitlab.com/")
}

// Parse reads `github.com/o/r[@ref][//sub]`, `gitlab.com/g/p[@ref][//sub]` or `<scheme>://host/path.git[@ref][//sub]`.
func Parse(raw string) (Spec, error) {
	s := Spec{Raw: raw}
	rest := strings.TrimSpace(raw)
	if rest == "" {
		return s, fmt.Errorf("source: empty")
	}
	scheme := ""
	if i := strings.Index(rest, "://"); i >= 0 {
		scheme, rest = rest[:i], rest[i+3:]
	}
	if i := strings.Index(rest, "//"); i >= 0 {
		s.Subdir, rest = rest[i+2:], rest[:i]
	}
	hostEnd := strings.IndexByte(rest, '/')
	if hostEnd < 0 {
		return s, fmt.Errorf("source: %q has no repository path", raw)
	}
	host, p := rest[:hostEnd], rest[hostEnd+1:]
	if i := strings.IndexByte(p, '@'); i >= 0 {
		p, s.Ref = p[:i], p[i+1:]
	}
	p = strings.TrimSuffix(strings.TrimRight(p, "/"), ".git")
	if strings.HasPrefix(scheme, "rigfile+") {
		if s.Ref != "" && !rangeRe.MatchString(s.Ref) {
			return s, fmt.Errorf("source: %q is not a version or range", s.Ref)
		}
	} else if s.Ref != "" && !refRe.MatchString(s.Ref) || strings.Contains(s.Ref, "..") || strings.HasSuffix(s.Ref, "/") || strings.HasSuffix(s.Ref, ".lock") {
		return s, fmt.Errorf("source: %q is not a valid ref", s.Ref)
	}
	if s.Subdir != "" {
		c := path.Clean(s.Subdir)
		if c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") || strings.ContainsAny(c, `\:`+"\x00") {
			return s, fmt.Errorf("source: %q is not a valid subdirectory", s.Subdir)
		}
		s.Subdir = c
	}
	hostName := strings.ToLower(host)
	if u, err := url.Parse("//" + host); err == nil && u.Hostname() != "" {
		hostName = strings.ToLower(u.Hostname())
	}
	switch {
	case (scheme == "" || scheme == "https") && hostName == "github.com":
		parts := strings.Split(p, "/")
		if len(parts) != 2 || !segRe.MatchString(parts[0]) || !segRe.MatchString(parts[1]) {
			return s, fmt.Errorf("source: %q is not github.com/owner/repo", raw)
		}
		s.Kind, s.Path = GitHub, p
	case (scheme == "" || scheme == "https") && hostName == "gitlab.com":
		parts := strings.Split(p, "/")
		if len(parts) < 2 {
			return s, fmt.Errorf("source: %q is not gitlab.com/group/project", raw)
		}
		for _, x := range parts {
			if !segRe.MatchString(x) {
				return s, fmt.Errorf("source: %q is not gitlab.com/group/project", raw)
			}
		}
		s.Kind, s.Path = GitLab, p
	case scheme == "rigfile+https" || scheme == "rigfile+http":
		parts := strings.Split(p, "/")
		if len(parts) != 2 || !segRe.MatchString(parts[0]) || !segRe.MatchString(parts[1]) {
			return s, fmt.Errorf("source: %q is not rigfile+https://registry/owner/name", raw)
		}
		base, err := regclient.ValidateBase(strings.TrimPrefix(scheme, "rigfile+") + "://" + host)
		if err != nil {
			return s, err
		}
		s.Kind, s.URL, s.Path = Registry, base, p
	case scheme == "https" || scheme == "ssh" || scheme == "file":
		if strings.HasPrefix(host, "-") || strings.ContainsAny(raw, " \t\n\x00") {
			return s, fmt.Errorf("source: %q is not a valid URL", raw)
		}
		s.Kind = Git
		s.URL = scheme + "://" + host + "/" + p
	case scheme == "":
		return s, fmt.Errorf("source: %q: only github.com/, gitlab.com/ or an https://, ssh:// or file:// git URL can be pulled", raw)
	default:
		return s, fmt.Errorf("source: scheme %q is not allowed (https, ssh or file)", scheme)
	}
	return s, nil
}
