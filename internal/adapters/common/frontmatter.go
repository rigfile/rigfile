package common

import (
	"bytes"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Frontmatter splits a Markdown file into its YAML frontmatter fields and the body. A file without a leading
// `---` fence has no frontmatter.
func Frontmatter(b []byte) (fields map[string]any, body string) {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, s
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, s
	}
	var m map[string]any
	if err := yaml.NewDecoder(bytes.NewReader([]byte(rest[:end]))).Decode(&m); err != nil {
		return nil, s
	}
	body = rest[end+4:]
	body = strings.TrimPrefix(body, "\n")
	return m, body
}

// String reads a string field.
func String(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
