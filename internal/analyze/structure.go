package analyze

import (
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/rigfile/rigfile/internal/manifest"
)

// manifestHits looks at what the manifest itself declares: hooks, MCP servers and permissions.
func manifestHits(name string, data []byte) []hit {
	var out []hit
	m, err := manifest.Parse(data)
	if err != nil {
		return nil // an invalid manifest is the validator's business, not the analyzer's
	}
	add := func(rule string) { out = append(out, hit{rule: rule}) }
	for _, s := range m.MCPServers {
		if s.IsRemote() {
			if u, err := url.Parse(s.URL); err == nil && u.Scheme == "http" && !loopback(u.Hostname()) {
				add("struct.mcp-http")
			}
			continue
		}
		full := strings.ToLower(s.Command + " " + strings.Join(s.Args, " "))
		base := strings.ToLower(path.Base(strings.ReplaceAll(s.Command, `\`, "/")))
		base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".cmd")
		switch base {
		case "sh", "bash", "zsh", "dash", "cmd", "powershell", "pwsh":
			add("struct.mcp-shell")
			if matchAny(downloadRun, full) || (strings.Contains(full, "curl") || strings.Contains(full, "wget")) && strings.Contains(full, "|") {
				add("struct.mcp-download")
			}
		case "npx", "bunx", "uvx", "pipx", "pnpm", "dlx":
			add("struct.mcp-launcher")
		}
	}
	for _, rules := range [][]manifest.PermissionRule{m.Permissions.Allow} {
		for _, r := range rules {
			if broadPermission(r) {
				add("struct.allow-broad")
			}
		}
	}
	for _, h := range m.Hooks {
		runs := []string{h.Run.Single}
		for _, r := range h.Run.PerOS {
			runs = append(runs, r)
		}
		for _, r := range runs {
			if r == "" {
				continue
			}
			if _, builtin := manifest.Builtin(r); !builtin {
				add("struct.hook-script")
			}
		}
	}
	return out
}

func broadPermission(r manifest.PermissionRule) bool {
	v := strings.TrimSpace(r.Bash + r.PowerShell)
	return v == "*" || v == "**" || v == ".*"
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func matchAny(res []*regexp.Regexp, s string) bool {
	for _, r := range res {
		if r.MatchString(s) {
			return true
		}
	}
	return false
}
