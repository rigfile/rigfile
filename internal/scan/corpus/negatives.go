package corpus

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

type negFamily struct {
	name string
	n    int
	make func(g gen, i int) (path, content string)
	// strict families must produce ZERO false positives (the .env.example class, plan §8.1a).
	strict bool
}

func pickOf(g gen, xs []string) string { return xs[g.intn(len(xs))] }

func negativeFamilies() []negFamily {
	return []negFamily{
		{name: "git-sha-in-log", n: 25, make: func(g gen, i int) (string, string) {
			return "docs/CHANGELOG.md", fmt.Sprintf("- %s fix parser crash (#%d)\n- %s bump deps\ncommit %s\n", g.pick(hexLower, 7), 100+i, g.pick(hexLower, 7), g.pick(hexLower, 40))
		}},
		{name: "sha256-digest", n: 25, make: func(g gen, i int) (string, string) {
			return "Dockerfile", "FROM golang:1.22@sha256:" + g.pick(hexLower, 64) + " AS build\nRUN echo \"" + g.pick(hexLower, 64) + "  app.tgz\" | sha256sum -c -\n"
		}},
		{name: "checksum-md5", n: 20, make: func(g gen, i int) (string, string) {
			return "release/notes.txt", "checksum: " + g.pick(hexLower, 32) + "\nbuild_id: " + g.pick(hexLower, 32) + "\nrequest_id = " + g.pick(hexLower, 32) + "\n"
		}},
		{name: "uuid-identifiers", n: 25, make: func(g gen, i int) (string, string) {
			return "config/ids.yaml", "client_id: \"" + g.uuid() + "\"\ntenant: " + g.uuid() + "\nrequest-id: " + g.uuid() + "\n"
		}},
		{name: "sri-integrity", n: 20, make: func(g gen, i int) (string, string) {
			return "public/index.html", "<script src=\"/a.js\" integrity=\"sha384-" + g.pick(b64std, 64) + "\" crossorigin></script>\n"
		}},
		{name: "npm-integrity-outside-lockfile", n: 15, make: func(g gen, i int) (string, string) {
			return "docs/deps.md", "left-pad@1.3.0 integrity sha512-" + g.pick(b64std, 86) + "==\n"
		}},
		{name: "base64-image-data-uri", n: 20, make: func(g gen, i int) (string, string) {
			return "site/style.css", ".logo { background: url(data:image/png;base64," + g.pick(b64std, 400) + "); }\n"
		}},
		{name: "public-ssh-key", n: 15, make: func(g gen, i int) (string, string) {
			return "docs/authorized.txt", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI" + g.pick(b64std, 43) + " deploy@example.test\n"
		}},
		{name: "certificate-and-public-key", n: 15, make: func(g gen, i int) (string, string) {
			body := ""
			for k := 0; k < 8; k++ {
				body += g.pick(b64std, 64) + "\n"
			}
			label := pickOf(g, []string{"CERTIFICATE", "PUBLIC KEY", "CERTIFICATE REQUEST"})
			return "certs/chain.txt", "-----BEGIN " + label + "-----\n" + body + "-----END " + label + "-----\n"
		}},
		{name: "placeholders-in-docs", n: 30, make: func(g gen, i int) (string, string) {
			ph := []string{"YOUR_API_KEY", "<your-token>", "xxxxxxxxxxxxxxxx", "${API_TOKEN}", "$SECRET_KEY", "REPLACE_ME", "changeme", "your-secret-here", "{{ secrets.TOKEN }}", "<PASSWORD>"}
			return "README.md", "Set your key:\n\n    export API_KEY=" + pickOf(g, ph) + "\n    password = \"" + pickOf(g, ph) + "\"\n"
		}},
		{name: "env-example", n: 25, strict: true, make: func(g gen, i int) (string, string) {
			return ".env.example", "# copy to .env and fill in\nAPI_KEY=\nSECRET_KEY=change-me\nDATABASE_URL=postgres://user:password@localhost:5432/app\nSTRIPE_KEY=sk_test_xxxxxxxxxxxxxxxxxxxxxxxx\nTOKEN=${TOKEN}\n"
		}},
		{name: "env-var-access-in-code", n: 30, make: func(g gen, i int) (string, string) {
			forms := []string{
				"api_key = os.environ[\"API_KEY\"]\ntoken = os.getenv('SERVICE_TOKEN')\n",
				"const secret = process.env.CLIENT_SECRET\nconst key = process.env.API_KEY ?? ''\n",
				"apiKey := os.Getenv(\"API_KEY\")\npassword := os.Getenv(\"DB_PASSWORD\")\n",
				"password = ENV.fetch('DB_PASSWORD')\ntoken = ENV['GITHUB_TOKEN']\n",
				"let token = std::env::var(\"API_TOKEN\").unwrap();\n",
			}
			return "src/config." + pickOf(g, []string{"py", "ts", "go", "rb", "rs"}), forms[i%len(forms)]
		}},
		{name: "keyword-identifiers", n: 30, make: func(g gen, i int) (string, string) {
			forms := []string{
				"max_tokens = 4096\ntokenizer = AutoTokenizer.from_pretrained(name)\ntoken_count = len(tokens)\n",
				"def authenticate(token, secret_key=None, api_key=None):\n    return check(token)\n",
				"api_version = \"2024-06-01\"\nkey_name = \"user_id\"\nsort_key = \"created_at\"\n",
				"keyboard = Keyboard()\nmonkey_patch(donkey)\nsecret_santa = draw_names(people)\n",
				"password_field = form.password\nauth_header = build_header(scheme)\naccess_level = 'admin'\n",
				"const tokenType = 'Bearer'; const keyMap = new Map(); function getSecretName() { return name }\n",
			}
			return "src/lib.py", forms[i%len(forms)] + "# " + g.pick(lowerAlnum, 8) + "\n"
		}},
		{name: "test-fixture-fake-values", n: 25, make: func(g gen, i int) (string, string) {
			vals := []string{"hunter2", "password123", "test-token", "abcd1234", "secret", "dummy-key", "fake_api_key", "12345678"}
			return "tests/test_auth.py", "PASSWORD = \"" + pickOf(g, vals) + "\"\napi_key = \"" + pickOf(g, vals) + "\"\n"
		}},
		{name: "aws-docs-example", n: 5, make: func(g gen, i int) (string, string) {
			return "docs/aws.md", "aws_access_key_id = " + j("AKIA", "IOSFODNN7", "EXAMPLE") + "\naws_secret_access_key = " + j("wJalrXUtnFEMI/K7MDENG/", "bPxRfiCY", "EXAMPLEKEY") + "\n"
		}},
		{name: "minified-js", n: 20, make: func(g gen, i int) (string, string) {
			var b strings.Builder
			for k := 0; k < 40; k++ {
				b.WriteString(fmt.Sprintf("var %s=function(%s,%s){return %s.%s(%s)};", g.pick(letters, 2), g.pick(letters, 1), g.pick(letters, 1), g.pick(letters, 1), g.pick(lowerAlnum, 5), g.pick(letters, 1)))
			}
			return "static/app.min.txt", b.String() + "\n"
		}},
		{name: "css-colors-and-hex-tables", n: 15, make: func(g gen, i int) (string, string) {
			var b strings.Builder
			for k := 0; k < 30; k++ {
				b.WriteString(fmt.Sprintf(".c%d { color: #%s; border: 1px solid #%s }\n", k, g.pick(hexLower, 6), g.pick(hexLower, 3)))
			}
			return "static/theme.css", b.String()
		}},
		{name: "guid-in-project-files", n: 15, make: func(g gen, i int) (string, string) {
			return "App.csproj", "<ProjectGuid>{" + strings.ToUpper(g.uuid()) + "}</ProjectGuid>\n<PackageId>Company.Product</PackageId>\n"
		}},
		{name: "urls-and-emails", n: 20, make: func(g gen, i int) (string, string) {
			return "docs/contact.md", "See https://docs.example.test/api/v1/tokens/" + g.pick(lowerAlnum, 6) + " or mail support@example.test. Token docs: https://example.test/keys\n"
		}},
		{name: "go-and-python-hash-code", n: 20, make: func(g gen, i int) (string, string) {
			return "pkg/hash.go", "// digest is the hex sha1 of the input\nvar digest = \"" + g.pick(hexLower, 40) + "\"\nfunc cacheKey(id string) string { return \"user:\" + id }\n"
		}},
		{name: "word-identifiers-as-values", n: 40, make: func(g gen, i int) (string, string) {
			words := []string{"authorization", "backend", "configuration", "endpoint", "middleware", "session", "service", "manager", "provider", "handler", "factory",
				"request", "response", "client", "server", "database", "connection", "timeout", "retry", "policy", "default", "primary", "secondary", "internal",
				"external", "public", "header", "payload", "context", "version", "identifier", "reference", "resource", "template", "cache", "storage", "network"}
			lhs := []string{"api_key", "secret", "auth_token", "password_field", "access_key_name", "token_type", "credentials_provider", "secret_key_name"}
			n := 2 + g.intn(3)
			var parts []string
			for k := 0; k < n; k++ {
				parts = append(parts, pickOf(g, words))
			}
			var val string
			switch i % 3 {
			case 0:
				val = strings.Join(parts, "_")
			case 1:
				val = strings.Join(parts, "-")
			default:
				val = parts[0]
				for _, p := range parts[1:] {
					val += strings.ToUpper(p[:1]) + p[1:]
				}
			}
			return "src/settings.py", pickOf(g, lhs) + " = \"" + val + "\"\n"
		}},
		{name: "hashes-and-ids-near-keywords", n: 60, make: func(g gen, i int) (string, string) {
			const bcryptSet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
			switch i % 6 {
			case 0:
				return "db/seed.sql", "INSERT INTO users (name, password_hash) VALUES ('alice', '$2b$12$" + g.pick(bcryptSet, 53) + "');\n"
			case 1:
				return "tests/fixtures.py", "USER = {\"email\": \"a@example.test\", \"password_hash\": \"$argon2id$v=19$m=65536,t=3,p=4$" + g.pick(b64std, 22) + "$" + g.pick(b64std, 43) + "\"}\n"
			case 2:
				return "src/cache.py", "cache_key = f\"user:{user_id}:profile:" + g.pick(hexLower, 8) + "\"\nsession_key = \"" + g.pick(lowerAlnum, 10) + "\"\n"
			case 3:
				return "config/keys.yaml", "key_id: " + g.uuid() + "\nkey_fingerprint: " + g.pick(hexLower, 40) + "\napi_key_id: \"" + g.pick(hexLower, 16) + "\"\n"
			case 4:
				return "src/tokens.json", "{\"token_hash\": \"" + g.pick(hexLower, 64) + "\", \"secret_hash\": \"" + g.pick(hexLower, 40) + "\"}\n"
			default:
				return "docs/api.md", "The `token` field is a UUID such as " + g.uuid() + ". The `key` is your project id, e.g. proj_" + g.pick(lowerAlnum, 12) + ".\n"
			}
		}},
		{name: "base64-of-plain-text", n: 20, make: func(g gen, i int) (string, string) {
			msgs := []string{"build finished in 42 seconds with 3 warnings and no errors", "hello world, this is a harmless configuration comment line",
				"{\"name\": \"demo\", \"version\": \"1.2.3\", \"private\": true}", "SELECT id, name FROM users WHERE active = true ORDER BY name"}
			return "fixtures/blob.txt", "payload: " + b64(msgs[i%len(msgs)]+" "+g.pick(lowerAlnum, 6)) + "\n"
		}},
		{name: "secretsmanager-usage", n: 15, make: func(g gen, i int) (string, string) {
			return "infra/main.tf", "data \"aws_secretsmanager_secret_version\" \"db\" {\n  secret_id = \"prod/db/password\"\n}\nresource \"x\" \"y\" { password = data.aws_secretsmanager_secret_version.db.secret_string }\n"
		}},
	}
}

// Negatives returns the hard-negative samples.
func Negatives(seed uint64) []Sample {
	g := gen{rand.New(rand.NewPCG(seed^0x5bd1e995, seed+17))}
	var out []Sample
	for _, f := range negativeFamilies() {
		for i := 0; i < f.n; i++ {
			path, content := f.make(g, i)
			tier := "core"
			if f.strict {
				tier = "strict"
			}
			out = append(out, Sample{ID: fmt.Sprintf("%s#%d", f.name, i), Family: f.name, Positive: false, Tier: tier, Path: path, Content: content})
		}
	}
	return out
}

// All returns positives followed by negatives.
func All(seed uint64) []Sample { return append(Positives(seed), Negatives(seed)...) }

// ---------------------------------------------------------------------------------------------------------
// file names

// NameCase is one file-name expectation for the blocklist.
type NameCase struct {
	Path    string
	Blocked bool
	Note    string
}

// Names is the file-name corpus (plan §8.1a): names that must be blocked and look-alikes that must not.
func Names() []NameCase {
	blocked := []string{
		".env", ".ENV", "prod/.env.production", "apps/api/.env.local", "id_rsa", "keys/id_ed25519", "id_ecdsa", "server.pem", "tls.key", "cert.p12", "cert.pfx",
		"release.jks", "app.keystore", "putty.ppk", "secrets.yml", "config/secrets.yaml", "terraform.tfstate", "terraform.tfstate.backup", "prod.tfvars",
		"service-account-prod.json", "gcp-credentials.json", "my-credentials-2024.json", "client_secret_123456.json", "token.json", ".npmrc", ".pypirc", ".netrc", "_netrc",
		".htpasswd", ".aws/credentials", ".gcp/key.txt", ".azure/config", "kubeconfig", "dev.kubeconfig", "firebase-adminsdk-ab12c.json", "ci-sa.json", "db.secret",
		"C:\\Users\\dev\\proj\\.env", "src\\deploy\\id_rsa", ".ssh/config-backup", ".gnupg/pubring.txt",
	}
	notBlocked := []string{
		".env.example", ".env.sample", ".env.template", ".env.dist", "env.py", "environment.ts", "keyboard.ts", "README.md", "src/tokens.py", "id_rsa.pub", "deploy_key.pub",
		"docs/secrets-management.md", "monkey.md", "package.json", "credentials.md", "pemfile.txt", "keys.go", "token_test.go", "service-account.md", "config/app.yaml",
		"prod.example.tfvars", "src/private_key_utils.py", "Makefile", "tsconfig.json", "notes/keystone.txt", "certs/ca.crt", "secrets_test.go", ".gitignore", "kubeconfig.md",
	}
	var out []NameCase
	for _, p := range blocked {
		out = append(out, NameCase{Path: p, Blocked: true})
	}
	for _, p := range notBlocked {
		out = append(out, NameCase{Path: p})
	}
	return out
}
