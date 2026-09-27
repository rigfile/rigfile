package models

import (
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/platform"
)

func hw(os, arch string, mem float64, gpus ...platform.GPU) platform.Hardware {
	return platform.Hardware{OS: os, Arch: arch, MemoryGB: mem, GPUs: gpus, FreeDiskGB: 200}
}

func TestTheRealCatalogParsesAndChoosesPerMachine(t *testing.T) {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	role := c.Roles["local-coder"]
	if len(role.Variants) < 4 || role.Variants[0].Source != "catalog:local-coder" {
		t.Fatalf("%+v", role)
	}
	apple := platform.GPU{Vendor: "apple", VRAMGB: 16}
	cases := []struct {
		name string
		h    platform.Hardware
		want string // variant id
	}{
		{"the reference setup: Apple Silicon 16 GB", hw("macos", "arm64", 16, apple), "apple-silicon-16gb-mlx-qwen3-8b-4bit"},
		{"Apple Silicon 64 GB (the 32 GB entry is a stub, so the verified one still wins)", hw("macos", "arm64", 64, apple), "apple-silicon-16gb-mlx-qwen3-8b-4bit"},
		{"Apple Silicon with too little memory", hw("macos", "arm64", 8, platform.GPU{Vendor: "apple", VRAMGB: 8}), "fallback-ollama-qwen3-8b"},
		{"Intel Mac", hw("macos", "amd64", 32), "fallback-ollama-qwen3-8b"},
		{"Linux with an NVIDIA GPU (the llama.cpp entry is a stub)", hw("linux", "amd64", 32, platform.GPU{Vendor: "nvidia", VRAMGB: 12}), "fallback-ollama-qwen3-8b"},
		{"Windows, nothing known about the hardware", platform.Hardware{OS: "windows", Arch: "amd64"}, "fallback-ollama-qwen3-8b"},
	}
	for _, tc := range cases {
		v, rej := Choose(role.Variants, tc.h)
		if v == nil || v.ID != tc.want {
			t.Errorf("%s: chose %v (rejected %+v), want %s", tc.name, v, rej, tc.want)
		}
	}
	// stubs are always rejected with their reason
	_, rej := Choose(role.Variants, hw("macos", "arm64", 64, apple))
	_ = rej
	for _, v := range role.Variants {
		if v.Status == "stub" {
			if err := Validate(v); err == nil || !strings.Contains(err.Error(), "never applied") {
				t.Errorf("%s: a stub must never validate: %v", v.ID, err)
			}
		}
	}
	// the verified entries validate
	for _, v := range role.Variants {
		if v.Status == "verified" {
			if err := Validate(v); err != nil {
				t.Errorf("%s: %v", v.ID, err)
			}
		}
	}
}

func TestMatchesFailsClosed(t *testing.T) {
	h := hw("linux", "amd64", 16)
	for name, tc := range map[string]struct {
		when map[string]any
		want bool
	}{
		"empty matches anything":    {map[string]any{}, true},
		"os and arch":               {map[string]any{"os": "linux", "arch": "amd64"}, true},
		"wrong os":                  {map[string]any{"os": "macos"}, false},
		"memory met":                {map[string]any{"min_memory_gb": 16}, true},
		"memory unmet":              {map[string]any{"min_memory_gb": 32}, false},
		"float memory":              {map[string]any{"min_memory_gb": 15.5}, true},
		"gpu required, none":        {map[string]any{"gpu": "nvidia"}, false},
		"vram required, none":       {map[string]any{"min_vram_gb": 8}, false},
		"an unknown condition":      {map[string]any{"cpu_flavour": "spicy"}, false},
		"a non-numeric requirement": {map[string]any{"min_memory_gb": "lots"}, false},
		"disk":                      {map[string]any{"min_disk_gb": 100}, true},
	} {
		if got, _ := Matches(tc.when, h); got != tc.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
	// hardware that could not be read never satisfies a minimum
	if ok, _ := Matches(map[string]any{"min_memory_gb": 1}, platform.Hardware{OS: "linux"}); ok {
		t.Error("unknown memory must not satisfy a minimum")
	}
}

func TestValidateRefusesWhatIsNotSafeOrPinned(t *testing.T) {
	good := Variant{Engine: "mlx-lm", EngineVersion: "0.31.3", Model: "mlx-community/Qwen3-8B-4bit", Revision: strings.Repeat("a", 40), WeightsFormat: "mlx-safetensors"}
	if err := Validate(good); err != nil {
		t.Fatal(err)
	}
	mut := func(f func(*Variant)) Variant { v := good; f(&v); return v }
	for name, tc := range map[string]struct {
		v    Variant
		want string
	}{
		"a pickle format":          {mut(func(v *Variant) { v.WeightsFormat = "pickle" }), "not a safe format"},
		"trust_remote_code":        {mut(func(v *Variant) { v.Args = map[string]any{"trust-remote-code": true} }), "never allowed"},
		"host as an arg":           {mut(func(v *Variant) { v.Args = map[string]any{"host": "0.0.0.0"} }), "may not be set here"},
		"served on all interfaces": {mut(func(v *Variant) { v.Serve.Host = "0.0.0.0" }), "loopback only"},
		"no revision":              {mut(func(v *Variant) { v.Revision = "" }), "40-character"},
		"a branch as revision":     {mut(func(v *Variant) { v.Revision = "main" }), "40-character"},
		"no engine version":        {mut(func(v *Variant) { v.EngineVersion = "" }), "engine version"},
		"an Ollama tag for mlx":    {mut(func(v *Variant) { v.Model = "qwen3:8b" }), "not a Hugging Face repo"},
		"llama.cpp":                {mut(func(v *Variant) { v.Engine = "llama.cpp" }), "not supported yet"},
		"a TODO":                   {mut(func(v *Variant) { v.Revision = "TODO-verify" }), "TODO"},
		"a stub":                   {mut(func(v *Variant) { v.Status = "stub" }), "never applied"},
		"an ollama repo id":        {Variant{Engine: "ollama", Model: "org/name"}, "not an Ollama tag"},
	} {
		if err := Validate(tc.v); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
	if err := Validate(Variant{Engine: "ollama", Model: "qwen3:8b", Serve: struct {
		Host          string `yaml:"host"`
		Port          int    `yaml:"port"`
		API           any    `yaml:"api"`
		CommandPinned struct {
			Exe  string   `yaml:"exe"`
			Args []string `yaml:"args"`
		} `yaml:"command_pinned"`
	}{Host: "localhost"}}); err != nil {
		t.Fatal(err)
	}
}

func TestLoopbackHosts(t *testing.T) {
	for h, want := range map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true, "[::1]": true, "127.1.2.3": true, "0.0.0.0": false, "192.168.1.5": false, "example.com": false, "": false} {
		if IsLoopbackHost(h) != want {
			t.Errorf("%q", h)
		}
	}
}
