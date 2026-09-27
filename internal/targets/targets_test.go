package targets

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/targets/matrix.md")

const matrixPath = "../../docs/targets/matrix.md"

func TestCapabilitiesAreCompleteAndTheMatrixDocIsCurrent(t *testing.T) {
	caps, err := LoadCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude-code", "codex", "gemini-cli", "cursor", "claude-desktop", "vscode-copilot", "devin", "zed"} {
		c, ok := caps[want]
		if !ok {
			t.Fatalf("missing capabilities for %s", want)
		}
		for _, cat := range CategoryOrder {
			cell, ok := c.Categories[cat]
			if !ok || (cell.Support != "yes" && cell.Support != "partial" && cell.Support != "no") || cell.Note == "" {
				t.Errorf("%s/%s: incomplete cell %+v (every cell needs a support level and a note)", want, cat, cell)
			}
		}
		if len(c.OS) == 0 || c.BaseSecure == "" || len(c.ConfigPaths) == 0 {
			t.Errorf("%s: os, config_paths and base_secure are required", want)
		}
	}
	if caps["claude-desktop"].BaseSecure != "none" || strings.Contains(strings.Join(caps["claude-desktop"].OS, ","), "linux") {
		t.Fatal("Claude Desktop has no Linux build and no base-secure surface (docs/targets/claude-desktop.md)")
	}
	md, err := Matrix()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(matrixPath, []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if old, err := os.ReadFile(matrixPath); err != nil || string(old) != md {
		t.Errorf("%s is stale or missing: run `go test ./internal/targets -update`", matrixPath)
	}
}

func TestRegistryOrderAndLookup(t *testing.T) {
	restore := Reset(Target{Name: "zeta"}, claudeCode(), Target{Name: "alpha"})
	defer restore()
	got := strings.Join(Names(), ",")
	if got != "claude-code,alpha,zeta" {
		t.Fatalf("%s", got)
	}
	if _, ok := Get("alpha"); !ok {
		t.Fatal("alpha")
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("nope")
	}
}
