package manifest

import (
	"errors"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const fixturePath = "../../testdata/fixtures/plan-example.rigfile.yaml"

func newV(t *testing.T) *Validator {
	t.Helper()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

func loadFixture(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// at walks nested maps/slices by key or index.
func at(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("at: %v is not a map (path %v)", v, path)
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok {
				t.Fatalf("at: %v is not a slice (path %v)", v, path)
			}
			v = s[k]
		}
	}
	return v
}

func TestFixtureIsValid(t *testing.T) {
	v := newV(t)
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.ValidateYAML(b); err != nil {
		t.Fatalf("fixture should be valid: %v", err)
	}
}

// Ports of the Python probes used in Stage 0 (scratch check_schema.py): the Go validator must agree.
func TestMutations(t *testing.T) {
	fakeAnt := "sk-ant-" + strings.Repeat("TEST", 6) // obviously fake
	type mut struct {
		name    string
		apply   func(t *testing.T, d map[string]any)
		invalid bool
	}
	set := func(t *testing.T, d map[string]any, val any, path ...any) {
		parent := at(t, d, path[:len(path)-1]...)
		switch p := parent.(type) {
		case map[string]any:
			p[path[len(path)-1].(string)] = val
		default:
			t.Fatalf("set: parent %T", parent)
		}
	}
	appendTo := func(t *testing.T, d map[string]any, val any, path ...any) {
		parent := at(t, d, path[:len(path)-1]...).(map[string]any)
		key := path[len(path)-1].(string)
		parent[key] = append(parent[key].([]any), val)
	}
	cases := []mut{
		{"secret-looking value in a non-secret env var", func(t *testing.T, d map[string]any) {
			set(t, d, fakeAnt, "mcp_servers", "alpaca", "env", "ALPACA_PAPER")
		}, true},
		{"credential-named env var with a literal", func(t *testing.T, d map[string]any) {
			set(t, d, "abc123", "mcp_servers", "alpaca", "env", "ALPACA_API_KEY")
		}, true},
		{"secret-looking value in args", func(t *testing.T, d map[string]any) {
			appendTo(t, d, fakeAnt, "mcp_servers", "alpaca", "args")
		}, true},
		{"secret in description", func(t *testing.T, d map[string]any) {
			d["description"] = "use key " + fakeAnt
		}, true},
		{"credentials in remote URL", func(t *testing.T, d map[string]any) {
			set(t, d, "https://user:pw@mcp.example.test/mcp", "mcp_servers", "github", "url")
		}, true},
		{"http (not https) remote MCP", func(t *testing.T, d map[string]any) {
			set(t, d, "http://mcp.example.test/mcp", "mcp_servers", "github", "url")
		}, true},
		{"Authorization header literal", func(t *testing.T, d map[string]any) {
			set(t, d, map[string]any{"Authorization": "Bearer abc"}, "mcp_servers", "github", "headers")
		}, true},
		{"Authorization header as secret ref (valid)", func(t *testing.T, d map[string]any) {
			set(t, d, map[string]any{"Authorization": "secret://gh/token"}, "mcp_servers", "github", "headers")
		}, false},
		{"bearer without token", func(t *testing.T, d map[string]any) {
			set(t, d, "bearer", "mcp_servers", "github", "auth")
		}, true},
		{"bearer with token (valid)", func(t *testing.T, d map[string]any) {
			set(t, d, "bearer", "mcp_servers", "github", "auth")
			set(t, d, "secret://gh/token", "mcp_servers", "github", "bearer_token")
		}, false},
		{"hard-coded mac home in permission", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"read": "/Users/ada/.ssh/**"}, "permissions", "deny")
		}, true},
		{"windows path in permission", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"read": `C:\Users\ada\.ssh\**`}, "permissions", "deny")
		}, true},
		{"absolute path in rig file ref", func(t *testing.T, d map[string]any) {
			set(t, d, "/etc/passwd", "instructions", 0, "file")
		}, true},
		{"parent traversal in rig file ref", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"path": "../outside"}, "skills")
		}, true},
		{"backslash in rig path", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"path": `commands\ship.md`}, "commands")
		}, true},
		{"nested .. segment in rig path", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"path": "a/../b"}, "commands")
		}, true},
		{"dotted file name is fine (valid)", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"path": "commands/v1..2.md"}, "commands")
		}, false},
		{"model server on 0.0.0.0", func(t *testing.T, d map[string]any) {
			set(t, d, "0.0.0.0", "models", "local-coder", "serve", "host")
		}, true},
		{"model args smuggling host", func(t *testing.T, d map[string]any) {
			set(t, d, "0.0.0.0", "models", "local-coder", "variants", 0, "args", "host")
		}, true},
		{"gateway upstream not loopback", func(t *testing.T, d map[string]any) {
			set(t, d, "http://evil.example.test/v1", "gateways", "anthropic-bridge", "routes", "x")
		}, true},
		{"gateway listens on all interfaces", func(t *testing.T, d map[string]any) {
			set(t, d, "0.0.0.0:4000", "gateways", "anthropic-bridge", "listen")
		}, true},
		{"revision not a sha", func(t *testing.T, d map[string]any) {
			set(t, d, "main", "models", "local-coder", "variants", 0, "revision")
		}, true},
		{"unknown os value", func(t *testing.T, d map[string]any) {
			set(t, d, []any{"wsl"}, "hooks", 1, "os")
		}, true},
		{"unknown top-level key", func(t *testing.T, d map[string]any) {
			d["secrets_values"] = map[string]any{}
		}, true},
		{"x- extension key (valid)", func(t *testing.T, d map[string]any) {
			d["x-notes"] = map[string]any{"a": 1}
		}, false},
		{"bad rig name (uppercase)", func(t *testing.T, d map[string]any) {
			d["name"] = "Adams/Data-Science"
		}, true},
		{"hook with builtin/script per-OS run (valid)", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"id": "x", "event": "stop", "run": map[string]any{"windows": "builtin:notify", "linux": "hooks/n.sh"}}, "hooks")
		}, false},
		{"hook with empty per-OS run", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"id": "x", "event": "stop", "run": map[string]any{}}, "hooks")
		}, true},
		{"permission rule with two kinds", func(t *testing.T, d map[string]any) {
			appendTo(t, d, map[string]any{"read": "a", "bash": "b"}, "permissions", "deny")
		}, true},
		{"model without role or variants", func(t *testing.T, d map[string]any) {
			set(t, d, map[string]any{"purpose": []any{"summaries"}}, "models", "bad")
		}, true},
		{"private key header in a description", func(t *testing.T, d map[string]any) {
			set(t, d, "-----BEGIN RSA PRIVATE KEY-----", "secrets", "alpaca/api_key", "description")
		}, true},
	}
	v := newV(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := loadFixture(t)
			tc.apply(t, d)
			err := v.ValidateValue(d)
			if tc.invalid && err == nil {
				t.Fatalf("expected the manifest to be rejected")
			}
			if !tc.invalid && err != nil {
				t.Fatalf("expected the manifest to be accepted, got: %v", err)
			}
			if err != nil {
				var ve *ValidationError
				if !errors.As(err, &ve) || len(ve.Problems) == 0 {
					t.Fatalf("want *ValidationError with problems, got %T: %v", err, err)
				}
			}
		})
	}
}

func TestMalformedYAML(t *testing.T) {
	v := newV(t)
	if err := v.ValidateYAML([]byte("a: [unclosed")); err == nil {
		t.Fatal("expected a parse error")
	}
}
