package platform

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeRelative turns an absolute host path into a relative path that can be joined under a backup
// directory without ever escaping it: the volume/drive is folded into the first element
// ("C:" -> "C_"), separators are normalised, and any ".." element is rejected.
// It uses the HOST OS's path rules, which is why it lives here.
func SafeRelative(abs string) (string, error) {
	if !filepath.IsAbs(abs) {
		return "", fmt.Errorf("platform: %q is not an absolute path", abs)
	}
	clean := filepath.Clean(abs)
	vol := filepath.VolumeName(clean)
	rest := strings.TrimLeft(clean[len(vol):], `/\`)
	vol = strings.NewReplacer(":", "_", `\`, "_", "/", "_").Replace(vol)
	rel := rest
	if vol != "" {
		rel = filepath.Join(vol, rest)
	}
	for _, seg := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", fmt.Errorf("platform: %q escapes its root", abs)
		}
	}
	return rel, nil
}

// ToShellPath renders a Windows absolute path (C:\Users\me\x, \\server\share\x) with forward slashes, the form
// Git for Windows' bundled sh, git's own config and `core.hooksPath` all accept without escaping. A path that
// is not Windows-shaped is returned unchanged, so it is safe to call on any OS's paths.
func ToShellPath(p string) string {
	drive := len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
	if drive || strings.HasPrefix(p, `\\`) {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return p
}
