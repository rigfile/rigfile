package scan

import (
	"strings"
	"testing"
)

func TestIsSensitiveFilename(t *testing.T) {
	yes := []string{".env", "app/.env", ".env.production", "certs/server.pem", "id_rsa", "home/.ssh/id_ed25519", "x/credentials.json",
		"gcp-credentials-prod.json", "svc-sa.json", "terraform.tfstate", "terraform.tfstate.backup", "prod.tfvars", ".npmrc", "a/.netrc",
		"secrets.yml", "app.secret", ".aws/config", "kubeconfig", "my.kubeconfig", `C:\Users\a\.ssh\id_rsa`, "ID_RSA", ".ENV"}
	no := []string{".env.example", "config/.env.sample", "id_rsa.pub", "main.go", "README.md", "dev.example.tfvars", "docs/environment.md", "keyboard.txt"}
	for _, p := range yes {
		if !IsSensitiveFilename(p) {
			t.Errorf("%q should be sensitive", p)
		}
	}
	for _, p := range no {
		if IsSensitiveFilename(p) {
			t.Errorf("%q should not be sensitive", p)
		}
	}
}

func TestLooksLikeSecret(t *testing.T) {
	yes := []string{"key = sk-ant-" + strings.Repeat("TEST", 6), "ghp_" + strings.Repeat("a1B2", 9), "AKIA" + strings.Repeat("A", 16),
		"-----BEGIN RSA PRIVATE KEY-----", "-----BEGIN PRIVATE KEY-----", "xoxb-" + strings.Repeat("1", 12)}
	no := []string{"hello world", "sk-short", "AKIAshort", "the word ghp is fine", "BEGIN PRIVATE KEY without dashes"}
	for _, s := range yes {
		if !LooksLikeSecret(s) {
			t.Errorf("%q should look like a secret", s)
		}
	}
	for _, s := range no {
		if LooksLikeSecret(s) {
			t.Errorf("%q should not look like a secret", s)
		}
	}
}
