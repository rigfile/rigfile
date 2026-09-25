package claudecode

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

func plat(t *testing.T, goos string, env map[string]string) *platform.Info {
	t.Helper()
	pi, err := platform.New(platform.Options{GOOS: goos, GOARCH: "arm64", Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	return pi
}

var mac = map[string]string{"HOME": "/Users/test"}

func TestTranslateRule(t *testing.T) {
	win := map[string]string{"USERPROFILE": `C:\Users\test`, "APPDATA": `C:\Users\test\AppData\Roaming`}
	tests := []struct {
		name string
		goos string
		env  map[string]string
		rule manifest.PermissionRule
		want string
		skip bool
		err  bool
	}{
		{"home tilde", "darwin", mac, manifest.PermissionRule{Read: "~/.ssh/**"}, "Read(~/.ssh/**)", false, false},
		{"HOME var", "linux", map[string]string{"HOME": "/home/t"}, manifest.PermissionRule{Read: "${HOME}/.aws/**"}, "Read(~/.aws/**)", false, false},
		{"absolute path needs // anchor", "linux", map[string]string{"HOME": "/home/t"}, manifest.PermissionRule{Read: "/etc/ssl/private/**"}, "Read(//etc/ssl/private/**)", false, false},
		{"project relative glob", "darwin", mac, manifest.PermissionRule{Read: "**/.env"}, "Read(**/.env)", false, false},
		{"config dir under home (mac)", "darwin", mac, manifest.PermissionRule{Read: "${CONFIG_DIR}/gh/**"}, "Read(~/.config/gh/**)", false, false},
		{"app data on mac", "darwin", mac, manifest.PermissionRule{Edit: "${APP_DATA}/Claude/**"}, "Edit(~/Library/Application Support/Claude/**)", false, false},
		{"app data on windows normalises to /c/", "windows", win, manifest.PermissionRule{Read: "${APP_DATA}/Claude/**"}, "Read(~/AppData/Roaming/Claude/**)", false, false},
		{"bash", "darwin", mac, manifest.PermissionRule{Bash: "git push*"}, "Bash(git push*)", false, false},
		{"web fetch", "darwin", mac, manifest.PermissionRule{WebFetch: "example.com"}, "WebFetch(domain:example.com)", false, false},
		{"mcp server", "darwin", mac, manifest.PermissionRule{MCP: "github"}, "mcp__github", false, false},
		{"mcp tool", "darwin", mac, manifest.PermissionRule{MCP: "github:create_issue"}, "mcp__github__create_issue", false, false},
		{"os filter skips", "darwin", mac, manifest.PermissionRule{Bash: "x", OS: []string{"windows"}}, "", true, false},
		{"os filter keeps", "windows", win, manifest.PermissionRule{Bash: "x", OS: []string{"windows"}}, "Bash(x)", false, false},
		{"target filter skips", "darwin", mac, manifest.PermissionRule{Bash: "x", Targets: []string{"codex"}}, "", true, false},
		{"unknown variable", "darwin", mac, manifest.PermissionRule{Read: "${NOPE}/x"}, "", false, true},
		{"empty rule", "darwin", mac, manifest.PermissionRule{}, "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := TranslateRule(plat(t, tc.goos, tc.env), tc.rule)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error=%v", err, tc.err)
			}
			if tc.err {
				return
			}
			if ok == tc.skip {
				t.Fatalf("ok = %v, want skipped=%v", ok, tc.skip)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

const settings = `{
  "permissions": {
    "allow": [
      "Bash(npm run *)"
    ],
    "deny": [
      "Bash(git push*)"
    ]
  },
  "model": "sonnet"
}
`

func rules(kind string, vals ...string) []manifest.PermissionRule {
	var out []manifest.PermissionRule
	for _, v := range vals {
		r := manifest.PermissionRule{}
		switch kind {
		case "read":
			r.Read = v
		case "bash":
			r.Bash = v
		}
		out = append(out, r)
	}
	return out
}

func TestPlanAddsMissingAndNeverRemoves(t *testing.T) {
	pi := plat(t, "darwin", mac)
	perms := manifest.Permissions{
		Deny: append(rules("read", "~/.ssh/**", "**/.env"), rules("bash", "git push*")...), // last one already present
		Ask:  rules("bash", "git push --force*"),
	}
	plan, err := PlanPermissions(pi, []byte(settings), perms)
	if err != nil {
		t.Fatal(err)
	}
	wantAdds := []Change{{"deny", "Read(~/.ssh/**)"}, {"deny", "Read(**/.env)"}, {"ask", "Bash(git push --force*)"}}
	if !reflect.DeepEqual(plan.Adds, wantAdds) {
		t.Fatalf("adds = %v\nwant %v", plan.Adds, wantAdds)
	}
	if len(plan.Present) != 1 || plan.Present[0].Rule != "Bash(git push*)" {
		t.Fatalf("present = %v", plan.Present)
	}
	out, err := plan.Apply([]byte(settings))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	p := m["permissions"].(map[string]any)
	if !reflect.DeepEqual(p["allow"], []any{"Bash(npm run *)"}) {
		t.Fatalf("existing allow changed: %v", p["allow"])
	}
	if got := p["deny"].([]any); len(got) != 3 || got[0] != "Bash(git push*)" {
		t.Fatalf("deny = %v (existing entry must stay first)", got)
	}
	if !strings.Contains(string(out), `"model": "sonnet"`) {
		t.Fatal("unrelated setting lost")
	}
	// Idempotent: planning again against the result is empty.
	again, _ := PlanPermissions(pi, out, perms)
	if !again.Empty() {
		t.Fatalf("second plan should be empty, got %v", again.Adds)
	}
}

// merge-semantics.md §7 example A: an allow shadowed by a deny is dropped and reported.
func TestShadowedAllowIsDroppedAndReported(t *testing.T) {
	pi := plat(t, "darwin", mac)
	perms := manifest.Permissions{
		Deny:  rules("read", "~/.ssh/**"),
		Allow: append(rules("read", "~/.ssh/config"), rules("bash", "npm test*")...),
	}
	plan, err := PlanPermissions(pi, []byte(`{}`), perms)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Dropped) != 1 || plan.Dropped[0].Rule != "Read(~/.ssh/config)" || !strings.Contains(plan.Dropped[0].Reason, "Read(~/.ssh/**)") {
		t.Fatalf("dropped = %+v", plan.Dropped)
	}
	for _, c := range plan.Adds {
		if c.Rule == "Read(~/.ssh/config)" {
			t.Fatal("shadowed allow must not be added")
		}
	}
	// The unrelated allow is kept.
	found := false
	for _, c := range plan.Adds {
		if c.List == "allow" && c.Rule == "Bash(npm test*)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unrelated allow missing: %v", plan.Adds)
	}
}

func TestAllowShadowedByExistingDeny(t *testing.T) {
	pi := plat(t, "darwin", mac)
	plan, _ := PlanPermissions(pi, []byte(settings), manifest.Permissions{Allow: rules("bash", "git push*")})
	if len(plan.Adds) != 0 || len(plan.Dropped) != 1 {
		t.Fatalf("allow of an already-denied rule must be dropped: %+v", plan)
	}
}

func TestOverlappingButUnprovableIsKept(t *testing.T) {
	// deny Read(~/.ssh/**) vs allow Read(~/.ssh*) is not provably covered: keep both, let Claude decide.
	pi := plat(t, "darwin", mac)
	plan, _ := PlanPermissions(pi, []byte(`{}`), manifest.Permissions{Deny: rules("read", "~/.ssh/**"), Allow: rules("read", "~/.ss*")})
	if len(plan.Dropped) != 0 || len(plan.Adds) != 2 {
		t.Fatalf("unprovable overlap must not be dropped: %+v", plan)
	}
}

func TestCreatesPermissionsInEmptyOrMissing(t *testing.T) {
	pi := plat(t, "darwin", mac)
	perms := manifest.Permissions{Deny: rules("read", "**/.env")}
	for _, doc := range []string{``, `{}`, "{\n  \"model\": \"x\"\n}\n"} {
		plan, err := PlanPermissions(pi, []byte(doc), perms)
		if err != nil || len(plan.Adds) != 1 {
			t.Fatalf("doc %q: %v %+v", doc, err, plan)
		}
		out, err := plan.Apply([]byte(doc))
		if err != nil || !json.Valid(out) || !strings.Contains(string(out), `"Read(**/.env)"`) {
			t.Fatalf("doc %q: apply: %v\n%s", doc, err, out)
		}
	}
}

func TestWrongShapeIsAnError(t *testing.T) {
	pi := plat(t, "darwin", mac)
	if _, err := PlanPermissions(pi, []byte(`{"permissions":{"deny":"nope"}}`), manifest.Permissions{}); err == nil {
		t.Fatal("deny as a string must be an error, not silently replaced")
	}
	if _, err := PlanPermissions(pi, []byte(`{"permissions":`), manifest.Permissions{}); err == nil {
		t.Fatal("invalid JSON must be an error")
	}
}
