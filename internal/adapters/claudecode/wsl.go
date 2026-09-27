package claudecode

import (
	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/platform"
)

// wslWindowsSide are the credential locations under the Windows user profile that are reachable from inside WSL
// as /mnt/c/Users/<name>/... Claude Code's Linux-side deny rules (~/.ssh/** ...) do not cover them, so base-secure
// adds the same protections for the Windows side. `*` stands for the Windows user name (docs/platforms.md §5).
var wslWindowsSide = []struct{ path, why string }{
	{".ssh/**", "SSH keys and config (Windows side)"},
	{".aws/**", "cloud credentials (Windows side)"},
	{".azure/**", "cloud credentials (Windows side)"},
	{".kube/**", "cluster credentials (Windows side)"},
	{".gnupg/**", "GPG keys (Windows side)"},
	{".docker/config.json", "registry credentials (Windows side)"},
	{".npmrc", "registry tokens (Windows side)"},
	{".pypirc", "registry tokens (Windows side)"},
	{".netrc", "stored passwords (Windows side)"},
	{".claude/.credentials.json", "Claude Code login (Windows side)"},
	{".claude.json", "Claude Code login session (Windows side)"},
	{"AppData/Roaming/Microsoft/Credentials/**", "Windows saved credentials"},
	{"AppData/Local/Microsoft/Credentials/**", "Windows saved credentials"},
	{"AppData/Roaming/Microsoft/Protect/**", "DPAPI master keys"},
	{"AppData/Roaming/gnupg/**", "GPG keys (Windows side)"},
	{"AppData/Roaming/rigfile/**", "Rigfile's git hooks and configuration (Windows side)"},
	{"AppData/Local/rigfile/**", "Rigfile state, backups and encrypted secrets (Windows side)"},
}

// WSLDenies are the extra deny rules base-secure applies inside WSL: the Windows drive is mounted under /mnt/c and
// an agent running in WSL can read it. UNVERIFIED: whether `*` in a directory segment of a Read rule matches the
// Windows user-name directory, and non-default automount roots (wsl.conf); the live check is in S3-M10.
func WSLDenies(pi *platform.Info) []manifest.PermissionRule {
	if pi == nil || !pi.WSL {
		return nil
	}
	out := make([]manifest.PermissionRule, 0, len(wslWindowsSide))
	for _, x := range wslWindowsSide {
		out = append(out, manifest.PermissionRule{Read: "/mnt/c/Users/*/" + x.path, Reason: x.why})
	}
	return out
}
