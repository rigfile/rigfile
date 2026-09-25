package splice

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const userTOML = `# my codex config: hand edited, keep my comments!
model = "some-model"   # trailing comment stays
approval_policy = "on-request"

[features]
# feature flags
alpha = true
`

const rigBody = `[mcp_servers.demo]
command = "rigfile"
args = ["exec", "--mcp", "demo", "--", "npx", "-y", "example-mcp@1.2.3"]
`

func TestUpsertAppendsAndPreservesUserContent(t *testing.T) {
	out, changed, err := UpsertTOML([]byte(userTOML), "rig#demo", []byte(rigBody), Options{})
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	if !bytes.HasPrefix(out, []byte(userTOML)) {
		t.Fatalf("user content was modified:\n%s", out)
	}
	var v map[string]any
	if err := toml.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["mcp_servers"].(map[string]any)["demo"]; !ok {
		t.Fatalf("managed table missing: %v", v)
	}
	if v["model"] != "some-model" {
		t.Fatalf("user key lost")
	}
}

func TestUpsertIsIdempotent(t *testing.T) {
	a, _, _ := UpsertTOML([]byte(userTOML), "rig#demo", []byte(rigBody), Options{})
	b, changed, err := UpsertTOML(a, "rig#demo", []byte(rigBody), Options{})
	if err != nil || changed || !bytes.Equal(a, b) {
		t.Fatalf("second upsert must be a no-op (err=%v changed=%v)", err, changed)
	}
}

func TestReplaceKeepsPositionAndSurroundings(t *testing.T) {
	doc, _, _ := Upsert([]byte(userTOML), Hash, "a", []byte("x = 1\n"), Options{})
	doc, _, _ = Upsert(doc, Hash, "b", []byte("y = 2\n"), Options{})
	doc = append(doc, []byte("\n# user text after\nz = 3\n")...)
	out, changed, err := Upsert(doc, Hash, "a", []byte("x = 100\n"), Options{})
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	s := string(out)
	if strings.Index(s, "rigfile:begin a ") > strings.Index(s, "rigfile:begin b ") {
		t.Fatal("region a moved after b; position must be kept")
	}
	if !strings.Contains(s, "x = 100") || strings.Contains(s, "x = 1\n") {
		t.Fatalf("body not replaced:\n%s", s)
	}
	if !strings.HasSuffix(s, "\n# user text after\nz = 3\n") || !strings.HasPrefix(s, userTOML) {
		t.Fatalf("surroundings changed:\n%s", s)
	}
}

func TestRemoveRoundTripsExactly(t *testing.T) {
	orig := []byte(userTOML)
	with, _, _ := Upsert(orig, Hash, "rig#demo", []byte(rigBody), Options{})
	back, removed, err := Remove(with, Hash, "rig#demo")
	if err != nil || !removed || !bytes.Equal(back, orig) {
		t.Fatalf("remove did not restore original (err=%v removed=%v)\n%q\nvs\n%q", err, removed, back, orig)
	}
	// Removing something absent is a no-op.
	same, removed, err := Remove(orig, Hash, "nope")
	if err != nil || removed || !bytes.Equal(same, orig) {
		t.Fatal("removing an absent region must be a no-op")
	}
}

func TestEmptyDocAndNoTrailingNewline(t *testing.T) {
	out, _, err := Upsert(nil, Hash, "a", []byte("k = 1\n"), Options{})
	if err != nil || !strings.HasPrefix(string(out), "# rigfile:begin a sha256=") {
		t.Fatalf("empty doc: %v %q", err, out)
	}
	out, _, err = Upsert([]byte("no newline at end"), Hash, "a", []byte("k = 1\n"), Options{})
	if err != nil || !strings.HasPrefix(string(out), "no newline at end\n\n# rigfile:begin") {
		t.Fatalf("no trailing newline: %v %q", err, out)
	}
}

func TestCRLFIsPreservedAndNeverMixed(t *testing.T) {
	doc := []byte("a = 1\r\nb = 2\r\n")
	out, _, err := Upsert(doc, Hash, "rig#x", []byte("c = 3\nd = 4\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ReplaceAll(out, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatalf("bare LF found in a CRLF file: %q", out)
	}
	if !bytes.HasPrefix(out, doc) {
		t.Fatal("user CRLF content changed")
	}
	// Re-applying the same body is still a no-op on CRLF files.
	again, changed, err := Upsert(out, Hash, "rig#x", []byte("c = 3\nd = 4\n"), Options{})
	if err != nil || changed || !bytes.Equal(again, out) {
		t.Fatalf("CRLF not idempotent: changed=%v err=%v", changed, err)
	}
}

func TestMarkdownStyle(t *testing.T) {
	md := []byte("# My notes\n\nSome text.\n")
	out, _, err := Upsert(md, HTML, "rig#security", []byte("## Security baseline\n- never commit secrets\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "<!-- rigfile:begin rig#security sha256=") || !strings.Contains(s, "<!-- rigfile:end rig#security -->") {
		t.Fatalf("html markers missing:\n%s", s)
	}
	if !strings.HasPrefix(s, string(md)) {
		t.Fatal("user markdown changed")
	}
	r, found, err := Find(out, HTML, "rig#security")
	if err != nil || !found || r.Drifted {
		t.Fatalf("find: %v %v %+v", err, found, r)
	}
}

func TestDriftIsDetectedAndRefusedUnlessOverwrite(t *testing.T) {
	doc, _, _ := Upsert([]byte(userTOML), Hash, "a", []byte("x = 1\n"), Options{})
	edited := bytes.Replace(doc, []byte("x = 1"), []byte("x = 999 # hand edit"), 1)
	r, _, _ := Find(edited, Hash, "a")
	if !r.Drifted {
		t.Fatal("hand edit inside region should be flagged as drift")
	}
	if _, _, err := Upsert(edited, Hash, "a", []byte("x = 2\n"), Options{}); !errors.Is(err, ErrDrift) {
		t.Fatalf("want ErrDrift, got %v", err)
	}
	out, changed, err := Upsert(edited, Hash, "a", []byte("x = 2\n"), Options{Overwrite: true})
	if err != nil || !changed || !strings.Contains(string(out), "x = 2") || strings.Contains(string(out), "999") {
		t.Fatalf("overwrite failed: %v", err)
	}
}

func TestMalformedMarkersAreRejected(t *testing.T) {
	cases := map[string]string{
		"begin without end": "# rigfile:begin a sha256=000000000000\nx=1\n",
		"end without begin": "x=1\n# rigfile:end a\n",
		"nested":            "# rigfile:begin a\n# rigfile:begin b\n# rigfile:end b\n# rigfile:end a\n",
		"mismatched end":    "# rigfile:begin a\n# rigfile:end b\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Upsert([]byte(doc), Hash, "c", []byte("k=1\n"), Options{}); !errors.Is(err, ErrMalformed) {
				t.Fatalf("want ErrMalformed, got %v", err)
			}
		})
	}
	dup := "# rigfile:begin a\nx=1\n# rigfile:end a\n# rigfile:begin a\ny=2\n# rigfile:end a\n"
	if _, _, err := Upsert([]byte(dup), Hash, "a", []byte("k=1\n"), Options{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("duplicate region: want ErrMalformed, got %v", err)
	}
	// A duplicate of a *different* id does not block editing ours.
	if _, _, err := Upsert([]byte(dup), Hash, "c", []byte("k=1\n"), Options{}); err != nil {
		t.Fatalf("unrelated id should still be editable: %v", err)
	}
}

func TestBodyCannotForgeMarkers(t *testing.T) {
	for _, body := range []string{
		"x = 1\n# rigfile:end a\n[evil]\n",
		"# rigfile:begin other\n",
		"<!-- rigfile:end a -->\n",
	} {
		if _, _, err := Upsert(nil, Hash, "a", []byte(body), Options{}); !errors.Is(err, ErrBadInput) {
			t.Fatalf("body %q: want ErrBadInput, got %v", body, err)
		}
	}
}

func TestBadIDs(t *testing.T) {
	for _, id := range []string{"", "has space", "new\nline", strings.Repeat("a", 200), "semi;colon", "-->"} {
		if _, _, err := Upsert(nil, Hash, id, []byte("k=1\n"), Options{}); !errors.Is(err, ErrBadInput) {
			t.Fatalf("id %q: want ErrBadInput, got %v", id, err)
		}
	}
}

func TestTOMLConflictWithUserTableIsReported(t *testing.T) {
	doc := []byte("[mcp_servers.demo]\ncommand = \"mine\"\n")
	_, _, err := UpsertTOML(doc, "rig#demo", []byte(rigBody), Options{})
	if !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("duplicate table should be a conflict, got %v", err)
	}
}

func TestTOMLInvalidBaseIsNotTouched(t *testing.T) {
	_, _, err := UpsertTOML([]byte("this is = = not toml"), "a", []byte("k = 1\n"), Options{})
	if !errors.Is(err, ErrInvalidBase) {
		t.Fatalf("want ErrInvalidBase, got %v", err)
	}
}

// Property: for arbitrary user text that contains no markers, Upsert then Remove restores it, and the
// user text is always an untouched prefix of the result. Deterministic seed.
func TestRandomUserContentIsNeverModified(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b = 1", "# c", "[t]", "", "  ", "é", "\t", "'''", "<!-- x -->", "rigfile", "# rigfile: not a marker"}
	for i := 0; i < 500; i++ {
		var b strings.Builder
		n := rng.Intn(12)
		eol := "\n"
		if rng.Intn(4) == 0 {
			eol = "\r\n"
		}
		for j := 0; j < n; j++ {
			b.WriteString(alphabet[rng.Intn(len(alphabet))] + eol)
		}
		orig := []byte(b.String())
		with, changed, err := Upsert(orig, Hash, "rig#p", []byte("k = 1\nl = 2\n"), Options{})
		if err != nil || !changed {
			t.Fatalf("iter %d: %v", i, err)
		}
		if !bytes.HasPrefix(with, orig) {
			t.Fatalf("iter %d: user prefix modified:\n%q\n%q", i, orig, with)
		}
		back, removed, err := Remove(with, Hash, "rig#p")
		if err != nil || !removed || !bytes.Equal(back, orig) {
			t.Fatalf("iter %d: round trip failed:\n%q\n%q", i, orig, back)
		}
	}
}
