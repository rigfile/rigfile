package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

var appleHW = platform.Hardware{OS: "macos", Arch: "arm64", MemoryGB: 16, FreeDiskGB: 300, GPUs: []platform.GPU{{Vendor: "apple", Name: "Apple Silicon", VRAMGB: 16}}}

func runHW(m *machine, hw platform.Hardware, args ...string) result {
	var out, errb bytes.Buffer
	code := runWith(m, &out, &errb, func(e *env) { e.hardware = &hw }, args...)
	return result{code, portable(out.String()), portable(errb.String())}
}

func TestPlanShowsTheModelsSection(t *testing.T) {
	m := newMachine(t)
	rig := plainRig(t, "models:\n  local-coder:\n    role: local-coder\n    serve: {port: 8080, autostart: true}\ngateways:\n  bridge:\n    listen: 127.0.0.1:4000\n    routes: {local-coder: \"http://127.0.0.1:8080/v1\"}\nrouting:\n  default: cloud\n  local_for: [summaries]\n")
	r := runHW(m, appleHW, "plan", rig, "--no-git")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"MODELS", "mlx-community/Qwen3-8B-4bit via mlx-lm 0.31.3", "download 4.6 GB", "pinned to revision 545dc4251c05", "served on 127.0.0.1:8080 (per-user service",
		"needs-bridge codex:", "gateways: are NOT applied", "routing: is NOT translated", "nothing is downloaded until you approve"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q:\n%s", want, r.out)
		}
	}
	// the same rig on a Linux box without a GPU falls back to Ollama, the engine with a documented route
	linux := platform.Hardware{OS: "linux", Arch: "amd64", MemoryGB: 16, FreeDiskGB: 100}
	r = runHW(m, linux, "plan", rig, "--no-git")
	for _, want := range []string{"qwen3:8b via ollama 0.34.4", "supported    codex:", "experimental claude-code:", "not chosen: apple-silicon-16gb-mlx-qwen3-8b-4bit: needs arch arm64, this is amd64"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q:\n%s", want, r.out)
		}
	}
	// a rig without models has no such section, and detects no hardware
	if r := runHW(m, appleHW, "plan", plainRig(t, ""), "--no-git"); strings.Contains(r.out, "MODELS") {
		t.Fatal("no models: no section")
	}
}

func TestModelsListShowsTheChoiceForThisMachine(t *testing.T) {
	m := newMachine(t)
	r := runHW(m, appleHW, "models", "list")
	if r.code != 0 || !strings.Contains(r.out, "This machine: macos/arm64, 16 GB memory") || !strings.Contains(r.out, "local-coder:") || !strings.Contains(r.out, "Qwen3-8B-4bit") {
		t.Fatalf("%+v", r)
	}
	if r := runHW(m, appleHW, "models"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}
