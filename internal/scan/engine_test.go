package scan

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Fake credentials are assembled at run time so the repo's own secret scanners (global git hook, CI gitleaks)
// never see a complete token in the source. Every value is made up.
func cat(parts ...string) string { return strings.Join(parts, "") }

var (
	fakeAWS    = cat("AKIA", "QYLPMN5HHHFP", "PRXZ")[:20] // AKIA + 16 chars from [A-Z2-7]
	fakeGH     = cat("ghp_", "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo")
	fakeSlack  = cat("xox", "b-", "123456789012-", "1234567890123-", "aBcDeFgHiJkLmNoPqRsTuVwX")
	fakeStripe = cat("sk_", "live_", "51HqYzKLmNoPqRsTuVwXyZ0123")
	fakeAnth   = cat("sk-", "ant-", "api03-", strings.Repeat("Zk3JqW9xLm2Pn7VbT5cRd8HyAeUsGfXiOoNz1QwEr4TyUiPa_Kd-Ab", 2)[:93], "AA")
	fakePEM    = cat("-----BEGIN ", "RSA PRIVATE KEY-----\n", strings.Repeat("MIIEowIBAAKCAQEAxfake0123456789abcdefghijklmnop\n", 3), "-----END ", "RSA PRIVATE KEY-----\n")
)

func newScanner(t *testing.T, o Options) *Scanner {
	t.Helper()
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEmbeddedRulesMatchRecordedHash(t *testing.T) {
	if got := hashHex(gitleaksTOML); got != strings.TrimSpace(gitleaksSHA) {
		t.Fatalf("embedded gitleaks.toml (%s) differs from gitleaks.toml.sha256: run scripts/update-gitleaks-rules.sh", got)
	}
	if RulesVersion() == "" {
		t.Fatal("VERSION missing")
	}
}

func TestEveryEmbeddedRuleCompilesUnderGoRE2(t *testing.T) {
	rs, err := DefaultRuleset()
	if err != nil {
		t.Fatal(err) // ParseRules fails on ANY rule Go cannot compile: no silent holes
	}
	if rs.NumRules() < 200 {
		t.Fatalf("only %d rules loaded", rs.NumRules())
	}
	if rs.Version != RulesVersion() {
		t.Fatal("version not recorded")
	}
}

func TestDetectsKnownFormatsAndNeverReturnsTheValue(t *testing.T) {
	s := newScanner(t, Options{})
	cases := map[string]string{
		"aws":       "aws_access_key_id = " + fakeAWS + "\n",
		"github":    "token: " + fakeGH + "\n",
		"slack":     "SLACK=" + fakeSlack + "\n",
		"stripe":    "key = \"" + fakeStripe + "\"\n",
		"anthropic": "ANTHROPIC_API_KEY=" + fakeAnth + "\n",
		"pem":       fakePEM,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			got := s.ScanText("config.txt", text)
			if len(got) == 0 {
				t.Fatalf("no finding for %s", name)
			}
			dump := fmt.Sprintf("%+v %v", got, got)
			for _, secret := range []string{fakeAWS, fakeGH, fakeSlack, fakeStripe, fakeAnth, "MIIEowIBAAKCAQEAxfake"} {
				if strings.Contains(dump, secret) {
					t.Fatalf("finding output contains a secret value: %s", dump)
				}
			}
		})
	}
	// the struct itself has no string field that could carry the value
	rt := reflect.TypeOf(Finding{})
	for i := 0; i < rt.NumField(); i++ {
		switch n := rt.Field(i).Name; n {
		case "Kind", "RuleID", "Description", "Path", "Line", "End", "Column", "Fingerprint":
		default:
			t.Fatalf("unexpected Finding field %q: review that it cannot hold a secret", n)
		}
	}
}

func TestLineColumnAndMultiLine(t *testing.T) {
	s := newScanner(t, Options{})
	text := "line one\nline two\ntoken = " + fakeGH + "\nlast\n"
	f := s.ScanText("a.txt", text)
	if len(f) != 1 || f[0].Line != 3 || f[0].Column < 1 {
		t.Fatalf("%+v", f)
	}
	f = s.ScanText("k.pem", "x\n"+fakePEM)
	if len(f) != 1 || f[0].Line != 2 || f[0].End < 4 {
		t.Fatalf("multi-line private key: %+v", f)
	}
}

func TestNegatives(t *testing.T) {
	s := newScanner(t, Options{})
	for _, text := range []string{
		"the quick brown fox\n",
		"commit " + strings.Repeat("a1b2c3d4", 5) + "\n",
		"uuid 123e4567-e89b-12d3-a456-426614174000\n",
		"password = changeme\n",
		"api_key = YOUR_API_KEY_HERE\n",
		"AKIAIOSFODNN7EXAMPLE\n", // gitleaks' own allowlist for AWS docs values
		"token = ${GITHUB_TOKEN}\n",
	} {
		if f := s.ScanText("notes.md", text); len(f) != 0 {
			t.Errorf("false positive on %q: %v", text, f)
		}
	}
}

func TestSensitiveNamesSizeAndBinary(t *testing.T) {
	s := newScanner(t, Options{MaxFileBytes: 1000})
	f := s.ScanFile(".ENV", []byte("A=1\n"))
	if len(f) != 1 || f[0].Kind != "name" {
		t.Fatalf("case-insensitive name: %+v", f)
	}
	if f := s.ScanFile("dir\\id_rsa", []byte("x")); len(f) != 1 || f[0].Path != "dir/id_rsa" {
		t.Fatalf("windows separators: %+v", f)
	}
	if f := s.ScanFile(".env.example", []byte("A=\n")); len(f) != 0 {
		t.Fatalf(".env.example is exempt: %+v", f)
	}
	if f := s.ScanFile("big.txt", []byte(strings.Repeat("x", 2000))); len(f) != 1 || f[0].Kind != "size" {
		t.Fatalf("%+v", f)
	}
	bin := append([]byte("token = "+fakeGH), 0)
	if f := s.ScanFile("blob.dat", bin); len(f) != 0 {
		t.Fatalf("binary content must not be scanned: %+v", f)
	}
	// no name checks when scanning non-file text
	s2 := newScanner(t, Options{SkipNames: true})
	if f := s2.ScanFile(".env", []byte("A=1\n")); len(f) != 0 {
		t.Fatalf("%+v", f)
	}
}

func TestInlineAllowIsOffByDefault(t *testing.T) {
	line := "token = " + fakeGH + "  # gitleaks:allow\n"
	if f := newScanner(t, Options{}).ScanText("a.txt", line); len(f) != 1 {
		t.Fatalf("an inline marker must not suppress a finding by default (an agent can write it): %+v", f)
	}
	if f := newScanner(t, Options{HonorInlineAllow: true}).ScanText("a.txt", line); len(f) != 0 {
		t.Fatalf("%+v", f)
	}
}

func TestGlobalPathAllowlistSkipsLockfiles(t *testing.T) {
	s := newScanner(t, Options{})
	if f := s.ScanText("node_modules/x/index.js", "token = "+fakeGH+"\n"); len(f) != 0 {
		t.Fatalf("gitleaks' global path allowlist applies: %+v", f)
	}
}

func TestAllowFile(t *testing.T) {
	s := newScanner(t, Options{})
	text := "token = " + fakeGH + "\n"
	f := s.ScanText("docs/example.md", text)
	if len(f) != 1 {
		t.Fatal(f)
	}
	a, err := ParseAllow(strings.NewReader(`
# comment
docs/**            # anything under docs
rule:aws-access-token src/*.go
fp:` + f[0].Fingerprint + `
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Filter(f); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	// a different path and rule are not allowed by those entries
	other := s.ScanText("src/app.txt", text)
	if got := (&Allow{}).Filter(other); len(got) != 1 {
		t.Fatal("empty allow list must keep findings")
	}
	a2, _ := ParseAllow(strings.NewReader("rule:github-pat src/*.txt\n"))
	if got := a2.Filter(s.ScanText("src/app.txt", text)); len(got) == len(other) && len(other) > 0 && other[0].RuleID == "github-pat" {
		t.Fatalf("rule-scoped allow should have matched: %+v", got)
	}
	if _, err := ParseAllow(strings.NewReader("fp:xyz\n")); err == nil {
		t.Fatal("bad fingerprint accepted")
	}
	// case-insensitive, name-only glob matches at any depth
	a3, _ := ParseAllow(strings.NewReader("*.PEM\n"))
	if got := a3.Filter([]Finding{{Path: "a/b/Key.pem"}}); len(got) != 0 {
		t.Fatal("basename glob")
	}
}

func TestRulesFileAllowlistConditions(t *testing.T) {
	rs, err := ParseRules([]byte(`
[[rules]]
id = "demo"
description = "demo"
regex = '''demo-([a-z0-9]{12})'''
keywords = ["demo-"]
[[rules.allowlists]]
condition = "AND"
paths = ['''\.md$''']
stopwords = ["placeholder"]
`))
	if err != nil {
		t.Fatal(err)
	}
	s := newScanner(t, Options{Rules: rs})
	if len(s.ScanText("a.go", "demo-abcdef123456")) != 1 {
		t.Fatal("should fire")
	}
	if len(s.ScanText("a.md", "demo-abcdef123456")) != 1 {
		t.Fatal("AND: path matches but no stopword, still fires")
	}
	if len(s.ScanText("a.md", "demo-placeholder0")) != 0 {
		t.Fatal("AND: path and stopword both match, suppressed")
	}
	if _, err := ParseRules([]byte("[[rules]]\nid='x'\nregex='(?=lookahead)'\n")); err == nil {
		t.Fatal("a rule Go cannot compile must be an error, not dropped")
	}
}

func TestPerformanceOnALargeDiff(t *testing.T) {
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector; CI runs this test without -race")
	}
	s := newScanner(t, Options{})
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "+    result_%d = compute(value_%d, config.get(\"option_%d\"), timeout=%d)  # ordinary code\n", i, i, i, i)
	}
	b.WriteString("+ token = " + fakeGH + "\n")
	text := b.String()
	s.ScanText("warmup.txt", "warm up the regexp caches "+fakeGH) // first call pays one-off page-fault costs
	var d time.Duration
	var f []Finding
	for i := 0; i < 3; i++ { // best of three: a scheduler hiccup is not a slow scanner
		start := time.Now()
		f = s.ScanText("big.diff", text)
		if e := time.Since(start); i == 0 || e < d {
			d = e
		}
	}
	if len(f) != 1 {
		t.Fatalf("%+v", f)
	}
	limit := 100 * time.Millisecond
	if os.Getenv("CI") != "" {
		limit = 300 * time.Millisecond // shared runners are slower; the local target stays 100 ms
	}
	if d > limit {
		t.Fatalf("scanning 5001 lines took %v (limit %v)", d, limit)
	}
	t.Logf("5001 lines: %v", d)
}

func BenchmarkScan5kLines(b *testing.B) {
	s, _ := New(Options{})
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "+    result_%d = compute(value_%d, config.get(\"option_%d\"))\n", i, i, i)
	}
	text := sb.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.ScanText("big.diff", text)
	}
}

func TestWindowedMatchesEqualFullScan(t *testing.T) {
	rs, err := DefaultRuleset()
	if err != nil {
		t.Fatal(err)
	}
	var r *rule
	for _, x := range rs.rules {
		if x.id == "generic-api-key" {
			r = x
		}
	}
	if r == nil || r.window == 0 {
		t.Fatal("generic-api-key must be windowed")
	}
	// text with many keyword hits of every shape, long lines, CRLF, hits at start/end, adjacent hits
	lines := []string{
		"api_key = \"Zk3JqW9xLm2Pn7VbT5cR\"",
		"    secret: '" + cat("aB3dE5gH7jK9", "mN1pQ3sT5vX7", "zA9cE1gI3kM5", "oQ7s") + "'",
		"token=" + strings.Repeat("x9Y8z7W6", 4),
		"const authHeader = getAuth() // password: hunter2hunter2hunter2",
		strings.Repeat("filler text without anything interesting ", 40),
		"ACCESS_TOKEN => '" + cat("q1w2e3r4t5y6", "u7i8o9p0a1s2", "d3f4g5h6") + "'",
		"credentials.creds = \"ZmFrZWZha2VmYWtlZmFrZQ0123456789\"\r",
		"passwd:Passw0rdPassw0rdPassw0rd",
	}
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString(lines[i%len(lines)])
		b.WriteString("\n")
		if i%37 == 0 {
			b.WriteString(strings.Repeat("plain ", 200) + "\n")
		}
	}
	b.WriteString("last_secret_key = 'LAST0123456789abcdefLAST'") // no trailing newline
	text := b.String()
	full := r.re.FindAllStringIndex(text, -1)
	win := findMatches(r, text, strings.ToLower(text))
	if len(full) == 0 {
		t.Fatal("test text should produce matches")
	}
	if !reflect.DeepEqual(full, win) {
		t.Fatalf("windowed scan diverges from full scan: %d vs %d matches", len(win), len(full))
	}
}

func TestNonASCIITextKeepsOffsetsAligned(t *testing.T) {
	s := newScanner(t, Options{})
	// U+0130 lower-cases to a different byte length; offsets must not drift for the finding after it
	text := strings.Repeat("İİİİ ünïcödé ", 200) + "\napi_key = \"Zk3JqW9xLm2Pn7VbT5cRd8Hy\"\n"
	f := s.ScanText("u.txt", text)
	if len(f) != 1 || f[0].Line != 2 {
		t.Fatalf("%+v", f)
	}
}
