// Package hook implements the built-in agent hooks (`rigfile hook <event>`), the portable
// replacement for shell-script hooks: one native binary, identical on every OS (plan §6.2, ADR 0001).
//
// Spike scope: the PreToolUse guard for Claude Code with a handful of base-secure rules (plan §8.2):
//   - deny bypassing git hooks (--no-verify, core.hooksPath changes)
//   - deny `git add` of credential-looking files
//   - ask before piping a download into a shell
//   - deny writing secret-shaped values into files
//
// Threat note: a hook is the SECOND layer. Claude Code documents hook `if` filters and Bash pattern
// matching as best-effort; hard denies belong in permissions.deny (docs/targets/claude-code.md §7-8).
// Command parsing here is a deliberately simple tokenizer, so it can be evaded by obfuscation
// (`eval`, base64, aliases). Malformed hook input fails CLOSED (blocks); an unrecognised tool or
// event yields no opinion.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/scan"
)

// Input is the subset of Claude Code's PreToolUse JSON (docs/targets/claude-code.md §7) we read.
type Input struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"` // PostToolUse
	CWD           string          `json:"cwd"`
}

// Verdict is the outcome of a rule.
type Verdict string

const (
	NoOpinion Verdict = ""
	Deny      Verdict = "deny"
	Ask       Verdict = "ask"
)

// Decision is what the hook tells Claude Code.
type Decision struct {
	Verdict Verdict
	Reason  string
	Rule    string // rule id, for tests and `doctor`
}

// ErrMalformed is returned for unreadable hook input; the caller must block (exit 2).
var ErrMalformed = fmt.Errorf("hook: malformed input")

// ParseInput reads a hook payload.
func ParseInput(r io.Reader) (Input, error) {
	var in Input
	dec := json.NewDecoder(io.LimitReader(r, 4<<20)) // hook payloads can carry file contents; cap at 4 MiB
	if err := dec.Decode(&in); err != nil {
		return Input{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return in, nil
}

// PreToolUse evaluates the built-in rules for one tool call.
func PreToolUse(in Input) Decision {
	switch in.ToolName {
	case "Bash", "PowerShell":
		var ti struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(in.ToolInput, &ti) != nil || ti.Command == "" {
			return Decision{}
		}
		return checkShell(ti.Command)
	case "Write", "Edit", "NotebookEdit":
		var ti struct {
			FilePath  string `json:"file_path"`
			Content   string `json:"content"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal(in.ToolInput, &ti) != nil {
			return Decision{}
		}
		if scan.LooksLikeSecret(ti.Content) || scan.LooksLikeSecret(ti.NewString) {
			return Decision{Deny, "Refusing to write what looks like a secret value into " + scan.Base(ti.FilePath) +
				". Use a secret:// reference or an environment variable instead, and tell the user which secret is needed.", "no-secret-writes"}
		}
	}
	return Decision{}
}

// --- shell command rules --------------------------------------------------------------------

var (
	hooksPath = regexp.MustCompile(`(?i)\bcore\.hooksPath\b`)
	// `bash <(curl ...)`, `sh -c "$(curl ...)"`, `eval "$(wget ...)"`: a download executed without a pipe
	subToShell  = regexp.MustCompile(`(?is)\b(ba|z|da|k)?sh\b[^;&|]*(<\(|\$\()\s*(curl|wget)\b|\beval\b[^;&|]*(\$\(|` + "`" + `)\s*(curl|wget)\b|\bsource\s+<\(\s*(curl|wget)\b`)
	pipeToShell = regexp.MustCompile(`(?is)\b(curl|wget)\b[^|;&]*\|\s*(sudo\s+)?(ba|z|da|k)?sh\b|\b(iwr|irm|invoke-webrequest|invoke-restmethod)\b[^|;&]*\|\s*(iex|invoke-expression)\b`)
)

func noVerifyFlag(sub string, rest []string) bool {
	// Long options that take their value as the NEXT token (so that token is text, not a flag).
	valueLong := map[string]bool{"--message": true, "--file": true, "--author": true, "--date": true, "--cleanup": true,
		"--reuse-message": true, "--reedit-message": true, "--fixup": true, "--squash": true, "--gpg-sign": false}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--no-verify" {
			return true
		}
		if valueLong[a] {
			i++ // skip its value
			continue
		}
		// `git commit -n` == --no-verify (for push/merge, -n means something else). Short flags can be
		// clustered (-anm "msg"): scan the cluster left to right. A value-taking flag (-m -F -C -c -S -J)
		// ends the scan: the rest of the token is its value, or, if it is the last character, the NEXT
		// token is.
		if sub == "commit" && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			cluster := a[1:]
			for idx, c := range cluster {
				if c == 'n' {
					return true
				}
				if strings.ContainsRune("mFCcSJ", c) {
					if idx == len(cluster)-1 {
						i++
					}
					break
				}
			}
		}
	}
	return false
}

// gitSubcommand skips git's global options to find the subcommand.
func gitSubcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || a == "-C" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i++ // takes a value
		case strings.HasPrefix(a, "-"):
			// standalone global flag (or --opt=value)
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// splitCommands splits a command line on ;, &&, ||, |, & and newlines, outside quotes.
func splitCommands(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if strings.TrimSpace(b.String()) != "" {
			out = append(out, b.String())
		}
		b.Reset()
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			b.WriteRune(c)
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(rs) {
				i++
				b.WriteRune(rs[i])
			}
		case c == '\'' || c == '"':
			quote = c
			b.WriteRune(c)
		case c == ';' || c == '\n' || c == '|' || c == '&' || c == '(' || c == ')' || c == '`':
			flush()
		default:
			b.WriteRune(c)
		}
	}
	flush()
	return out
}

// fields splits on whitespace outside quotes and strips the quotes.
func fields(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	has := false
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				b.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, has = c, true
		case c == ' ' || c == '\t':
			if has || b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
				has = false
			}
		default:
			b.WriteRune(c)
		}
	}
	if has || b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// Output renders the decision as Claude Code's PreToolUse JSON. An empty verdict renders nothing.
func (d Decision) Output() ([]byte, error) {
	if d.Verdict == NoOpinion {
		return nil, nil
	}
	return json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       string(d.Verdict),
			"permissionDecisionReason": d.Reason,
		},
	})
}

// Builtins are the hook names a rig may reference as `run: builtin:<name>`, with the canonical events
// each supports. `rigfile hook run <name>` dispatches to them.
var Builtins = map[string][]string{
	"guard":       {"pre_tool_use"},  // shell command rules (S2-M5: full §8.2 list)
	"write-guard": {"pre_tool_use"},  // secrets in Write/Edit content
	"redact":      {"post_tool_use"}, // secrets in tool output, before the model sees it
}

// KnownBuiltin reports whether name is a built-in hook.
func KnownBuiltin(name string) bool { _, ok := Builtins[name]; return ok }

// SupportsEvent reports whether the built-in handles the canonical event.
func SupportsEvent(name, event string) bool {
	for _, e := range Builtins[name] {
		if e == event {
			return true
		}
	}
	return false
}
