package jsonedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func mustAppend(t *testing.T, doc string, path []string, vals ...string) (string, []string) {
	t.Helper()
	out, added, err := AppendStrings([]byte(doc), path, vals)
	if err != nil {
		t.Fatalf("AppendStrings: %v", err)
	}
	if !json.Valid(out) {
		t.Fatalf("result is not valid JSON:\n%s", out)
	}
	return string(out), added
}

var deny = []string{"permissions", "deny"}

func TestAppendToMultilineArrayMatchesLayoutExactly(t *testing.T) {
	doc := `{
  "permissions": {
    "allow": [
      "Bash(npm run *)"
    ],
    "deny": [
      "Bash(git push*)",
      "Bash(rm -rf*)"
    ]
  },
  "model": "sonnet"
}
`
	want := `{
  "permissions": {
    "allow": [
      "Bash(npm run *)"
    ],
    "deny": [
      "Bash(git push*)",
      "Bash(rm -rf*)",
      "Read(~/.ssh/**)",
      "Read(~/.aws/**)"
    ]
  },
  "model": "sonnet"
}
`
	got, added := mustAppend(t, doc, deny, "Read(~/.ssh/**)", "Read(~/.aws/**)")
	if got != want {
		t.Fatalf("layout not preserved.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if !reflect.DeepEqual(added, []string{"Read(~/.ssh/**)", "Read(~/.aws/**)"}) {
		t.Fatalf("added = %v", added)
	}
}

func TestIndentationStylesAreFollowed(t *testing.T) {
	for name, unit := range map[string]string{"4 spaces": "    ", "tab": "\t"} {
		t.Run(name, func(t *testing.T) {
			doc := "{\n" + unit + `"permissions": {` + "\n" + unit + unit + `"deny": [` + "\n" + unit + unit + unit + `"a"` + "\n" + unit + unit + "]\n" + unit + "}\n}\n"
			got, _ := mustAppend(t, doc, deny, "b")
			want := strings.Replace(doc, `"a"`, `"a",`+"\n"+unit+unit+unit+`"b"`, 1)
			if got != want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, want)
			}
		})
	}
}

func TestCompactDocumentsStayCompact(t *testing.T) {
	got, _ := mustAppend(t, `{"a":1}`, deny, "x", "y")
	if got != `{"a":1,"permissions":{"deny":["x","y"]}}` {
		t.Fatalf("compact insert: %s", got)
	}
	got, _ = mustAppend(t, `{"a": 1}`, deny, "x")
	if got != `{"a": 1,"permissions": {"deny": ["x"]}}` {
		t.Fatalf("spaced-colon compact insert: %s", got)
	}
	got, _ = mustAppend(t, `{"permissions":{"allow":["z"]}}`, deny, "x")
	if got != `{"permissions":{"allow":["z"],"deny":["x"]}}` {
		t.Fatalf("existing parent: %s", got)
	}
	got, _ = mustAppend(t, `{}`, deny, "x")
	if got != `{"permissions":{"deny":["x"]}}` {
		t.Fatalf("empty object: %s", got)
	}
}

func TestSingleLineArrays(t *testing.T) {
	got, _ := mustAppend(t, `{"permissions":{"deny":["a","b"]}}`, deny, "c")
	if got != `{"permissions":{"deny":["a","b","c"]}}` {
		t.Fatalf("compact: %s", got)
	}
	got, _ = mustAppend(t, `{"permissions": {"deny": ["a", "b"]}}`, deny, "c")
	if got != `{"permissions": {"deny": ["a", "b", "c"]}}` {
		t.Fatalf("spaced: %s", got)
	}
	got, _ = mustAppend(t, `{"permissions": {"deny": []}}`, deny, "c", "d")
	if got != `{"permissions": {"deny": ["c","d"]}}` {
		t.Fatalf("empty: %s", got)
	}
}

func TestMissingPiecesAreCreated(t *testing.T) {
	// permissions exists, deny missing
	got, added := mustAppend(t, "{\n  \"permissions\": {\n    \"allow\": [\n      \"x\"\n    ]\n  }\n}\n", deny, "a")
	want := "{\n  \"permissions\": {\n    \"allow\": [\n      \"x\"\n    ],\n    \"deny\": [\n      \"a\"\n    ]\n  }\n}\n"
	if got != want || len(added) != 1 {
		t.Fatalf("missing deny:\n%s\nwant:\n%s", got, want)
	}
	// permissions missing entirely
	got, _ = mustAppend(t, "{\n  \"model\": \"sonnet\"\n}\n", deny, "a", "b")
	want = "{\n  \"model\": \"sonnet\",\n  \"permissions\": {\n    \"deny\": [\n      \"a\",\n      \"b\"\n    ]\n  }\n}\n"
	if got != want {
		t.Fatalf("missing permissions:\n%s\nwant:\n%s", got, want)
	}
	// empty object and empty document
	got, _ = mustAppend(t, "{}", deny, "a")
	if !json.Valid([]byte(got)) || !strings.Contains(got, `"deny"`) {
		t.Fatalf("empty object: %s", got)
	}
	got, _ = mustAppend(t, "", deny, "a")
	if !strings.Contains(got, `"permissions"`) {
		t.Fatalf("empty doc: %s", got)
	}
	// deeper path
	got, _ = mustAppend(t, "{\n  \"a\": 1\n}\n", []string{"x", "y", "z"}, "v")
	var m map[string]any
	_ = json.Unmarshal([]byte(got), &m)
	if m["x"].(map[string]any)["y"].(map[string]any)["z"].([]any)[0] != "v" {
		t.Fatalf("deep path: %s", got)
	}
}

func TestExistingAndDuplicateValuesAreSkipped(t *testing.T) {
	doc := "{\n  \"permissions\": {\n    \"deny\": [\n      \"a\"\n    ]\n  }\n}\n"
	got, added := mustAppend(t, doc, deny, "a", "b", "b")
	if !reflect.DeepEqual(added, []string{"b"}) {
		t.Fatalf("added = %v", added)
	}
	if strings.Count(got, `"b"`) != 1 || strings.Count(got, `"a"`) != 1 {
		t.Fatalf("duplicates written:\n%s", got)
	}
	// Nothing to add => byte-identical document.
	same, added2 := mustAppend(t, doc, deny, "a")
	if same != doc || len(added2) != 0 {
		t.Fatal("no-op must return the document unchanged")
	}
}

func TestCRLFIsPreserved(t *testing.T) {
	doc := "{\r\n  \"permissions\": {\r\n    \"deny\": [\r\n      \"a\"\r\n    ]\r\n  }\r\n}\r\n"
	got, _ := mustAppend(t, doc, deny, "b")
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("bare LF in CRLF document: %q", got)
	}
}

func TestOtherContentIsUntouched(t *testing.T) {
	doc := "{\n  \"z_first\": {\"k\": [1,2,  3]},\n  \"permissions\": {\n    \"deny\": [\n      \"a\"\n    ]\n  },\n  \"a_last\": \"unicode é ✓\",\n  \"n\": 1.50\n}\n"
	got, _ := mustAppend(t, doc, deny, "b")
	// Everything before the insertion point and after it is identical.
	pre := doc[:strings.Index(doc, `"a"`)+3]
	post := doc[strings.Index(doc, `"a"`)+3:]
	if !strings.HasPrefix(got, pre) || !strings.HasSuffix(got, post) {
		t.Fatalf("surrounding bytes changed:\n%s", got)
	}
}

func TestHTMLCharactersAreNotEscaped(t *testing.T) {
	got, _ := mustAppend(t, `{"permissions":{"deny":[]}}`, deny, "Bash(a <b> & c)")
	if !strings.Contains(got, `"Bash(a <b> & c)"`) {
		t.Fatalf("html chars escaped: %s", got)
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]struct {
		doc  string
		path []string
		want error
	}{
		"invalid json":         {`{"a":`, deny, ErrInvalidJSON},
		"root is an array":     {`[1]`, deny, ErrWrongType},
		"parent not an object": {`{"permissions": "nope"}`, deny, ErrWrongType},
		"target not an array":  {`{"permissions": {"deny": "x"}}`, deny, ErrWrongType},
		"non-string element":   {`{"permissions": {"deny": [1]}}`, deny, ErrWrongType},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := AppendStrings([]byte(c.doc), c.path, []string{"x"})
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	if _, _, err := AppendStrings([]byte("{}"), nil, []string{"x"}); err == nil {
		t.Fatal("empty path must be an error")
	}
}

func TestKeysWithSpecialCharacters(t *testing.T) {
	doc := `{"a.b": {"c*d": ["x"]}}`
	got, _ := mustAppend(t, doc, []string{"a.b", "c*d"}, "y")
	if got != `{"a.b": {"c*d": ["x","y"]}}` {
		t.Fatalf("got %s", got)
	}
}

// Property: for random documents in random layouts, the result is valid JSON, contains everything the
// original did, in the same order, plus exactly the new values.
func TestRandomDocuments(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	indents := []string{"", "  ", "    ", "\t"}
	for i := 0; i < 400; i++ {
		var have []string
		for j := 0; j < rng.Intn(4); j++ {
			have = append(have, "rule-"+string(rune('a'+rng.Intn(20))))
		}
		have = dedupe(have)
		if have == nil {
			have = []string{} // a nil slice would marshal as null, which is (correctly) rejected
		}
		base := map[string]any{"z": rng.Intn(100), "permissions": map[string]any{"deny": have, "ask": []string{"q"}}, "a": "text"}
		expect := have // what the document really contained before the edit
		if rng.Intn(3) == 0 {
			delete(base["permissions"].(map[string]any), "deny")
			expect = nil
		}
		if rng.Intn(6) == 0 {
			delete(base, "permissions")
			expect = nil
		}
		ind := indents[rng.Intn(len(indents))]
		var doc []byte
		if ind == "" {
			doc, _ = json.Marshal(base)
		} else {
			doc, _ = json.MarshalIndent(base, "", ind)
			doc = append(doc, '\n')
		}
		add := []string{"new-1", "rule-a", "new-2"}
		out, _, err := AppendStrings(doc, deny, add)
		if err != nil || !json.Valid(out) {
			t.Fatalf("iter %d: err=%v valid=%v\n%s", i, err, json.Valid(out), out)
		}
		var got map[string]any
		_ = json.Unmarshal(out, &got)
		gotDeny := toStrings(got["permissions"].(map[string]any)["deny"])
		for _, h := range expect {
			if !contains(gotDeny, h) {
				t.Fatalf("iter %d: lost %q\n--- original ---\n%s\n--- result ---\n%s", i, h, doc, out)
			}
		}
		if !contains(gotDeny, "new-1") || !contains(gotDeny, "new-2") {
			t.Fatalf("iter %d: new values missing\n%s", i, out)
		}
		if got["a"] != "text" || !bytes.Contains(out, []byte(`"z"`)) {
			t.Fatalf("iter %d: unrelated keys lost", i)
		}
		if strings.Count(string(out), `"rule-a"`) > 1 {
			t.Fatalf("iter %d: duplicate written", i)
		}
	}
}

func toStrings(v any) []string {
	var s []string
	for _, x := range v.([]any) {
		s = append(s, x.(string))
	}
	return s
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// --- AppendRaw / ReadRaw (structured entries such as Claude Code hooks) ------------------------------

const hookEntry = `{"matcher":"Bash","hooks":[{"type":"command","command":"rigfile","args":["hook","run","guard"]}]}`

func TestAppendRawCreatesNestedBranchWithMatchingLayout(t *testing.T) {
	doc := "{\n  \"model\": \"sonnet\"\n}\n"
	out, added, err := AppendRaw([]byte(doc), []string{"hooks", "PreToolUse"}, []string{hookEntry})
	if err != nil || len(added) != 1 {
		t.Fatalf("%v %v", err, added)
	}
	want := `{
  "model": "sonnet",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "rigfile",
            "args": [
              "hook",
              "run",
              "guard"
            ]
          }
        ]
      }
    ]
  }
}
`
	if string(out) != want {
		t.Fatalf("layout:\n%s\nwant:\n%s", out, want)
	}
	if !json.Valid(out) {
		t.Fatal("invalid JSON")
	}
}

func TestAppendRawAppendsToExistingAndDeduplicates(t *testing.T) {
	doc := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Write",
        "hooks": [
          {
            "type": "command",
            "command": "mine.sh"
          }
        ]
      }
    ]
  }
}
`
	out, added, err := AppendRaw([]byte(doc), []string{"hooks", "PreToolUse"}, []string{hookEntry, hookEntry})
	if err != nil || len(added) != 1 {
		t.Fatalf("duplicates within one call must collapse: %v %v", err, added)
	}
	if !strings.HasPrefix(string(out), doc[:strings.LastIndex(doc, "      }\n    ]")+len("      }")]) {
		t.Fatalf("user's existing hook changed:\n%s", out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if n := len(m["hooks"].(map[string]any)["PreToolUse"].([]any)); n != 2 {
		t.Fatalf("want 2 entries, got %d\n%s", n, out)
	}
	// second run: nothing to add, byte-identical, even though the stored entry was pretty-printed
	again, added2, err := AppendRaw(out, []string{"hooks", "PreToolUse"}, []string{hookEntry})
	if err != nil || len(added2) != 0 || !bytes.Equal(again, out) {
		t.Fatalf("not idempotent: %v %v", err, added2)
	}
}

func TestAppendRawLayoutStyles(t *testing.T) {
	for name, doc := range map[string]string{
		"4 spaces": "{\n    \"a\": 1\n}\n",
		"tab":      "{\n\t\"a\": 1\n}\n",
		"compact":  `{"a":1}`,
		"crlf":     "{\r\n  \"a\": 1\r\n}\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := AppendRaw([]byte(doc), []string{"hooks", "Stop"}, []string{`{"hooks":[{"type":"command","command":"x"}]}`})
			if err != nil || !json.Valid(out) {
				t.Fatalf("%v\n%s", err, out)
			}
			s := string(out)
			switch name {
			case "compact":
				if strings.Contains(s, "\n") {
					t.Fatalf("compact doc got newlines: %q", s)
				}
			case "crlf":
				if strings.Contains(strings.ReplaceAll(s, "\r\n", ""), "\n") {
					t.Fatalf("bare LF in CRLF doc: %q", s)
				}
			case "tab":
				if !strings.Contains(s, "\n\t\t\"Stop\"") {
					t.Fatalf("tab indentation not followed: %q", s)
				}
			case "4 spaces":
				if !strings.Contains(s, "\n        \"Stop\"") {
					t.Fatalf("4-space indentation not followed: %q", s)
				}
			}
		})
	}
}

func TestReadRaw(t *testing.T) {
	doc := `{"hooks": {"Stop": [ {"a": 1,  "b": [1, 2]}, "s", 3 ]}}`
	got, err := ReadRaw([]byte(doc), []string{"hooks", "Stop"})
	if err != nil || len(got) != 3 || got[0] != `{"a":1,"b":[1,2]}` || got[1] != `"s"` || got[2] != "3" {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := ReadRaw([]byte(doc), []string{"hooks", "Nope"}); got != nil || err != nil {
		t.Fatalf("missing path: %v %v", got, err)
	}
	if _, err := ReadRaw([]byte(`{"hooks":"x"}`), []string{"hooks"}); err == nil {
		t.Fatal("non-array must be an error")
	}
	if got, err := ReadRaw(nil, []string{"a"}); got != nil || err != nil {
		t.Fatal("empty doc")
	}
}

func TestAppendRawRejectsInvalidValuesAndShapes(t *testing.T) {
	if _, _, err := AppendRaw([]byte(`{}`), []string{"a", "b"}, []string{`{not json`}); err == nil {
		t.Fatal("invalid JSON value must be rejected")
	}
	if _, _, err := AppendRaw([]byte(`{"a": {"b": "x"}}`), []string{"a", "b"}, []string{`1`}); !errors.Is(err, ErrWrongType) {
		t.Fatalf("target not an array: %v", err)
	}
	if _, _, err := AppendRaw([]byte(`{"a": 1}`), []string{"a", "b"}, []string{`1`}); !errors.Is(err, ErrWrongType) {
		t.Fatalf("parent not an object: %v", err)
	}
	// mixed element types are fine for raw arrays
	out, _, err := AppendRaw([]byte(`{"a": ["x", 2]}`), []string{"a"}, []string{`{"k": true}`})
	if err != nil || !json.Valid(out) {
		t.Fatalf("%v %s", err, out)
	}
}

// --- RemoveRaw / RemoveStrings ------------------------------------------------------------------

func TestRemoveStringsFirstMiddleLastOnly(t *testing.T) {
	doc := "{\n  \"permissions\": {\n    \"deny\": [\n      \"a\",\n      \"b\",\n      \"c\"\n    ]\n  }\n}\n"
	cases := []struct{ remove, want string }{
		{"a", "{\n  \"permissions\": {\n    \"deny\": [\n      \"b\",\n      \"c\"\n    ]\n  }\n}\n"},
		{"b", "{\n  \"permissions\": {\n    \"deny\": [\n      \"a\",\n      \"c\"\n    ]\n  }\n}\n"},
		{"c", "{\n  \"permissions\": {\n    \"deny\": [\n      \"a\",\n      \"b\"\n    ]\n  }\n}\n"},
	}
	for _, c := range cases {
		out, n, err := RemoveStrings([]byte(doc), deny, []string{c.remove})
		if err != nil || n != 1 || string(out) != c.want {
			t.Fatalf("remove %s: n=%d err=%v\n%s\nwant\n%s", c.remove, n, err, out, c.want)
		}
	}
	one := "{\n  \"permissions\": {\n    \"deny\": [\n      \"only\"\n    ]\n  }\n}\n"
	out, n, err := RemoveStrings([]byte(one), deny, []string{"only"})
	if err != nil || n != 1 || string(out) != "{\n  \"permissions\": {\n    \"deny\": []\n  }\n}\n" {
		t.Fatalf("only element: %v %d\n%s", err, n, out)
	}
}

func TestRemoveRawObjectsAndLayouts(t *testing.T) {
	entry := `{"matcher":"Bash","hooks":[{"type":"command","command":"rigfile","args":["hook","run","guard"]}]}`
	user := `{"matcher":"Write","hooks":[{"type":"command","command":"mine.sh"}]}`
	for name, doc := range map[string]string{
		"pretty":  "{\n  \"hooks\": {\n    \"PreToolUse\": [\n      " + user + ",\n      " + entry + "\n    ]\n  }\n}\n",
		"compact": `{"hooks":{"PreToolUse":[` + user + `,` + entry + `]}}`,
		"crlf":    "{\r\n  \"hooks\": {\r\n    \"PreToolUse\": [\r\n      " + user + ",\r\n      " + entry + "\r\n    ]\r\n  }\r\n}\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, n, err := RemoveRaw([]byte(doc), []string{"hooks", "PreToolUse"}, []string{entry})
			if err != nil || n != 1 || !json.Valid(out) {
				t.Fatalf("n=%d err=%v\n%s", n, err, out)
			}
			var m map[string]any
			_ = json.Unmarshal(out, &m)
			left := m["hooks"].(map[string]any)["PreToolUse"].([]any)
			if len(left) != 1 || left[0].(map[string]any)["matcher"] != "Write" {
				t.Fatalf("the user's hook must remain: %s", out)
			}
			if strings.Contains(string(out), "rigfile") {
				t.Fatalf("our entry still present: %s", out)
			}
		})
	}
}

func TestRemoveIsExactAndSafe(t *testing.T) {
	doc := `{"a": ["x, y", "x", ["x"], {"k": "x"}, 5]}`
	out, n, err := RemoveStrings([]byte(doc), []string{"a"}, []string{"x"})
	if err != nil || n != 1 || string(out) != `{"a": ["x, y", ["x"], {"k": "x"}, 5]}` {
		t.Fatalf("must remove only the exact string element: n=%d %v\n%s", n, err, out)
	}
	// no match / missing path / empty doc are no-ops
	same, n, err := RemoveStrings([]byte(doc), []string{"a"}, []string{"nope"})
	if err != nil || n != 0 || string(same) != doc {
		t.Fatal("no match must be a byte-identical no-op")
	}
	if same, n, _ := RemoveStrings([]byte(doc), []string{"b", "c"}, []string{"x"}); n != 0 || string(same) != doc {
		t.Fatal("missing path")
	}
	if _, n, err := RemoveStrings(nil, []string{"a"}, []string{"x"}); n != 0 || err != nil {
		t.Fatal("empty doc")
	}
	if _, _, err := RemoveStrings([]byte(`{"a": "s"}`), []string{"a"}, []string{"x"}); !errors.Is(err, ErrWrongType) {
		t.Fatalf("non-array: %v", err)
	}
	if _, _, err := RemoveStrings([]byte(`{`), []string{"a"}, []string{"x"}); !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("invalid: %v", err)
	}
	// duplicates are all removed
	out, n, _ = RemoveStrings([]byte(`{"a":["x","y","x"]}`), []string{"a"}, []string{"x"})
	if n != 2 || string(out) != `{"a":["y"]}` {
		t.Fatalf("n=%d %s", n, out)
	}
}

// Property: add then remove restores the original document (for docs where the array existed).
func TestAddThenRemoveRestoresOriginal(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 300; i++ {
		var have []string
		for j := 0; j < 1+rng.Intn(3); j++ {
			have = append(have, "r"+string(rune('a'+rng.Intn(20)))+string(rune('a'+j)))
		}
		base := map[string]any{"z": 1, "permissions": map[string]any{"deny": have}}
		var doc []byte
		switch rng.Intn(3) {
		case 0:
			doc, _ = json.Marshal(base)
		case 1:
			doc, _ = json.MarshalIndent(base, "", "  ")
			doc = append(doc, '\n')
		default:
			doc, _ = json.MarshalIndent(base, "", "\t")
			doc = append(doc, '\n')
		}
		added, _, err := AppendStrings(doc, deny, []string{"NEW-A", "NEW-B"})
		if err != nil {
			t.Fatal(err)
		}
		back, n, err := RemoveStrings(added, deny, []string{"NEW-A", "NEW-B"})
		if err != nil || n != 2 || !bytes.Equal(back, doc) {
			t.Fatalf("iter %d: not restored (n=%d err=%v)\n--- original ---\n%s\n--- added ---\n%s\n--- back ---\n%s", i, n, err, doc, added, back)
		}
	}
}
