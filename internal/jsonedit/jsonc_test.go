package jsonedit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommentsSurviveEveryEdit(t *testing.T) {
	doc := "{\n  // the team's own servers\n  \"servers\": {\n    /* theirs */\n    \"theirs\": {\"command\": \"x\"}, // keep me\n  },\n  \"inputs\": [], // trailing comma above and comment here\n}\n"
	// add a member
	out, _, added, err := SetMissingRaw([]byte(doc), []string{"servers", "mine"}, `{"command":"y"}`)
	if err != nil || !added {
		t.Fatalf("%v %v", added, err)
	}
	// every comment survives (one that sat on the same line as the member before the insertion may now follow the new member)
	for _, keep := range []string{"// the team's own servers", "/* theirs */", "// keep me", "// trailing comma above and comment here"} {
		if !strings.Contains(string(out), keep) {
			t.Errorf("lost %q:\n%s", keep, out)
		}
	}
	if v, ok := ReadValueRaw(out, []string{"servers", "mine", "command"}); !ok || v != `"y"` {
		t.Fatalf("%q %v\n%s", v, ok, out)
	}
	if v, ok := ReadValueRaw(out, []string{"servers", "theirs", "command"}); !ok || v != `"x"` {
		t.Fatalf("the existing member must be untouched: %q", v)
	}
	// replace and remove our member; the others' comments are still there
	out2, replaced, err := ReplaceRaw(out, []string{"servers", "mine"}, `{"command":"z"}`)
	if err != nil || !replaced || !strings.Contains(string(out2), "/* theirs */") {
		t.Fatalf("%v %v\n%s", replaced, err, out2)
	}
	if v, _ := ReadValueRaw(out2, []string{"servers", "mine", "command"}); v != `"z"` {
		t.Fatalf("%q", v)
	}
	out3, removed, err := RemoveMember(out2, []string{"servers", "mine"})
	if err != nil || !removed || strings.Contains(string(out3), `"mine"`) || !strings.Contains(string(out3), "/* theirs */") || !strings.Contains(string(out3), "// the team's own servers") {
		t.Fatalf("%v %v\n%s", removed, err, out3)
	}
	if v, ok := ReadValueRaw(out3, []string{"servers", "theirs", "command"}); !ok || v != `"x"` {
		t.Fatalf("%q", v)
	}
	// a header comment before the root object
	small := "// header\n{\n  \"a\": 1 // one\n}\n"
	o, _, added, err := SetMissingRaw([]byte(small), []string{"b"}, "2")
	if err != nil || !added || !strings.HasPrefix(string(o), "// header\n{") || !strings.Contains(string(o), "// one") {
		t.Fatalf("%v %v\n%s", added, err, o)
	}
	if v, _ := ReadValueRaw(o, []string{"b"}); v != "2" {
		t.Fatalf("%q", v)
	}
}

func TestSlashesInsideStringsAreNotComments(t *testing.T) {
	doc := `{"url": "https://example.test/a//b", "glob": "/* not a comment */", "k": {}}`
	out, _, added, err := SetMissingRaw([]byte(doc), []string{"k", "m"}, "1")
	_ = added
	if err != nil || !strings.Contains(string(out), "https://example.test/a//b") || !strings.Contains(string(out), "/* not a comment */") || !json.Valid(out) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestPlainJSONIsUnchangedByTheWrapper(t *testing.T) {
	doc := "{\n  \"k\": {}\n}\n"
	out, _, _, err := SetMissingRaw([]byte(doc), []string{"k", "m"}, `{"a":1}`)
	if err != nil || !json.Valid(out) || !strings.Contains(string(out), `"m"`) {
		t.Fatalf("%v %s", err, out)
	}
}

func TestUnterminatedCommentsAndStringsAreInvalid(t *testing.T) {
	for _, bad := range []string{"{ /* never closed", "{\"a\": \"open", "{ \"a\": 1 "} {
		if _, _, err := AppendRaw([]byte(bad), []string{"b"}, []string{"1"}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestMaskKeepsOffsets(t *testing.T) {
	doc := []byte("{\n // c\n \"a\": [1,2,], /* x */\n}\n")
	sh, masked, ok := mask(doc)
	if !ok || !masked || len(sh) != len(doc) || !json.Valid(sh) {
		t.Fatalf("%v %v %q", ok, masked, sh)
	}
	for i := range doc {
		if doc[i] != sh[i] && sh[i] != ' ' {
			t.Fatalf("only blanks may differ: %d", i)
		}
	}
}
