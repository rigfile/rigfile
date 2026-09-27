package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/source"
)

var (
	ownerRe   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38})$`)
	rigNameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62})$`)
	versionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)
)

// reservedOwners are namespaces only an admin may publish under: the project's own and the vendors the plan protects
// against squatting (docs/registry.md §4).
var reservedOwners = map[string]bool{"rigfile": true, "anthropic": true, "openai": true, "google": true, "claude": true, "codex": true,
	"gemini": true, "cursor": true, "github": true, "microsoft": true}

// IsReservedOwner reports whether owner is a protected namespace.
func IsReservedOwner(owner string) bool { return reservedOwners[strings.ToLower(owner)] }

// uploadInfo is what validating an uploaded tarball established.
type uploadInfo struct {
	NewVersion
	Problems []string // why it was refused, when it was
}

// validateUpload unpacks the tarball with the hardened extractor and checks it is a valid rig for owner/name. It never
// executes or interprets anything from the archive beyond parsing the manifest.
func validateUpload(data []byte, owner, name string) (*uploadInfo, error) {
	dir, err := os.MkdirTemp("", "rigfile-upload-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "rig")
	if err := source.Extract(bytes.NewReader(data), root, false, source.DefaultLimits); err != nil {
		return nil, badUpload("the archive is not acceptable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, manifest.FileName)); err != nil {
		return nil, badUpload("the archive has no %s at its top level", manifest.FileName)
	}
	l, err := manifest.Load(root)
	if err != nil {
		return nil, badUpload("rigfile.yaml is not valid: %v", err)
	}
	if ps := manifest.Check(l); manifest.HasErrors(ps) {
		var msgs []string
		for _, p := range ps {
			if p.Level == manifest.Error {
				msgs = append(msgs, p.Where+": "+p.Msg)
			}
		}
		return nil, &UploadError{Msg: "the rig has errors", Problems: msgs}
	}
	m := l.M
	if m.Name != owner+"/"+name {
		return nil, badUpload("the manifest is named %q but the upload is for %s/%s", m.Name, owner, name)
	}
	if !versionRe.MatchString(m.Version) {
		return nil, badUpload("the manifest version %q must be x.y.z (optionally with a -pre-release suffix)", m.Version)
	}
	for _, f := range m.From {
		if strings.HasPrefix(strings.ToLower(f), "rigfile/") && !strings.HasPrefix(strings.ToLower(f), "rigfile/base-secure") {
			return nil, badUpload("%q: only rigfile/base-secure may be inherited from the rigfile/ namespace", f)
		}
	}
	yamlBytes, err := os.ReadFile(filepath.Join(root, manifest.FileName))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	info := &uploadInfo{NewVersion: NewVersion{
		Owner: owner, Name: name, Version: m.Version, TarballSHA: hex.EncodeToString(sum[:]), Size: int64(len(data)),
		ManifestYAML: string(yamlBytes), Description: trunc(m.Description, 500), Layers: m.From,
	}}
	for _, t := range m.Targets.Include {
		info.Targets = append(info.Targets, t)
	}
	for k := range m.Secrets {
		info.NeedsSecrets = append(info.NeedsSecrets, k)
	}
	sort.Strings(info.NeedsSecrets)
	for _, lg := range m.Logins {
		info.NeedsLogins = append(info.NeedsLogins, lg.Provider)
	}
	err = filepath.WalkDir(root, func(fp string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, fp)
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		text := len(b) <= 256<<10 && utf8.Valid(b) && !bytes.Contains(b, []byte{0})
		info.Files = append(info.Files, FileEntry{Path: rel, Size: int64(len(b)), SHA256: hex.EncodeToString(h[:]), IsText: text})
		if strings.EqualFold(rel, "README.md") && text {
			info.Readme = string(b)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

// UploadError is a refusal the publisher can act on.
type UploadError struct {
	Msg      string
	Problems []string
}

func (e *UploadError) Error() string { return e.Msg }

func badUpload(format string, a ...any) error { return &UploadError{Msg: fmt.Sprintf(format, a...)} }
