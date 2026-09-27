package common

import (
	"reflect"
	"testing"

	"github.com/rigfile/rigfile/internal/manifest"
)

func TestExecWrapForCarriesLevel2MetadataOnlyWhenTheServerDeclaresAllow(t *testing.T) {
	s := manifest.MCPServer{Command: "npx", Args: []string{"-y", "alpaca-mcp@1.4.2"},
		Env:     map[string]string{"A_KEY": "secret://alpaca/key", "REGION": "us"},
		Network: manifest.Network{Allow: []string{"api.alpaca.markets", "*.alpaca.markets"}}}
	hosts := map[string][]string{"alpaca/key": {"api.alpaca.markets"}, "unused/one": {"x.example.test"}}
	_, got := ExecWrapFor("", "alpaca", s, hosts)
	want := []string{"exec", "--server", "alpaca", "--allow", "api.alpaca.markets,*.alpaca.markets",
		"--secret", "A_KEY=alpaca/key", "--env", "REGION=us", "--bind", "alpaca/key=api.alpaca.markets", "--", "npx", "-y", "alpaca-mcp@1.4.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	// no network.allow: exactly the Level 1 entry, whatever the secrets declare
	s.Network = manifest.Network{}
	_, got = ExecWrapFor("", "alpaca", s, hosts)
	_, level1 := ExecWrap("", s)
	if !reflect.DeepEqual(got, level1) || got[1] != "--secret" {
		t.Fatalf("%q", got)
	}
}
