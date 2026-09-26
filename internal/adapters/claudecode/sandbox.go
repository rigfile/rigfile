package claudecode

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/jsonedit"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// Claude Code's sandbox (docs/targets/claude-code.md §12.5, §12.7) is the only OS-level enforcement for what
// Bash commands and their children can read: permission rules and hooks look at command TEXT, so a script
// that opens ~/.ssh/id_rsa itself passes both. base-secure offers it as an OPT-IN profile (`rigfile apply
// --sandbox`, remembered in state.json): macOS (Seatbelt), Linux and WSL2 (bubblewrap + socat), not native
// Windows. The profile only ever adds: user-set values are never overwritten, and it never touches hooks or
// permission rules.

var sandboxCredentialFiles = []string{
	"~/.ssh", "~/.aws", "~/.config/gcloud", "~/.azure", "~/.kube", "~/.gnupg", "~/.docker/config.json", "~/.npmrc", "~/.pypirc", "~/.netrc",
	"~/.claude/.credentials.json", "~/.claude.json", "~/.rigfile", "~/.config/rigfile",
}

var sandboxCredentialEnv = []string{
	"AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", "NPM_TOKEN", "OPENAI_API_KEY", "ANTHROPIC_API_KEY",
}

func (e Env) have(cmd string) bool {
	if e.Have != nil {
		return e.Have(cmd)
	}
	_, err := exec.LookPath(cmd)
	return err == nil
}

func (b *builder) sandboxOps(settingsPath string, work *[]byte) []engine.Op {
	env := b.env
	if env.Plat.OS == platform.Windows {
		b.note("the Claude Code sandbox is not available on native Windows; --sandbox was ignored (use WSL2)")
		return nil
	}
	if env.Plat.OS == platform.Linux {
		var missing []string
		for _, c := range []string{"bwrap", "socat"} {
			if !env.have(c) {
				missing = append(missing, map[string]string{"bwrap": "bubblewrap", "socat": "socat"}[c])
			}
		}
		if len(missing) > 0 {
			b.note("the sandbox needs %s on Linux: without it Claude Code warns and runs commands UNSANDBOXED. Install it yourself (Rigfile never runs sudo): sudo apt-get install %s   or   sudo dnf install %s",
				strings.Join(missing, " and "), strings.Join(missing, " "), strings.Join(missing, " "))
		}
	}
	var ops []engine.Op
	for _, s := range []struct{ key, raw, why string }{
		{"sandbox.enabled", "true", "commands run inside an OS-level sandbox: filesystem and network isolation for Bash and its children"},
		{"sandbox.allowUnsandboxedCommands", "false", "strict: Claude cannot retry a blocked command outside the sandbox with dangerouslyDisableSandbox"},
	} {
		if op, ok := b.settingOp(settingsPath, work, s.key, s.raw, s.why); ok {
			ops = append(ops, op)
		}
	}
	ops = append(ops, b.sandboxList(settingsPath, work, "sandbox.credentials.files", credentialFileEntries(),
		"sandbox: credential files and directories are unreadable to sandboxed commands (includes Python/Node scripts that open them)"))
	ops = append(ops, b.sandboxList(settingsPath, work, "sandbox.credentials.envVars", credentialEnvEntries(),
		"sandbox: these environment variables are removed from sandboxed commands"))
	return ops
}

func credentialFileEntries() []string {
	var out []string
	for _, p := range sandboxCredentialFiles {
		b, _ := json.Marshal(map[string]string{"path": p, "mode": "deny"})
		out = append(out, string(b))
	}
	return out
}

func credentialEnvEntries() []string {
	var out []string
	for _, n := range sandboxCredentialEnv {
		b, _ := json.Marshal(map[string]string{"name": n, "mode": "deny"})
		out = append(out, string(b))
	}
	return out
}

// sandboxList adds structured entries to a JSON array (owned per entry, removed again by the usual orphan pass).
func (b *builder) sandboxList(settingsPath string, work *[]byte, dotted string, entries []string, why string) engine.Op {
	env := b.env
	segs := strings.Split(dotted, ".")
	next, added, err := jsonedit.AppendRaw(*work, segs, entries)
	if err != nil {
		b.fail(fmt.Errorf("%s: %w", env.short(settingsPath), err))
		return engine.Op{}
	}
	op := engine.Op{Category: "setting", Key: dotted, Detail: []string{why}}
	for _, e := range entries {
		compacted, _ := compactJSON(e)
		op.Items = append(op.Items, state.Item{Category: "setting", Key: dotted, Kind: state.KindJSONRaw, Path: settingsPath,
			Hash: hashing.Bytes([]byte(compacted)), Detail: map[string]string{"list": dotted, "raw": compacted, "path": dotted}})
	}
	label := env.short(settingsPath) + "   " + dotted
	if len(added) == 0 {
		op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
		return op
	}
	*work = next
	op.Symbol, op.Summary = engine.Update, fmt.Sprintf("%s   (+%d entr%s)", label, len(added), map[bool]string{true: "y", false: "ies"}[len(added) == 1])
	return op
}
