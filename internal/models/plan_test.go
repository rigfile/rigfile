package models

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/platform"
)

func mustCat(t *testing.T) *Catalog {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolveARoleOnTheReferenceMachine(t *testing.T) {
	h := hw("macos", "arm64", 16, platform.GPU{Vendor: "apple", VRAMGB: 16})
	p := Resolve("local-coder", manifest.Model{Role: "local-coder", Serve: manifest.ModelServe{Autostart: true}}, mustCat(t), h)
	if p.Blocked != "" || p.Chosen == nil || p.Chosen.Engine != "mlx-lm" {
		t.Fatalf("%+v", p)
	}
	if p.Serve.Host != "127.0.0.1" || p.Serve.Port != 8080 || !p.Serve.Autostart || p.Publisher != "mlx-community" || p.License != "apache-2.0" || !p.EnoughDisk || p.DownloadBytes < 4e9 {
		t.Fatalf("%+v", p)
	}
	var out bytes.Buffer
	p.Render(&out)
	for _, want := range []string{"mlx-community/Qwen3-8B-4bit via mlx-lm 0.31.3", "download 4.6 GB", "publisher mlx-community", "pinned to revision 545dc4251c05", "served on 127.0.0.1:8080 (per-user service", "needs-bridge", "tool calling is not known"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	// the honest wiring: mlx-lm is not wired to either agent
	for _, w := range p.Wiring {
		if (w.Target == "codex" || w.Target == "claude-code") && w.Status != "needs-bridge" {
			t.Errorf("%+v", w)
		}
	}
}

func TestOllamaIsTheOnlyEngineWiredToTheAgents(t *testing.T) {
	p := Resolve("m", manifest.Model{Role: "local-coder"}, mustCat(t), hw("linux", "amd64", 16))
	if p.Chosen == nil || p.Chosen.Engine != "ollama" || p.Serve.Port != 11434 {
		t.Fatalf("%+v", p)
	}
	got := map[string]string{}
	for _, w := range p.Wiring {
		got[w.Target] = w.Status
	}
	if got["codex"] != "supported" || got["claude-code"] != "experimental" || got["cursor"] != "unsupported" {
		t.Fatalf("%v", got)
	}
}

func TestResolveExplicitVariantsAndRefusals(t *testing.T) {
	cat := mustCat(t)
	h := hw("macos", "arm64", 16, platform.GPU{Vendor: "apple", VRAMGB: 16})
	// a rig's own variant is validated like a catalog one
	bad := manifest.Model{Variants: []manifest.ModelVariant{{When: map[string]any{}, Engine: "mlx-lm", Model: "acme/model", Revision: "main", EngineVersion: "1.0"}}}
	if p := Resolve("x", bad, cat, h); p.Chosen != nil || !strings.Contains(p.Blocked, "no variant") || len(p.Considered) != 1 || !strings.Contains(p.Considered[0].Reason, "40-character") {
		t.Fatalf("%+v", p)
	}
	good := manifest.Model{Variants: []manifest.ModelVariant{{When: map[string]any{"os": "macos"}, Engine: "ollama", Model: "qwen3:8b"}}, LicenseAck: "Apache-2.0"}
	if p := Resolve("x", good, cat, h); p.Chosen == nil || p.License != "Apache-2.0" || p.Publisher != "ollama.com/library" {
		t.Fatalf("%+v", p)
	}
	// a non-loopback serve host is refused even where the schema would have caught it first
	nonLoop := good
	nonLoop.Serve.Host = "0.0.0.0"
	if p := Resolve("x", nonLoop, cat, h); p.Chosen != nil || !strings.Contains(p.Blocked, "loopback") {
		t.Fatalf("%+v", p)
	}
	for name, m := range map[string]manifest.Model{"unknown role": {Role: "nope"}, "neither": {}} {
		if p := Resolve("x", m, cat, h); p.Chosen != nil || p.Blocked == "" {
			t.Errorf("%s: %+v", name, p)
		}
	}
	// not enough disk is a warning, not a silent success
	small := h
	small.FreeDiskGB = 2
	if p := Resolve("x", manifest.Model{Role: "local-coder"}, cat, small); p.EnoughDisk || !strings.Contains(strings.Join(p.Warnings, "|"), "may not fit") {
		t.Fatalf("%+v", p)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "size unknown", 5000000: "5 MB", 4607835174: "4.6 GB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
}
