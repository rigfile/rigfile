package hook

import (
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/scan"
)

// ScannerFunc builds the scanner on first use (compiling the rules costs tens of milliseconds, which hooks
// that end up with nothing to scan must not pay).
type ScannerFunc func() (*scan.Scanner, error)

// WriteGuard is the `write-guard` built-in (PreToolUse on Write/Edit/MultiEdit/NotebookEdit): it runs the
// full secret scanner over the content the agent is about to write. A secret is denied, with a message that
// names the rule but never the value and says what to do instead; when the target file is git-ignored the
// answer is "ask" (a developer may legitimately keep a real value in an ignored .env). File names are not
// judged here: the deny rules and the git hooks own that.
func WriteGuard(in Input, scf ScannerFunc) Decision {
	switch in.ToolName {
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
	default:
		return Decision{}
	}
	file, texts := writeTexts(in.ToolInput)
	joined := strings.Join(texts, "\n")
	if strings.TrimSpace(joined) == "" {
		return Decision{}
	}
	sc, err := scf()
	if err != nil {
		return Decision{Deny, "The secret scanner is unavailable (" + err.Error() + "), so this write cannot be checked. Run `rigfile doctor`.", "write-guard-unavailable"}
	}
	fs := sc.ScanText("", joined)
	if len(fs) == 0 {
		return Decision{}
	}
	rules := map[string]bool{}
	var names []string
	for _, f := range fs {
		if !rules[f.RuleID] {
			rules[f.RuleID] = true
			names = append(names, f.RuleID)
		}
	}
	msg := "This write to " + scan.Base(file) + " contains what looks like a secret (" + strings.Join(names, ", ") +
		"). Do not write secret values into files: use an environment variable or a secret:// reference, and tell the user which secret is needed."
	if file != "" && gitIgnored(in.CWD, file) {
		return Decision{Ask, msg + " (The file is git-ignored, so the user may still approve it.)", "ask-secret-in-ignored-file"}
	}
	return Decision{Deny, msg, "no-secret-writes"}
}

// writeTexts extracts the file path and every piece of new text from a write-like tool input. Both the
// documented field names and the ones seen in practice are read, so a schema difference cannot silently
// disable the guard.
func writeTexts(raw json.RawMessage) (file string, texts []string) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return "", nil
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(m[k], &s)
		return s
	}
	file = str("file_path")
	if file == "" {
		file = str("notebook_path")
	}
	for _, k := range []string{"content", "file_text", "new_string", "new_str", "new_source", "text"} {
		if s := str(k); s != "" {
			texts = append(texts, s)
		}
	}
	var edits []map[string]json.RawMessage
	if json.Unmarshal(m["edits"], &edits) == nil {
		for _, e := range edits {
			for _, k := range []string{"new_string", "new_str"} {
				var s string
				if json.Unmarshal(e[k], &s) == nil && s != "" {
					texts = append(texts, s)
				}
			}
		}
	}
	return file, texts
}

func gitIgnored(cwd, file string) bool {
	c := exec.Command("git", "check-ignore", "-q", "--", file)
	c.Dir = cwd
	return c.Run() == nil
}

// Redact is the `redact` built-in (PostToolUse): if the tool's output contains secrets it returns the
// hookSpecificOutput that replaces the output with a redacted copy before the model sees it. ok is false when
// there is nothing to change. `updatedToolOutput` is documented to replace a tool's text output for every
// tool; its exact value type and the tool_response shapes are UNVERIFIED (docs/targets/claude-code.md §12)
// and are covered by the live red-team procedure, so the output is plain text and a warning is always added
// through `additionalContext` as well.
func Redact(in Input, scf ScannerFunc) (out []byte, ok bool, err error) {
	text := responseText(in.ToolResponse)
	if strings.TrimSpace(text) == "" {
		return nil, false, nil
	}
	sc, err := scf()
	if err != nil {
		return nil, false, err
	}
	red, fs := sc.Redact(text)
	if len(fs) == 0 || red == text {
		return nil, false, nil
	}
	b, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "PostToolUse",
			"updatedToolOutput": red,
			"additionalContext": "Rigfile replaced secret-looking values in this tool output with [REDACTED:<rule>]. Do not try to recover or repeat them; tell the user which file or command produced one.",
		},
	})
	return b, err == nil, err
}

// responseText flattens a tool_response (a string, or an object with stdout/stderr/content/output/text
// fields, or anything else as compact JSON) into the text the model would read.
func responseText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		var parts []string
		for _, k := range []string{"stdout", "stderr", "output", "content", "text", "result"} {
			var v string
			if json.Unmarshal(m[k], &v) == nil && v != "" {
				parts = append(parts, v)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return string(raw)
}
