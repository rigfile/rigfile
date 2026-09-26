package tui

import (
	"bytes"
	"strings"
	"testing"
)

func items() []Item {
	return []Item{
		{Group: "skills", Label: "pdf", Detail: "~/.claude/skills/pdf", Checked: true},
		{Group: "skills", Label: "notes", Checked: true},
		{Group: "instructions", Label: "CLAUDE.md", Warn: "may contain personal information"},
	}
}

func run(t *testing.T, keys string, ansi bool) ([]bool, bool, string) {
	var out bytes.Buffer
	sel, ok, err := Checklist(strings.NewReader(keys), &out, items(), Options{Title: "Publish", ANSI: ansi})
	if err != nil {
		t.Fatal(err)
	}
	return sel, ok, out.String()
}

func TestDefaultsAndConfirm(t *testing.T) {
	sel, ok, out := run(t, "\r", false)
	if !ok || !sel[0] || !sel[1] || sel[2] {
		t.Fatalf("%v %v", sel, ok)
	}
	for _, want := range []string{"Publish", "SKILLS", "INSTRUCTIONS", "[x] pdf", "~/.claude/skills/pdf", "[ ] CLAUDE.md", "! may contain personal information"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestKeysToggleMoveAllNone(t *testing.T) {
	// vi key down (to "notes"), space (unticks it), arrow down (to CLAUDE.md), space (ticks it), enter
	sel, ok, _ := run(t, "j \x1b[B \r", false)
	if !ok || !sel[0] || sel[1] || !sel[2] {
		t.Fatalf("%v", sel)
	}
	sel, _, _ = run(t, "n\r", false)
	if sel[0] || sel[1] || sel[2] {
		t.Fatalf("none: %v", sel)
	}
	sel, _, _ = run(t, "a\r", false)
	if !sel[0] || !sel[1] || !sel[2] {
		t.Fatalf("all: %v", sel)
	}
	sel, _, _ = run(t, "k \r", false) // wraps to the last item
	if !sel[2] {
		t.Fatalf("wrap: %v", sel)
	}
}

func TestCancel(t *testing.T) {
	for _, keys := range []string{"q", "\x03", "\x1b", "j "} { // the last one: input ends without confirming
		if _, ok, _ := run(t, keys, false); ok {
			t.Errorf("%q should cancel", keys)
		}
	}
}

func TestANSIRedrawsInPlace(t *testing.T) {
	_, _, out := run(t, " \r", true)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("no cursor movement: %q", out)
	}
}

func TestEmptyList(t *testing.T) {
	sel, ok, err := Checklist(strings.NewReader(""), &bytes.Buffer{}, nil, Options{})
	if err != nil || !ok || len(sel) != 0 {
		t.Fatal()
	}
}
