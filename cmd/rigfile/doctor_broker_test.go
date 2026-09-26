package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/rigd"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

func TestDoctorShowsTheLevelPerServer(t *testing.T) {
	entry := func(args ...string) state.Item {
		v, _ := json.Marshal(map[string]any{"command": "rigfile", "args": args})
		return state.Item{Category: "mcp", Key: "x", Detail: map[string]string{"value": string(v)}}
	}
	items := []state.Item{
		entry("exec", "--server", "alpaca", "--allow", "api.alpaca.markets", "--secret", "K=a/k", "--bind", "a/k=api.alpaca.markets", "--", "npx"),
		entry("exec", "--server", "legacy", "--allow", "x.example.test", "--secret", "K=l/k", "--", "npx"),
		entry("exec", "--server", "noallow", "--secret", "K=n/k", "--", "npx"),
		entry("exec", "--server", "plain", "--", "npx"),
		{Category: "mcp", Key: "not-wrapped", Detail: map[string]string{"value": `{"command":"npx","args":["x"]}`}},
	}
	st := state.New()
	st.Targets["cursor"] = &state.TargetState{Items: items}
	dir := t.TempDir()

	lines, worst := brokerLevels(st, dir)
	got := strings.Join(lines, "\n")
	for _, want := range []string{"alpaca: L1 (real key in the process; `rigfile broker enable`", "plain: no secrets"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if worst != lvWarn || strings.Contains(got, "not-wrapped") {
		t.Fatalf("%v\n%s", worst, got)
	}

	on := rigd.Config{Enabled: true, Excluded: []string{"legacy"}}
	if err := on.Save(dir); err != nil {
		t.Fatal(err)
	}
	lines, worst = brokerLevels(st, dir)
	got = strings.Join(lines, "\n")
	for _, want := range []string{"alpaca: L2, broker not running: will refuse to start", "legacy: L1 (excluded", "noallow: L1 (real key in the process; the server declares no network.allow)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if worst != lvFail {
		t.Fatalf("%v", worst)
	}
	if levelFor(wrapped{secrets: true, hasAllow: true, server: "a"}, on, true) != "L2 protected (surrogate key; real key stays in the broker)" {
		t.Fatal("a running broker protects a declared server")
	}
}
