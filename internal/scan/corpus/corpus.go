// Package corpus generates the labelled test corpus for the secret scanner (plan §12 Stage 2, S2-M2).
//
// Nothing in this package (or the repo) contains a complete credential: every positive sample is assembled
// at run time from a public prefix plus a deterministic pseudo-random body, so the repo's own scanners (the
// global git hook, CI gitleaks) stay quiet and no real secret can be mistaken for a fixture. Every value is
// made up; none has ever been valid anywhere.
//
// Samples are deterministic for a given seed. Positives are labelled by family and tier:
//
//	core     formats the scanner must always find (the 100% recall gate)
//	stretch  known-hard cases we do not handle yet (none at the moment); reported, not gated
//
// Negatives are hard negatives: text that looks secret-shaped or mentions secrets without containing one.
package corpus

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
)

// Sample is one labelled file.
type Sample struct {
	ID       string
	Family   string
	Positive bool
	Tier     string // "core" | "stretch" (positives only)
	Path     string
	Content  string
}

const (
	alnum      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	lowerAlnum = "abcdefghijklmnopqrstuvwxyz0123456789"
	hexLower   = "0123456789abcdef"
	b64url     = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	b64std     = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	base32u    = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	digits     = "0123456789"
	letters    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

type gen struct{ r *rand.Rand }

func (g gen) pick(set string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(set[g.r.IntN(len(set))])
	}
	return b.String()
}

// rich is like pick but retries until the body has healthy entropy, so a generator never produces a value
// that no sane scanner should flag (e.g. "aaaaaaaa"). Entropy is measured the way gitleaks does.
func (g gen) rich(set string, n int) string {
	for {
		s := g.pick(set, n)
		if n < 12 || entropy(s) >= 3.5 {
			return s
		}
	}
}

func (g gen) uuid() string {
	h := g.pick(hexLower, 32)
	return h[:8] + "-" + h[8:12] + "-4" + h[13:16] + "-a" + h[17:20] + "-" + h[20:]
}

func (g gen) intn(n int) int { return g.r.IntN(n) }

func entropy(s string) (e float64) {
	if s == "" {
		return 0
	}
	c := map[rune]int{}
	for _, r := range s {
		c[r]++
	}
	inv := 1.0 / float64(len(s))
	for _, n := range c {
		f := float64(n) * inv
		e -= f * math.Log2(f)
	}
	return e
}

func j(parts ...string) string { return strings.Join(parts, "") }

// ---------------------------------------------------------------------------------------------------------
// positives

type wrapper func(tok string, g gen) (path, content string)

// generic contexts: a token in the places tokens really live.
var contexts = []wrapper{
	func(t string, _ gen) (string, string) { return ".env", "SERVICE_TOKEN=" + t + "\n" },
	func(t string, _ gen) (string, string) {
		return "config/app.yaml", "service:\n  credentials: \"" + t + "\"\n"
	},
	func(t string, _ gen) (string, string) {
		return "src/client.py", "from svc import Client\nclient = Client(token=\"" + t + "\")\n"
	},
	func(t string, _ gen) (string, string) {
		return "scripts/deploy.sh", "#!/bin/sh\ncurl -H \"Authorization: Bearer " + t + "\" https://api.example.test/v1/items\n"
	},
	func(t string, _ gen) (string, string) { return "README.md", "Use this token to log in: " + t + "\n" },
	func(t string, _ gen) (string, string) { return "main.go", "const apiKey = \"" + t + "\"\n" },
	func(t string, _ gen) (string, string) { return "settings.json", "{\n  \"token\": \"" + t + "\"\n}\n" },
	func(t string, _ gen) (string, string) {
		return "Dockerfile", "FROM alpine\nENV SERVICE_TOKEN=" + t + "\n"
	},
}

type family struct {
	name   string
	tier   string
	n      int // variants
	token  func(g gen) string
	custom wrapper // context-dependent formats supply their own surrounding text; token unused
}

func positiveFamilies() []family {
	return []family{
		{name: "aws-access-key-id", tier: "core", n: 8, token: func(g gen) string { return j("AKIA", g.rich(base32u, 16)) }},
		{name: "aws-secret-access-key", tier: "core", n: 6, custom: func(_ string, g gen) (string, string) {
			return ".aws/config-backup", "[default]\naws_access_key_id = " + j("AKIA", g.rich(base32u, 16)) + "\naws_secret_access_key = " + g.rich(b64std, 40) + "\n"
		}},
		{name: "gcp-api-key", tier: "core", n: 8, token: func(g gen) string { return j("AI", "za", g.rich(b64url, 35)) }},
		{name: "gcp-service-account-json", tier: "core", n: 4, custom: func(_ string, g gen) (string, string) {
			key := j("-----BEGIN ", "PRIVATE KEY-----\\n") + g.rich(b64std, 64) + "\\n" + g.rich(b64std, 64) + j("\\n-----END ", "PRIVATE KEY-----\\n")
			return "deploy/sa.txt", "{\n  \"type\": \"service_account\",\n  \"project_id\": \"demo-project\",\n  \"private_key_id\": \"" + g.pick(hexLower, 40) +
				"\",\n  \"private_key\": \"" + key + "\",\n  \"client_email\": \"ci@demo-project.iam.example.test\"\n}\n"
		}},
		{name: "anthropic-api-key", tier: "core", n: 8, token: func(g gen) string { return j("sk-", "ant-", "api03-", g.rich(b64url, 93), "AA") }},
		{name: "openai-api-key", tier: "core", n: 8, token: func(g gen) string {
			return j("sk-", "proj-", g.rich(b64url, 58), "T3Blbk", "FJ", g.rich(b64url, 58))
		}},
		{name: "openai-legacy-key", tier: "core", n: 4, token: func(g gen) string { return j("sk-", g.rich(alnum, 20), "T3Blbk", "FJ", g.rich(alnum, 20)) }},
		{name: "github-pat-classic", tier: "core", n: 8, token: func(g gen) string { return j("gh", "p_", g.rich(alnum, 36)) }},
		{name: "github-fine-grained-pat", tier: "core", n: 6, token: func(g gen) string { return j("github", "_pat_", g.rich(alnum, 82)) }},
		{name: "github-oauth-app", tier: "core", n: 4, token: func(g gen) string { return j("gh", "o_", g.rich(alnum, 36)) }},
		{name: "gitlab-pat", tier: "core", n: 4, token: func(g gen) string { return j("gl", "pat-", g.rich(b64url, 20)) }},
		{name: "slack-bot-token", tier: "core", n: 6, token: func(g gen) string {
			return j("xo", "xb-", g.pick(digits, 12), "-", g.pick(digits, 13), "-", g.rich(alnum, 24))
		}},
		{name: "slack-user-token", tier: "core", n: 4, token: func(g gen) string {
			return j("xo", "xp-", g.pick(digits, 12), "-", g.pick(digits, 12), "-", g.pick(digits, 13), "-", g.rich(lowerAlnum, 32))
		}},
		{name: "slack-webhook", tier: "core", n: 4, token: func(g gen) string {
			return j("https://hooks", ".slack.com/services/", "T", g.pick(base32u, 10), "/B", g.pick(base32u, 10), "/", g.rich(alnum, 24))
		}},
		{name: "stripe-secret-key", tier: "core", n: 6, token: func(g gen) string { return j("sk", "_live_", g.rich(alnum, 24)) }},
		{name: "stripe-restricted-key", tier: "core", n: 4, token: func(g gen) string { return j("rk", "_live_", g.rich(alnum, 24)) }},
		{name: "npm-token", tier: "core", n: 6, token: func(g gen) string { return j("npm", "_", g.rich(alnum, 36)) }},
		{name: "pypi-token", tier: "core", n: 4, token: func(g gen) string { return j("py", "pi-AgEIcHlwaS5vcmc", g.rich(b64url, 70)) }},
		{name: "twilio-api-key", tier: "core", n: 4, token: func(g gen) string { return j("S", "K", g.rich(hexLower, 32)) }},
		{name: "sendgrid-api-key", tier: "core", n: 4, token: func(g gen) string { return j("S", "G.", g.rich(b64url, 22), ".", g.rich(b64url, 43)) }},
		{name: "shopify-token", tier: "core", n: 4, token: func(g gen) string { return j("shp", "at_", g.rich(hexLower, 32)) }},
		{name: "digitalocean-pat", tier: "core", n: 4, token: func(g gen) string { return j("do", "p_v1_", g.rich(hexLower, 64)) }},
		{name: "huggingface-token", tier: "core", n: 4, token: func(g gen) string { return j("h", "f_", g.pick(letters, 34)) }},
		{name: "databricks-token", tier: "core", n: 4, token: func(g gen) string { return j("da", "pi", g.rich(hexLower, 32)) }},
		{name: "linear-api-key", tier: "core", n: 4, token: func(g gen) string { return j("lin", "_api_", g.rich(lowerAlnum, 40)) }},
		{name: "doppler-token", tier: "core", n: 3, token: func(g gen) string { return j("dp", ".pt.", g.rich(lowerAlnum, 43)) }},
		{name: "pulumi-token", tier: "core", n: 3, token: func(g gen) string { return j("pu", "l-", g.rich(hexLower, 40)) }},
		{name: "age-secret-key", tier: "core", n: 3, token: func(g gen) string {
			return j("AGE-SECRET-KEY-", "1", g.rich("QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L", 58))
		}},
		{name: "jwt", tier: "core", n: 6, token: func(g gen) string {
			return j("ey", "JhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.", "ey", g.rich(b64url, 40), ".", g.rich(b64url, 43))
		}},
		{name: "private-key-rsa", tier: "core", n: 4, custom: pem("RSA PRIVATE KEY", "keys/deploy")},
		{name: "private-key-ec", tier: "core", n: 3, custom: pem("EC PRIVATE KEY", "keys/signing")},
		{name: "private-key-openssh", tier: "core", n: 3, custom: pem("OPENSSH PRIVATE KEY", "keys/host")},
		{name: "private-key-pkcs8", tier: "core", n: 3, custom: pem("PRIVATE KEY", "keys/service")},
		{name: "private-key-pgp", tier: "core", n: 2, custom: pem("PGP PRIVATE KEY BLOCK", "keys/release")},
		{name: "db-url-with-password", tier: "core", n: 10, custom: func(_ string, g gen) (string, string) {
			schemes := []string{"postgres", "postgresql", "mysql", "mongodb+srv", "redis", "amqp"}
			s := schemes[g.intn(len(schemes))]
			return "config/database.yml", "production:\n  url: " + s + "://app_user:" + g.rich(alnum, 20+g.intn(8)) + "@db.internal.example.test:5432/app\n"
		}},
		{name: "basic-auth-url", tier: "core", n: 4, custom: func(_ string, g gen) (string, string) {
			return "scripts/fetch.sh", "wget https://deploy:" + g.rich(alnum, 22) + "@repo.example.test/artifacts/app.tgz\n"
		}},
		{name: "netrc-password", tier: "core", n: 3, custom: func(_ string, g gen) (string, string) {
			return "home/netrc-backup", "machine api.example.test\n  login deploy\n  password " + g.rich(alnum, 24) + "\n"
		}},
		{name: "npmrc-auth-token", tier: "core", n: 3, custom: func(_ string, g gen) (string, string) {
			return "config/npmrc-backup", "//registry.example.test/:_authToken=" + g.rich(alnum, 40) + "\n"
		}},
		{name: "generic-hex-secret", tier: "core", n: 8, custom: func(_ string, g gen) (string, string) {
			return "settings.py", "SECRET_KEY = '" + g.rich(hexLower, 50+g.intn(20)) + "'\n"
		}},
		{name: "generic-password-config", tier: "core", n: 8, custom: func(_ string, g gen) (string, string) {
			return "config/app.ini", "[db]\nhost = localhost\npassword = " + g.rich(alnum, 16+g.intn(10)) + "\n"
		}},
		{name: "generic-api-key-env", tier: "core", n: 8, custom: func(_ string, g gen) (string, string) {
			return ".env.production", "API_KEY=" + g.rich(alnum, 32+g.intn(12)) + "\nDEBUG=false\n"
		}},
		{name: "mailgun-key", tier: "core", n: 3, custom: func(_ string, g gen) (string, string) {
			return "config/mail.env", "MAILGUN_API_KEY=" + j("key", "-", g.rich(hexLower, 32)) + "\n"
		}},
		{name: "datadog-key", tier: "core", n: 3, custom: func(_ string, g gen) (string, string) {
			return "config/monitoring.yaml", "datadog_api_key: " + g.rich(lowerAlnum, 40) + "\n"
		}},
		{name: "k8s-secret-manifest", tier: "core", n: 3, custom: func(_ string, g gen) (string, string) {
			return "k8s/secret.yaml", "apiVersion: v1\nkind: Secret\nmetadata:\n  name: app\ndata:\n  password: " + g.rich(b64std, 24) + "==\n  api_key: " + g.rich(b64std, 40) + "\n"
		}},
		// ---- encoded secrets (scanner decodes base64/hex/percent segments; was a measured 0/10 gap before decode.go)
		{name: "encoded-base64-github", tier: "core", n: 4, custom: func(_ string, g gen) (string, string) {
			return "config/blob.txt", "payload: " + b64(j("token=", "gh", "p_", g.rich(alnum, 36))) + "\n"
		}},
		{name: "encoded-hex-aws", tier: "core", n: 3, custom: func(_ string, g gen) (string, string) {
			return "config/blob.txt", "data = \"" + hexEnc(j("AKIA", g.rich(base32u, 16))) + "\"\n"
		}},
		{name: "encoded-percent-slack", tier: "core", n: 3, custom: func(_ string, g gen) (string, string) {
			t := j("xo", "xb-", g.pick(digits, 12), "-", g.pick(digits, 13), "-", g.rich(alnum, 24))
			return "config/url.txt", "https://example.test/cb?t=" + strings.ReplaceAll(t, "-", "%2D") + "\n"
		}},
	}
}

func pem(label, path string) wrapper {
	return func(_ string, g gen) (string, string) {
		var b strings.Builder
		b.WriteString("-----BEGIN " + label + "-----\n")
		for i := 0; i < 6+g.intn(6); i++ {
			b.WriteString(g.rich(b64std, 64) + "\n")
		}
		b.WriteString("-----END " + label + "-----\n")
		return path, b.String()
	}
}

func b64(s string) string {
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for i := 0; i < len(s); i += 3 {
		var n uint32
		k := 0
		for ; k < 3 && i+k < len(s); k++ {
			n |= uint32(s[i+k]) << (16 - 8*uint(k))
		}
		for x := 0; x < 4; x++ {
			if x <= k {
				out.WriteByte(enc[(n>>(18-6*uint(x)))&63])
			} else {
				out.WriteByte('=')
			}
		}
	}
	return out.String()
}

func hexEnc(s string) string { return fmt.Sprintf("%x", s) }

// ---------------------------------------------------------------------------------------------------------

// Positives returns the positive samples.
func Positives(seed uint64) []Sample {
	g := gen{rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
	var out []Sample
	for _, f := range positiveFamilies() {
		for i := 0; i < f.n; i++ {
			var path, content string
			if f.custom != nil {
				path, content = f.custom("", g)
			} else {
				ctx := contexts[i%len(contexts)]
				path, content = ctx(f.token(g), g)
			}
			out = append(out, Sample{ID: fmt.Sprintf("%s#%d", f.name, i), Family: f.name, Positive: true, Tier: f.tier, Path: path, Content: content})
		}
	}
	return out
}
