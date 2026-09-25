// Package scan holds the secret-detection rules shared by the git hooks, the agent hooks and (later)
// publish. Spike subset: sensitive file names and a floor of known secret formats. The real scanner
// embeds the gitleaks ruleset (plan §13, ADR 0001 open item 4).
package scan

import (
	"path"
	"regexp"
	"strings"
)

// sensitiveName mirrors RIGFILE_PLAN.md §8.1a and the global git hook's blocklist. Case-insensitive.
var sensitiveName = regexp.MustCompile(`(?i)` +
	`(^|/)\.env$|(^|/)\.env\.[^/]+$|\.(pem|key|p12|pfx|jks|keystore|ppk)$|` +
	`(^|/)id_(rsa|dsa|ecdsa|ed25519)(\.[^/]*)?$|` +
	`(^|/)([^/]*credentials[^/]*|service-account[^/]*|[^/]*-sa|[^/]*adminsdk[^/]*|client_secret[^/]*|token)\.json$|` +
	`(^|/)\.(npmrc|pypirc|netrc|htpasswd)$|(^|/)_netrc$|\.tfstate(\.[^/]*)?$|\.tfvars$|` +
	`(^|/)secrets\.ya?ml$|\.secret$|(^|/)\.(aws|gcp|azure|ssh|gnupg)/|(^|/)kubeconfig$|\.kubeconfig$`)

// exempt names share a blocked pattern but are public/example files.
var exemptName = regexp.MustCompile(`(?i)(^|/)\.env\.(example|sample|template|dist)$|\.example\.tfvars$|\.pub$`)

// IsSensitiveFilename reports whether a repo-relative or absolute path looks like a credential file.
// Backslashes are treated as separators so Windows-style paths match too.
func IsSensitiveFilename(p string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	return sensitiveName.MatchString(p) && !exemptName.MatchString(p)
}

// Base returns the last path element (separator-agnostic), for messages.
func Base(p string) string { return path.Base(strings.ReplaceAll(p, `\`, "/")) }

// secretShapes is the same floor as the JSON Schema's `secretLooking` (schema/rigfile.v1.json).
var secretShapes = regexp.MustCompile(
	`sk-ant-[A-Za-z0-9_-]{8,}|sk-[A-Za-z0-9]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|` +
		`xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|sk_live_[A-Za-z0-9]{10,}|` +
		`-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// LooksLikeSecret reports whether text contains a known secret format (it never returns the match:
// callers must not echo it).
func LooksLikeSecret(s string) bool { return secretShapes.MatchString(s) }
