package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/githook"
)

// effectiveHooksPath is core.hooksPath as git resolves it from the current directory ("" = unset).
func effectiveHooksPath(e env) string {
	c := exec.Command("git", "config", "--type=path", "--get", "core.hooksPath")
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL"} { // the machine we are checking, not the process's
		if v := e.getenv(k); v != "" {
			c.Env = append(c.Env, k+"="+v)
		}
	}
	c.Env = append(c.Env, "PATH="+e.getenv("PATH"))
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// providerAdvice tells the user where to rotate a credential of a given rule. Values are never involved.
var providerAdvice = []struct{ prefix, where string }{
	{"aws-", "AWS: IAM → Users → Security credentials → deactivate and delete the access key, then create a new one"},
	{"github-", "GitHub: Settings → Developer settings → Personal access tokens → Delete/regenerate the token"},
	{"gitlab-", "GitLab: User settings → Access tokens → Revoke the token"},
	{"slack-", "Slack: api.slack.com/apps → your app → OAuth & Permissions → revoke/rotate the token (webhooks: regenerate the URL)"},
	{"stripe-", "Stripe: Dashboard → Developers → API keys → Roll the key"},
	{"openai-", "OpenAI: platform.openai.com → API keys → Revoke the key"},
	{"anthropic-", "Anthropic: console.anthropic.com → API keys → Delete the key"},
	{"gcp-", "Google Cloud: APIs & Services → Credentials → delete/regenerate the key (service-account keys: IAM → Service accounts → Keys)"},
	{"npm-", "npm: npmjs.com → Access Tokens → Revoke"},
	{"pypi-", "PyPI: Account settings → API tokens → Remove"},
	{"private-key", "Private key: generate a NEW key pair, replace the public key everywhere it is authorised (servers, GitHub, CI), then discard the old one"},
	{"rigfile-url-credentials", "Database/broker password in a URL: change the password on the server, then update the consumers"},
	{"rigfile-netrc-password", "Password in a netrc entry: change it at the service"},
	{"generic-", "Unknown service: find what the value unlocks (variable name, config key) and rotate it there"},
	{"jwt", "JWT: revoke the session/signing key that issued it, or wait for expiry only if it is short-lived and low-privilege"},
}

func adviceFor(rule string) string {
	for _, a := range providerAdvice {
		if strings.HasPrefix(rule, a.prefix) {
			return a.where
		}
	}
	return "Find the service this credential belongs to and revoke it there"
}

// doctorGit is `rigfile doctor --git [repo]`: it reads the whole history (never rewrites it), lists what a
// scanner finds, and walks the user through the only real fix, rotation (plan §8.1d). Values are never
// printed.
func doctorGit(e env, dir string, maxCommits int) int {
	g := githook.Git{Dir: dir}
	sc, err := githook.DefaultScanner()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	res, n, truncated, err := g.ScanHistory(sc, maxCommits)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile: cannot read the repository history:", err)
		return 1
	}
	where := dir
	if where == "" {
		where = "the current repository"
	}
	if truncated {
		fmt.Fprintf(e.out, "note: only the newest %d commits were scanned (--max-commits)\n", n)
	}
	if len(res.Findings) == 0 {
		fmt.Fprintf(e.out, "✔ no secrets found in %d commit(s) of %s\n", n, where)
		return 0
	}
	fmt.Fprintf(e.out, "✘ %d possible secret(s) found in the history of %s (%d commits scanned)\n\n", len(res.Findings), where, n)
	sort.SliceStable(res.Findings, func(i, j int) bool { return res.Findings[i].Path < res.Findings[j].Path })
	rules := map[string]bool{}
	inHead := 0
	for _, f := range res.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		head := "removed since"
		if f.Kind == "content" && g.PresentAtHead(sc, f) {
			head, inHead = "STILL IN HEAD", inHead+1
		}
		fmt.Fprintf(e.out, "  %-42s %-28s first seen in %s   %s   [fp:%s]\n", loc, f.RuleID, f.Commit, head, f.Fingerprint)
		rules[f.RuleID] = true
	}
	var ids []string
	for r := range rules {
		ids = append(ids, r)
	}
	sort.Strings(ids)
	fmt.Fprint(e.out, `
What to do (in this order):

 1. ROTATE every credential below FIRST. Removing it from git does not un-leak it: anyone who cloned, forked,
    or saw a CI log may already have it. Assume it is compromised.
`)
	for _, r := range ids {
		fmt.Fprintf(e.out, "      - %-26s %s\n", r, adviceFor(r))
	}
	fmt.Fprintf(e.out, ` 2. Stop the leak in the code: read the value from an environment variable or a secret reference
    (`+"`rigfile secrets set <name>`"+` + secret://<name>)%s.
 3. Only after rotating, optionally clean history. Rigfile never rewrites history for you. Typical route:
      git clone --mirror <repo> clean.git && cd clean.git
      git filter-repo --invert-paths --path <file>          # or --replace-text expressions.txt
    then coordinate a force-push with everyone who has clones (they must re-clone), and ask the host to purge
    cached views/PRs (GitHub: support ticket for sensitive data removal).
 4. Add the new secrets' files to .gitignore and keep the pre-commit hook on (`+"`rigfile doctor`"+` checks it).
`, map[bool]string{true: " (" + fmt.Sprint(inHead) + " still in the current files)", false: ""}[inHead > 0])
	return 1
}
