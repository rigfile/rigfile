package localui

import (
	"fmt"
	"strings"
	"testing"
)

// realOpRow builds a line exactly as internal/engine.Plan.Render does, so this test exercises the real byte
// format rather than a guess at it.
func realOpRow(title, symbol, summary string) string {
	return fmt.Sprintf("%-13s %s %s", title, symbol, summary)
}

func TestParseOpRow(t *testing.T) {
	line := realOpRow("HOOKS", "+", "base-secure-guard [PreToolUse]   ⚠ executes code")
	label, sym, rest, ok := parseOpRow(line)
	if !ok || label != "HOOKS" || sym != '+' || !strings.HasPrefix(rest, "base-secure-guard") {
		t.Fatalf("label=%q sym=%c rest=%q ok=%v", label, sym, rest, ok)
	}
	// a continuation row: empty title, per Plan.Render's second Sprintf ("%-13s     %s")
	cont := fmt.Sprintf("%-13s     %s", "", "runs the built-in `rigfile hook run guard`")
	if _, _, _, ok := parseOpRow(cont); ok {
		t.Fatal("a detail/continuation line (5-space gap, not the op-row's 1-space-symbol-1-space) must not parse as an op row")
	}
	for _, bad := range []string{
		"",
		"short",
		realOpRow("HOOKS", "x", "not a real symbol"), // 'x' is not one of + ~ = ! -
		"  + skills/x <script>alert(1)</script>",     // hand-written test payload, not the real fixed-width format
	} {
		if _, _, _, ok := parseOpRow(bad); ok {
			t.Errorf("must not parse as an op row: %q", bad)
		}
	}
}

func TestTranscriptEscapesEverything(t *testing.T) {
	text := "Rig: ada/demo@1.0.0\n" + realOpRow("SKILLS", "+", `<script>alert(1)</script>`) + "\nNOTE          <img onerror=x>"
	got := string(transcript(text))
	if strings.Contains(got, "<script>") || strings.Contains(got, "onerror=x>") {
		t.Fatalf("raw markup leaked through: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(got, "&lt;img onerror=x&gt;") {
		t.Fatalf("expected the escaped forms to be present: %s", got)
	}
	if strings.Contains(got, "style=") {
		t.Fatal("no inline style is ever allowed (docs/local-ui.md CSP)")
	}
}

func TestTranscriptSectionsBySymbolAndCollapsesNotApplicable(t *testing.T) {
	var b strings.Builder
	b.WriteString("Claude Code\n")
	b.WriteString(realOpRow("HOOKS", "+", "base-secure-guard [PreToolUse]") + "\n")
	b.WriteString(fmt.Sprintf("%-13s     %s\n", "", "runs the built-in `rigfile hook run guard`"))
	b.WriteString(realOpRow("PERMISSIONS", "~", "~/.claude/settings.json") + "\n")
	b.WriteString("NOT APPLICABLE HERE\n")
	b.WriteString(`              hook "base-secure-guard-powershell" (os: [windows])` + "\n")
	b.WriteString(`              permission ask "git push*" (os: [windows])` + "\n")
	b.WriteString("NOTE          a note for the reader\n")
	b.WriteString("2 change(s)")
	got := string(transcript(b.String()))

	for _, want := range []string{
		`<h3>Claude Code</h3>`,
		`class="cat">HOOKS<`, `class="sym op-new">+<`,
		`class="cat">PERMISSIONS<`, `class="sym op-update">~<`,
		`<details class="noise"><summary>Not applicable here (2 items)</summary>`,
		`class="note">a note for the reader<`,
		`class="ln">2 change(s)<`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// the windows-only noise is inside the collapsed <details>, not rendered as top-level rows
	if i := strings.Index(got, "</details>"); i < 0 || !strings.Contains(got[:i], "base-secure-guard-powershell") {
		t.Fatalf("the collapsed items must be inside the <details>:\n%s", got)
	}
}

func TestTranscriptNeverProducesScriptOrInlineStyle(t *testing.T) {
	// a defensive fuzz-ish pass over some awkward inputs: blank text, only noise, unmatched details marker,
	// a line that is exactly 16 bytes (the parseOpRow boundary), CRLF-ish stray characters.
	for _, text := range []string{
		"",
		"NOT APPLICABLE HERE",
		"NOT APPLICABLE HERE\n   only one indented line, never closed",
		realOpRow("X", "+", ""),
		strings.Repeat("a", 13) + " + " + strings.Repeat("<script>", 50),
	} {
		got := string(transcript(text))
		if strings.Contains(got, "<script") || strings.Contains(got, "style=") {
			t.Fatalf("input %q produced unsafe output: %s", text, got)
		}
	}
}
