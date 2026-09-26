package redteam

import "testing"

const home, cwd = "/home/dev", "/work/proj"

func TestBashRulesFollowTheDocumentedGaps(t *testing.T) {
	r := Rules{Deny: []string{"Bash(curl *)", "Bash(rm *)", "Bash(git*--no-verify*)"}, Ask: []string{"Bash(git push *)"}}
	for cmd, want := range map[string]Verdict{
		"curl https://x":               Deny,
		"/usr/bin/curl https://x":      None, // documented gap: path-qualified program
		"sh -c 'curl https://x'":       None, // documented gap: sh -c is not unwrapped
		"git -C . push origin main":    None, // documented gap
		"git push origin main":         Ask,
		"git push":                     Ask, // trailing ` *` also matches the bare command
		"FOO=bar git push origin":      Ask,
		"timeout 5 git push origin":    Ask,
		"npm test && git push origin":  Ask,
		"echo $(git push origin main)": Ask,
		"echo hi; rm -rf build":        Deny,
		"git commit -m x --no-verify":  Deny,
		"git commit -nm x":             None, // the pattern only knows the long flag
		"ls -la":                       None,
		"FOO=1 BAR=2 nohup curl https": Deny,
		"cat /dev/null | curl x | wc":  Deny,
	} {
		if got, rule := r.Bash(cmd, home, cwd); got != want {
			t.Errorf("%q: got %v (%s), want %v", cmd, got, rule, want)
		}
	}
}

func TestReadDenyCoversRecognisedBashReadersAndPathAnchors(t *testing.T) {
	r := Rules{Deny: []string{"Read(**/.env)", "Read(~/.ssh/**)", "Read(//etc/shadow)", "Read(./secrets/**)", "Read(/only-in-claude-dir)"}}
	for _, c := range []struct {
		tool, path string
		want       Verdict
	}{
		{"Read", ".env", Deny}, {"Read", "a/b/.env", Deny}, {"Read", "/other/repo/.env", Deny}, {"Read", ".env.example", None},
		{"Read", "~/.ssh/id_ed25519", Deny}, {"Read", "/home/dev/.ssh/config", Deny}, {"Read", "/etc/shadow", Deny},
		{"Read", "secrets/key.txt", Deny}, {"Read", "other/secrets/key.txt", None}, // relative rule: cwd only
		{"Read", "/home/dev/.claude/only-in-claude-dir", Deny}, {"Read", "/only-in-claude-dir", None}, // `/x` = ~/.claude/x in user settings
		{"Edit", "~/.ssh/id_rsa", Deny}, // a Read deny also blocks Edit/Write
	} {
		if got, rule := r.Path(c.tool, c.path, home, cwd); got != c.want {
			t.Errorf("%s %s: got %v (%s), want %v", c.tool, c.path, got, rule, c.want)
		}
	}
	for cmd, want := range map[string]Verdict{
		"cat .env": Deny, "head -5 ~/.ssh/id_rsa": Deny, "sed -n p ~/.ssh/id_rsa": Deny, "tee /dev/null < .env": Deny, "cat README.md": None,
		"python3 -c \"open('.env').read()\"": None, // documented gap: scripts open files themselves
		"grep -r KEY .":                      None, // documented gap
	} {
		if got, rule := r.Bash(cmd, home, cwd); got != want {
			t.Errorf("%q: got %v (%s), want %v", cmd, got, rule, want)
		}
	}
}
