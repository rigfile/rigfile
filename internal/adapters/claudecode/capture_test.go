package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

func wr(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const fakeKey = "sk-ant-TESTTESTTESTTESTTEST"

func findings(c *Captured, level, cat string) []string {
	var out []string
	for _, f := range c.Report {
		if f.Level == level && f.Category == cat {
			out = append(out, f.Item+": "+f.Msg)
		}
	}
	return out
}

func TestCaptureEverythingAndRoundTrip(t *testing.T) {
	home := t.TempDir()
	hs := filepath.ToSlash(home) // JSON must not contain raw Windows backslashes
	cd := filepath.Join(home, ".claude")
	wr(t, cd, "CLAUDE.md", "# Mine\nbe terse\n\n<!-- rigfile:begin x sha256=aaaaaaaaaaaa -->\nmanaged\n<!-- rigfile:end x -->\n")
	wr(t, cd, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: PDFs\n---\nbody\n")
	wr(t, cd, "skills/pdf/refs/notes.md", "notes")
	wr(t, cd, "agents/reviewer.md", "---\nname: reviewer\ndescription: r\n---\nx\n")
	wr(t, cd, "commands/hi.md", "hi")
	wr(t, cd, "settings.json", `{
  "env": {"FOO": "bar"},
  "permissions": {
    "deny": ["Read(~/.ssh/**)", "Read(//etc/shadow)", "Read(`+hs+`/private/**)", "Bash(rm -rf *)"],
    "ask": ["Bash(git push*)", "WebFetch(domain:example.com)", "mcp__alpaca__place_stock_order"],
    "allow": ["Bash(git status)", "Bash", "Write(x)"]
  },
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "if": "Bash(git commit*)", "command": "rigfile", "args": ["hook", "run", "guard"]}]},
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "echo hi >> `+hs+`/log.txt"}]}
    ],
    "PostToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "prettier", "args": ["--write", "my file"]}]}],
    "Weird": [{"hooks": [{"type": "command", "command": "x"}]}],
    "Stop": [{"hooks": [{"type": "prompt", "prompt": "p"}]}]
  }
}`)
	wr(t, home, ".claude.json", `{"mcpServers": {
  "alpaca": {"type": "stdio", "command": "npx", "args": ["-y", "alpaca-mcp@1.4.2"], "env": {"ALPACA_API_KEY": "`+fakeKey+`", "REGION": "us"}},
  "wrapped": {"type": "stdio", "command": "/usr/local/bin/rigfile", "args": ["exec", "--secret", "TOKEN_X=w/token", "--env", "A=b", "--", "uvx", "w-mcp==1.0"]},
  "remote": {"type": "http", "url": "https://mcp.example.test/v1", "headers": {"Authorization": "Bearer `+fakeKey+`", "X-Team": "core"}},
  "leaky": {"type": "stdio", "command": "npx", "args": ["--token=`+fakeKey+`"]},
  "local": {"type": "stdio", "command": "uvx", "args": ["tool", "--config", "`+hs+`/proj/cfg.yaml"]},
  "old": {"type": "sse", "url": "https://x.test/sse"},
  "http": {"type": "http", "url": "http://insecure.test"}
}}`)

	c, err := Capture(CaptureOptions{ClaudeDir: cd, ClaudeJSON: filepath.Join(home, ".claude.json"), Home: home, Name: "jia/captured"})
	if err != nil {
		t.Fatal(err)
	}
	// the manifest is schema-valid and parses back
	if err := c.Problems(); err != nil {
		t.Fatalf("captured manifest invalid: %v\n%s", err, c.Manifest)
	}
	m, err := manifest.Parse(c.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	// files: managed region left out, skill tree copied, hook script generated
	if got := string(c.Files["instructions/claude-md.md"]); strings.Contains(got, "managed") || !strings.Contains(got, "be terse") {
		t.Fatalf("instructions: %q", got)
	}
	if _, ok := c.Files["skills/pdf/refs/notes.md"]; !ok {
		t.Fatal("skill subfiles missing")
	}
	if len(m.Agents) != 1 || len(m.Commands) != 1 || len(m.Skills) != 1 || len(m.Instructions) != 1 {
		t.Fatalf("%+v", m)
	}

	// permissions
	var denyRead []string
	for _, r := range m.Permissions.Deny {
		if r.Read != "" {
			denyRead = append(denyRead, r.Read)
		}
	}
	want := []string{"~/.ssh/**", "/etc/shadow", "~/private/**"}
	if strings.Join(denyRead, "|") != strings.Join(want, "|") {
		t.Fatalf("deny read = %v, want %v\n%s\n%+v", denyRead, want, c.Manifest, c.Report)
	}
	if len(m.Permissions.Ask) != 3 || m.Permissions.Ask[2].MCP != "alpaca:place_stock_order" {
		t.Fatalf("ask = %+v", m.Permissions.Ask)
	}
	if len(m.Permissions.Allow) != 1 || m.Permissions.Allow[0].Bash != "git status" {
		t.Fatalf("allow = %+v", m.Permissions.Allow)
	}
	if sk := findings(c, "skipped", "permission"); len(sk) != 2 {
		t.Fatalf("skipped perms: %v", sk)
	}

	// hooks: builtin round-trips, others become scripts with $HOME, unknown/prompt skipped
	if len(m.Hooks) != 3 {
		t.Fatalf("hooks: %+v\n%s", m.Hooks, c.Manifest)
	}
	// events are captured in alphabetical order: PostToolUse first, then the two PreToolUse hooks
	if m.Hooks[1].Run.Single != "builtin:guard" || m.Hooks[1].Match.Tool != "bash" || m.Hooks[1].Match.Command != "git commit*" {
		t.Fatalf("%+v", m.Hooks[1])
	}
	sh := string(c.Files["hooks/"+m.Hooks[2].ID+".sh"])
	if !strings.HasPrefix(sh, "#!/bin/sh\n") || strings.Contains(sh, hs) || !strings.Contains(sh, "$HOME/log.txt") {
		t.Fatalf("script %q", sh)
	}
	if got := string(c.Files["hooks/"+m.Hooks[0].ID+".sh"]); !strings.Contains(got, "prettier --write 'my file'") {
		t.Fatalf("args must be shell-quoted: %q", got)
	}
	if len(findings(c, "skipped", "hook")) != 2 {
		t.Fatalf("%v", findings(c, "skipped", "hook"))
	}
	if len(findings(c, "note", "hook")) != 1 { // Edit|Write matcher
		t.Fatalf("%v", findings(c, "note", "hook"))
	}

	// MCP
	if len(m.MCPServers) != 3 {
		t.Fatalf("servers: %v", m.MCPServers)
	}
	a := m.MCPServers["alpaca"]
	if a.Env["ALPACA_API_KEY"] != "secret://alpaca/alpaca_api_key" || a.Env["REGION"] != "us" || a.Command != "npx" {
		t.Fatalf("%+v", a)
	}
	w := m.MCPServers["wrapped"]
	if w.Command != "uvx" || w.Env["TOKEN_X"] != "secret://w/token" || w.Env["A"] != "b" {
		t.Fatalf("unwrap: %+v", w)
	}
	r := m.MCPServers["remote"]
	if r.Auth != "bearer" || r.BearerToken != "secret://remote/bearer_token" || r.Headers["X-Team"] != "core" {
		t.Fatalf("%+v", r)
	}
	if len(findings(c, "skipped", "mcp")) != 4 { // leaky, local(home path), old(sse), http(insecure)
		t.Fatalf("%v", findings(c, "skipped", "mcp"))
	}

	// NOTHING sensitive leaks anywhere in the output, including the report
	var all strings.Builder
	all.Write(c.Manifest)
	for _, b := range c.Files {
		all.Write(b)
	}
	for _, f := range c.Report {
		all.WriteString(f.Item + f.Msg)
	}
	if strings.Contains(all.String(), "TESTTESTTEST") {
		t.Fatal("a secret value leaked into the capture")
	}
	if len(findings(c, "redacted", "secret")) != 2 {
		t.Fatalf("redactions should be reported: %v", findings(c, "redacted", "secret"))
	}
}

func TestCaptureRefusesUnsafeContent(t *testing.T) {
	home := t.TempDir()
	cd := filepath.Join(home, ".claude")
	wr(t, cd, "CLAUDE.md", "my key is "+fakeKey+"\n")
	wr(t, cd, "skills/ok/SKILL.md", "---\nname: ok\ndescription: d\n---\n")
	wr(t, cd, "skills/leaky/SKILL.md", "---\nname: leaky\ndescription: d\n---\n")
	wr(t, cd, "skills/leaky/notes.txt", "token "+fakeKey)
	wr(t, cd, "skills/creds/SKILL.md", "---\nname: creds\ndescription: d\n---\n")
	wr(t, cd, "skills/creds/.env", "A=1")
	wr(t, cd, "skills/nomd/readme.txt", "x")
	wr(t, cd, "agents/bad.md", fakeKey)
	if err := os.Symlink(filepath.Join(cd, "skills/ok"), filepath.Join(cd, "skills/linked")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(cd, "agents/pw.md")); err != nil {
		t.Skip()
	}
	c, err := Capture(CaptureOptions{ClaudeDir: cd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Parse(c.Manifest)
	if len(m.Skills) != 1 || m.Skills[0].Path != "skills/ok" || len(m.Agents) != 0 || len(m.Instructions) != 0 {
		t.Fatalf("only the clean skill may be captured: %s", c.Manifest)
	}
	if n := len(findings(c, "skipped", "skill")); n != 4 { // leaky, creds, nomd, linked
		t.Fatalf("skill skips: %v", findings(c, "skipped", "skill"))
	}
	if len(findings(c, "skipped", "agent")) != 2 || len(findings(c, "skipped", "instruction")) != 1 {
		t.Fatalf("%+v", c.Report)
	}
	for _, f := range c.Report {
		if strings.Contains(f.Msg+f.Item, "TESTTEST") {
			t.Fatal("report leaks a secret")
		}
	}
}

func TestCaptureSkipsWhatRigfileAlreadyManagesAndEmptyDirs(t *testing.T) {
	home := t.TempDir()
	c, err := Capture(CaptureOptions{ClaudeDir: filepath.Join(home, "nope"), Home: home})
	if err != nil || len(c.Files) != 0 || !strings.Contains(string(c.Manifest), "name: local/my-rig") {
		t.Fatalf("%v %s", err, c.Manifest)
	}
	cd := filepath.Join(home, ".claude")
	wr(t, cd, "agents/mine.md", "---\nname: mine\ndescription: d\n---\n")
	wr(t, cd, "agents/managed.md", "---\nname: managed\ndescription: d\n---\n")
	wr(t, cd, "settings.json", `{"permissions":{"deny":["Bash(rm -rf *)"]},"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"`+filepath.ToSlash(cd)+`/rigfile/hooks/g/x.sh"}]}]}}`)
	c, _ = Capture(CaptureOptions{ClaudeDir: cd, Home: home, Skip: func(cat, key string) bool {
		return (cat == "agent" && key == "managed") || (cat == "permission" && key == "deny:Bash(rm -rf *)")
	}})
	m, _ := manifest.Parse(c.Manifest)
	if len(m.Agents) != 1 || len(m.Permissions.Deny) != 0 || len(m.Hooks) != 0 {
		t.Fatalf("%s", c.Manifest)
	}
}
