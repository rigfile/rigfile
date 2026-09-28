package merge

import (
	"errors"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/manifest"
)

// layer builds a Layer from a YAML body (header added).
func layer(t *testing.T, name string, locked bool, body string) Layer {
	t.Helper()
	m, err := manifest.Parse([]byte("apiVersion: rigfile.dev/v1\nname: " + name + "\nversion: 1.0.0\n" + body))
	if err != nil {
		t.Fatalf("layer %s: %v", name, err)
	}
	return Layer{Name: name, Version: "1.0.0", Locked: locked, M: m, Dir: "/rigs/" + name}
}

func mustMerge(t *testing.T, target string, ls ...Layer) *Merged {
	t.Helper()
	m, err := Merge(ls, target)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return m
}

func ruleStrings(list []Prov[manifest.PermissionRule]) []string {
	var out []string
	for _, r := range list {
		k, v := r.V.Kind()
		out = append(out, k+":"+v)
	}
	return out
}

// §7 A: permissions union; deny is never removed; allow/ask are kept as data (shadowing is the adapter's job).
func TestPermissionsUnionAndDenyIsNeverRemoved(t *testing.T) {
	base := layer(t, BaseSecure, true, "permissions:\n  deny: [{read: '~/.ssh/**'}]\n  ask: [{bash: 'git push*'}]\n")
	py := layer(t, "adams/python-dev", false, "permissions:\n  allow: [{read: '~/.ssh/config'}]\n  deny: [{bash: 'rm -rf /*'}]\n")
	ds := layer(t, "adams/data-science", false, "permissions:\n  allow: [{bash: 'git push*'}]\n  deny: [{read: '~/.ssh/**'}]\n") // duplicate deny: deduped
	m := mustMerge(t, "claude-code", base, py, ds)
	if got := strings.Join(ruleStrings(m.Deny), " | "); got != "read:~/.ssh/** | bash:rm -rf /*" {
		t.Fatalf("deny = %s", got)
	}
	if len(m.Ask) != 1 || len(m.Allow) != 2 {
		t.Fatalf("ask=%v allow=%v", ruleStrings(m.Ask), ruleStrings(m.Allow))
	}
	if m.Deny[0].Layer != BaseSecure {
		t.Fatal("first provenance must be kept for a duplicate rule")
	}
}

// §7 B: a locked item cannot be redefined by a later layer.
func TestLockedItemsCannotBeReplaced(t *testing.T) {
	hook := "hooks:\n  - {id: block-env-commit, event: pre_tool_use, run: 'builtin:block-env'}\n"
	base := layer(t, BaseSecure, true, hook+"instructions:\n  - {id: security, file: i/sec.md}\nmcp_servers:\n  guard: {command: rigfile, args: [\"exec\", \"--\", \"x\"]}\n")
	for name, body := range map[string]string{
		"hook":        "hooks:\n  - {id: block-env-commit, event: stop, run: 'builtin:other'}\n",
		"instruction": "instructions:\n  - {id: security, file: mine.md}\n",
		"mcp server":  "mcp_servers:\n  guard: {command: evil}\n",
	} {
		_, err := Merge([]Layer{base, layer(t, "adams/x", false, body)}, "claude-code")
		var le *LockedError
		if !errors.As(err, &le) || le.LockedBy != BaseSecure {
			t.Errorf("%s: want LockedError, got %v", name, err)
		}
	}
	// A different id next to it is fine, and both exist.
	m := mustMerge(t, "claude-code", base, layer(t, "adams/x", false, "hooks:\n  - {id: mine, event: stop, run: 'builtin:x'}\n"))
	if len(m.Hooks) != 2 {
		t.Fatalf("hooks = %d", len(m.Hooks))
	}
}

// §7 C: MCP servers are replaced wholesale; nothing leaks through from the earlier definition.
func TestMCPServerIsReplacedWholesale(t *testing.T) {
	a := layer(t, "adams/python-dev", false, "mcp_servers:\n  github: {transport: http, url: 'https://mcp.example.test/mcp', auth: oauth}\n")
	b := layer(t, "adams/data-science", false, "mcp_servers:\n  github: {command: npx, args: ['-y', 'pkg@1.0.0']}\n")
	m := mustMerge(t, "claude-code", a, b)
	g := m.MCPServers["github"]
	if g.V.Command != "npx" || g.V.URL != "" || g.V.Auth != "" || g.Layer != "adams/data-science" {
		t.Fatalf("not replaced wholesale: %+v", g)
	}
	if len(m.Replaced) != 1 || m.Replaced[0] != (Replacement{"mcp server", "github", "adams/python-dev", "adams/data-science"}) {
		t.Fatalf("replacements = %+v", m.Replaced)
	}
}

// §7 D + §2.1: diamonds are handled by the resolver; here: order is layer order, later replaces earlier in place.
func TestInstructionsKeepPositionAndScopesAreSeparate(t *testing.T) {
	a := layer(t, "a/base", false, "instructions:\n  - {id: one, file: a1.md}\n  - {id: two, file: a2.md}\n  - {id: one, file: p1.md, scope: project}\n")
	b := layer(t, "b/top", false, "instructions:\n  - {id: one, file: b1.md}\n  - {id: three, file: b3.md}\n")
	m := mustMerge(t, "claude-code", a, b)
	var got []string
	for _, i := range m.Instructions {
		got = append(got, i.V.Key()+"="+i.V.File)
	}
	want := "user/one=b1.md user/two=a2.md project/one=p1.md user/three=b3.md"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

// §7 E: overrides.<target> apply after their own layer, only for that target, and cannot remove denies.
func TestOverridesApplyOnlyToTheirTarget(t *testing.T) {
	top := layer(t, "adams/data-science", false, `permissions:
  deny: [{read: '**/secrets/**'}]
mcp_servers:
  helper: {command: npx, args: ['-y', 'h@1.0.0']}
overrides:
  codex:
    permissions:
      deny: [{read: '~/.codex/auth.json'}]
    mcp_servers:
      helper: {command: npx, args: ['-y', 'h@2.0.0']}
`)
	cc := mustMerge(t, "claude-code", top)
	cx := mustMerge(t, "codex", top)
	if len(cc.Deny) != 1 || cc.MCPServers["helper"].V.Args[1] != "h@1.0.0" {
		t.Fatalf("claude-code must not see codex overrides: %v %v", ruleStrings(cc.Deny), cc.MCPServers["helper"].V.Args)
	}
	if len(cx.Deny) != 2 || cx.MCPServers["helper"].V.Args[1] != "h@2.0.0" {
		t.Fatalf("codex overrides missing: %v %v", ruleStrings(cx.Deny), cx.MCPServers["helper"].V.Args)
	}
	if cx.MCPServers["helper"].Layer != "adams/data-science#overrides.codex" {
		t.Fatalf("override provenance: %s", cx.MCPServers["helper"].Layer)
	}
	// No target => no overrides at all.
	if n := mustMerge(t, "", top); len(n.Deny) != 1 {
		t.Fatal("empty target must not apply overrides")
	}
}

func TestSkillsAgentsCommandsUnionAndReplaceByKey(t *testing.T) {
	a := layer(t, "a/a", false, "skills:\n  - {path: skills/pdf}\n  - {ref: 'skills.sh/x/y@1.0.0'}\nagents:\n  - {path: agents/rev.md}\ncommands:\n  - {path: commands/ship.md}\n")
	b := layer(t, "b/b", false, "skills:\n  - {path: other/pdf}\nagents:\n  - {path: agents/new.md}\n")
	m := mustMerge(t, "claude-code", a, b)
	if len(m.Skills) != 2 || m.Skills[0].V.Path != "other/pdf" || m.Skills[0].Layer != "b/b" {
		t.Fatalf("skill 'pdf' should be replaced in place by the later layer: %+v", m.Skills)
	}
	if len(m.Agents) != 2 || len(m.Commands) != 1 {
		t.Fatalf("agents=%d commands=%d", len(m.Agents), len(m.Commands))
	}
	if len(m.Replaced) != 1 || m.Replaced[0].Category != "skill" {
		t.Fatalf("replacements = %+v", m.Replaced)
	}
}

func TestDuplicateWithinOneLayerIsAnError(t *testing.T) {
	// The schema allows it; Check flags it; Merge must still refuse rather than pick one silently.
	l := layer(t, "a/a", false, "hooks:\n  - {id: h, event: stop, run: 'builtin:x'}\n  - {id: h, event: stop, run: 'builtin:y'}\n")
	if _, err := Merge([]Layer{l}, ""); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("got %v", err)
	}
}

func TestToolsUnionAndPinConflicts(t *testing.T) {
	a := layer(t, "a/a", false, "tools:\n  common: [gh, uv, node@20]\n  npm: ['@scope/pkg@1.0.0']\n")
	b := layer(t, "b/b", false, "tools:\n  common: [gh@2.50.0, jq, node@22]\n  npm: ['@scope/pkg@1.0.0']\n  linux: {apt: [build-essential]}\n")
	m := mustMerge(t, "", a, b)
	got := map[string]string{}
	for _, e := range m.Tools["common"] {
		got[e.V] = e.Layer
	}
	if len(m.Tools["common"]) != 4 || got["gh@2.50.0"] != "b/b" || got["node@22"] != "b/b" || got["uv"] != "a/a" || got["jq"] != "b/b" {
		t.Fatalf("common = %v", m.Tools["common"])
	}
	if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "node") || !strings.Contains(m.Warnings[0], "20") {
		t.Fatalf("a pin conflict must produce exactly one warning: %v", m.Warnings)
	}
	if len(m.Tools["npm"]) != 1 || len(m.Tools["linux.apt"]) != 1 {
		t.Fatalf("npm=%v apt=%v", m.Tools["npm"], m.Tools["linux.apt"])
	}
}

func TestSplitPin(t *testing.T) {
	for in, want := range map[string][2]string{
		"gh": {"gh", ""}, "gh@2.1": {"gh", "2.1"}, "@scope/pkg": {"@scope/pkg", ""}, "@scope/pkg@1.0.0": {"@scope/pkg", "1.0.0"},
	} {
		if n, v := splitPin(in); n != want[0] || v != want[1] {
			t.Errorf("splitPin(%q) = %q,%q", in, n, v)
		}
	}
}

func TestSecretHostsMayOnlyNarrow(t *testing.T) {
	a := layer(t, "a/a", false, "secrets:\n  x/key: {description: d, hosts: ['*.example.com']}\n")
	narrow := layer(t, "b/b", false, "secrets:\n  x/key: {description: newer, hosts: ['api.example.com']}\n")
	m := mustMerge(t, "", a, narrow)
	s := m.Secrets["x/key"].V
	if s.Description != "newer" || len(s.Hosts) != 1 || s.Hosts[0] != "api.example.com" {
		t.Fatalf("narrowing should be accepted: %+v", s)
	}
	for name, hosts := range map[string]string{"different host": "evil.com", "wider wildcard": "'*.com'", "sibling": "'*.other.com'"} {
		wide := layer(t, "c/c", false, "secrets:\n  x/key: {description: d, hosts: ["+hosts+"]}\n")
		_, err := Merge([]Layer{a, wide}, "")
		var we *WideningError
		if !errors.As(err, &we) {
			t.Errorf("%s: want WideningError, got %v", name, err)
		}
	}
	// A later layer that omits hosts keeps the earlier binding (it cannot widen by silence).
	silent := layer(t, "d/d", false, "secrets:\n  x/key: {description: only text}\n")
	m = mustMerge(t, "", a, silent)
	if h := m.Secrets["x/key"].V.Hosts; len(h) != 1 || h[0] != "*.example.com" {
		t.Fatalf("binding lost: %v", h)
	}
}

func TestModelsGatewaysAndConsistency(t *testing.T) {
	a := layer(t, "a/a", false, "models:\n  m1: {role: local-coder, serve: {port: 8080}}\n")
	b := layer(t, "b/b", false, "gateways:\n  g: {listen: '127.0.0.1:4000', routes: {m1: 'http://127.0.0.1:8080/v1'}}\nrouting: {default: local, local_for: [summaries], fallback_on_limit: m1}\n")
	m := mustMerge(t, "", a, b)
	if m.Routing.Default != "local" || m.Routing.FallbackOnLimit != "m1" || len(m.Gateways) != 1 {
		t.Fatalf("%+v", m.Routing)
	}
	// dangling references and port clashes are errors
	bad := layer(t, "c/c", false, "gateways:\n  g: {listen: '127.0.0.1:4000', routes: {ghost: 'http://127.0.0.1:8080/v1'}}\n")
	if _, err := Merge([]Layer{a, bad}, ""); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("dangling route: %v", err)
	}
	clash := layer(t, "d/d", false, "gateways:\n  g: {listen: '127.0.0.1:8080', routes: {m1: 'http://127.0.0.1:8080/v1'}}\n")
	if _, err := Merge([]Layer{a, clash}, ""); err == nil || !strings.Contains(err.Error(), "port 8080") {
		t.Fatalf("port clash: %v", err)
	}
	dangling := layer(t, "e/e", false, "routing: {fallback_on_limit: nope}\n")
	if _, err := Merge([]Layer{dangling}, ""); err == nil {
		t.Fatal("fallback to an undefined model must fail")
	}
	// routing.local_for is an ordered union
	c := layer(t, "f/f", false, "routing: {local_for: [summaries, commit-messages]}\n")
	m = mustMerge(t, "", a, b, c)
	if strings.Join(m.Routing.LocalFor, ",") != "summaries,commit-messages" {
		t.Fatalf("local_for = %v", m.Routing.LocalFor)
	}
}

func TestLoginsReplaceByProvider(t *testing.T) {
	a := layer(t, "a/a", false, "logins:\n  - {provider: github, reason: first}\n")
	b := layer(t, "b/b", false, "logins:\n  - {provider: github, reason: second, method: oauth}\n  - {provider: claude-code, method: vendor-cli}\n")
	m := mustMerge(t, "", a, b)
	if len(m.Logins) != 2 || m.Logins[0].V.Reason != "second" {
		t.Fatalf("%+v", m.Logins)
	}
}

// The merged model must hash identically regardless of where the rig lives on disk.
func TestHashIsMachineIndependentAndSensitive(t *testing.T) {
	body := "permissions:\n  deny: [{read: '~/.ssh/**'}]\nmcp_servers:\n  s: {command: npx, args: ['-y', 'p@1.0.0']}\n"
	a := layer(t, "a/a", false, body)
	b := layer(t, "a/a", false, body)
	b.Dir = "/completely/different/machine/path"
	ha, _ := mustMerge(t, "claude-code", a).Hash()
	hb, _ := mustMerge(t, "claude-code", b).Hash()
	if ha != hb || len(ha) != 64 {
		t.Fatalf("hash depends on the directory: %s vs %s", ha, hb)
	}
	c := layer(t, "a/a", false, strings.Replace(body, "p@1.0.0", "p@1.0.1", 1))
	if hc, _ := mustMerge(t, "claude-code", c).Hash(); hc == ha {
		t.Fatal("hash must change when content changes")
	}
	// determinism across repeated merges (map iteration order must not leak in)
	for i := 0; i < 20; i++ {
		if h, _ := mustMerge(t, "claude-code", a).Hash(); h != ha {
			t.Fatal("nondeterministic hash")
		}
	}
}

func TestProjectionFiltersByOSAndTargetAndReportsSkips(t *testing.T) {
	l := layer(t, "a/a", false, `skills:
  - {path: skills/mac-only, os: [macos]}
  - {path: skills/all}
  - {path: skills/codex-only, targets: [codex]}
hooks:
  - {id: notify, event: stop, run: {macos: hooks/n.sh, windows: hooks/n.ps1}}
  - {id: linux-only, event: stop, run: 'builtin:x', os: [linux]}
mcp_servers:
  winsrv: {command: npx, args: ['-y', 'p@1.0.0'], os: [windows]}
  anysrv: {command: npx, args: ['-y', 'q@1.0.0']}
permissions:
  deny: [{read: '~/.ssh/**'}, {read: '~/AppData/x', os: [windows]}]
`)
	m := mustMerge(t, "claude-code", l)
	p := m.Project("macos", "claude-code")
	if len(p.Skills) != 2 || p.Skills[0].V.Key() != "mac-only" || p.Skills[1].V.Key() != "all" {
		t.Fatalf("skills = %+v", p.Skills)
	}
	if len(p.MCPServers) != 1 || p.MCPServers[0].Name != "anysrv" {
		t.Fatalf("mcp = %+v", p.MCPServers)
	}
	if len(p.Deny) != 1 {
		t.Fatalf("deny = %v", ruleStrings(p.Deny))
	}
	if len(p.Skipped) == 0 {
		t.Fatal("skipped items must be listed, not silently dropped")
	}
	skipped := map[string]bool{}
	for _, s := range p.Skipped {
		skipped[s.Category+":"+s.Key] = true
	}
	for _, want := range []string{"skill:codex-only", "hook:linux-only", "mcp server:winsrv"} {
		if !skipped[want] {
			t.Errorf("expected skip %q in %v", want, p.Skipped)
		}
	}
	// hook with no version for linux is applicable but unrunnable
	pl := m.Project("linux", "claude-code")
	if len(pl.Unrunnable) != 1 || pl.Unrunnable[0] != "notify" {
		t.Fatalf("unrunnable = %v", pl.Unrunnable)
	}
	if got := len(pl.Permissions().Deny); got != 1 {
		t.Fatalf("Permissions().Deny = %d", got)
	}
}
