package common

import (
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

// ExecWrap is the argv of a stdio MCP server wrapped for Rigfile's exec shim (docs/targets/*.md): secrets
// become `--secret ENV=ref` and are resolved at launch, never written to a config file. bin is the rigfile
// command ("rigfile" unless a full path was configured).
func ExecWrap(bin string, s manifest.MCPServer) (command string, args []string) {
	return ExecWrapFor(bin, "", s, nil)
}

// ExecWrapFor is ExecWrap for a named server, also telling the exec shim what the Level 2 broker needs (docs/rigd.md):
// the server's name, its network.allow list and, per secret it uses, the hosts the secret is bound to. A server that
// declares no network.allow gets none of these flags (nothing to enforce), so its entry is exactly the Level 1 one.
func ExecWrapFor(bin, name string, s manifest.MCPServer, secretHosts map[string][]string) (command string, args []string) {
	if bin == "" {
		bin = "rigfile"
	}
	args = []string{"exec"}
	if len(s.Network.Allow) > 0 {
		if name != "" {
			args = append(args, "--server", name)
		}
		args = append(args, "--allow", strings.Join(s.Network.Allow, ","))
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	bound := map[string]bool{}
	var binds []string
	for _, k := range keys {
		if ref, ok := manifest.SecretRef(s.Env[k]); ok {
			args = append(args, "--secret", k+"="+ref)
			if hosts := secretHosts[ref]; len(hosts) > 0 && len(s.Network.Allow) > 0 && !bound[ref] {
				bound[ref] = true
				binds = append(binds, "--bind", ref+"="+strings.Join(hosts, ","))
			}
		} else {
			args = append(args, "--env", k+"="+s.Env[k])
		}
	}
	args = append(args, binds...)
	args = append(args, "--", s.Command)
	args = append(args, s.Args...)
	return bin, args
}

// RemoteUnsupported explains why a remote (http) server cannot be written for a target that has no way to keep
// a credential out of the file, or "" when it can be written as is.
func RemoteUnsupported(s manifest.MCPServer) string {
	if s.Auth == "bearer" || s.BearerToken != "" {
		return "bearer-token servers need a header helper or the Level-2 broker (not available yet)"
	}
	for k, v := range s.Headers {
		if _, isRef := manifest.SecretRef(v); isRef {
			return "header " + k + " references a secret; injecting secrets into headers is not supported yet"
		}
	}
	return ""
}
