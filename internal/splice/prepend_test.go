package splice

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestPrependPutsANewTomlRegionAboveTablesAndRoundTrips(t *testing.T) {
	orig := "# mine\n[profiles.x]\nmodel = \"m\"\n"
	body := []byte("approval_policy = \"on-request\"\n")
	out, changed, err := UpsertTOML([]byte(orig), "top", body, Options{Prepend: true})
	if err != nil || !changed {
		t.Fatalf("%v %v", err, changed)
	}
	if !strings.HasPrefix(string(out), "# rigfile:begin top") || !strings.HasSuffix(string(out), orig) {
		t.Fatalf("region must be first and the user's text untouched after it:\n%s", out)
	}
	var v map[string]any
	if err := toml.Unmarshal(out, &v); err != nil || v["approval_policy"] != "on-request" {
		t.Fatalf("a top-level key must stay top-level: %v %v", err, v)
	}
	// replacing keeps the position; removing restores the original bytes
	out2, _, _ := UpsertTOML(out, "top", []byte("approval_policy = \"never\"\n"), Options{Prepend: true, Overwrite: true})
	if !strings.HasPrefix(string(out2), "# rigfile:begin top") || strings.Count(string(out2), "rigfile:begin") != 1 {
		t.Fatalf("%s", out2)
	}
	back, removed, err := Remove(out, Hash, "top")
	if err != nil || !removed || string(back) != orig {
		t.Fatalf("remove must restore the original:\n%q\n%q", back, orig)
	}
	// an empty file
	e, _, _ := UpsertTOML(nil, "top", body, Options{Prepend: true})
	if b, _, _ := Remove(e, Hash, "top"); len(b) != 0 {
		t.Fatalf("%q", b)
	}
}
