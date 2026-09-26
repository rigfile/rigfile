package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/hook"
	"github.com/digitaldreamer3462/rigfile/internal/jsonedit"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// Canonical → Claude Code event names (docs/targets/claude-code.md §7).
var eventNames = map[string]string{
	"pre_tool_use": "PreToolUse", "post_tool_use": "PostToolUse", "permission_request": "PermissionRequest",
	"user_prompt_submit": "UserPromptSubmit", "session_start": "SessionStart", "session_end": "SessionEnd",
	"stop": "Stop", "subagent_start": "SubagentStart", "subagent_stop": "SubagentStop",
	"pre_compact": "PreCompact", "post_compact": "PostCompact", "notification": "Notification",
}

var toolNames = map[string]string{
	"bash": "Bash", "powershell": "PowerShell", "read": "Read", "edit": "Edit", "write": "Write", "glob": "Glob",
	"grep": "Grep", "web_fetch": "WebFetch", "web_search": "WebSearch", "agent": "Agent",
}

type hookHandler struct {
	Type    string   `json:"type"`
	If      string   `json:"if,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout int      `json:"timeout,omitempty"`
}

type hookGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookHandler `json:"hooks"`
}

// matcherFor maps a canonical match block to Claude Code's `matcher` and handler `if`.
func matcherFor(m manifest.HookMatch) (matcher, ifRule string, err error) {
	switch {
	case m.Tool == "" || m.Tool == "*":
	case strings.HasPrefix(m.Tool, "mcp"):
		matcher = "mcp__" + strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(m.Tool, "mcp"), ":"), ":", "__")
		if matcher == "mcp__" {
			matcher = "mcp__.*"
		}
	default:
		n, ok := toolNames[m.Tool]
		if !ok {
			return "", "", fmt.Errorf("unknown tool %q in hook match", m.Tool)
		}
		matcher = n
		if m.Tool == "edit" {
			matcher = "Edit|MultiEdit|NotebookEdit" // every tool that edits files (S2-M5): a hook must not miss MultiEdit
		}
	}
	switch {
	case m.Command != "" && (m.Tool == "bash" || m.Tool == "powershell"):
		ifRule = fmt.Sprintf("%s(%s)", matcher, m.Command)
	case m.Path != "" && (m.Tool == "read" || m.Tool == "edit" || m.Tool == "write"):
		n := toolNames[m.Tool]
		if m.Tool == "write" || m.Tool == "edit" {
			n = "Edit" // path rules for Write are never consulted; Edit(path) covers all writers (§8)
		}
		ifRule = fmt.Sprintf("%s(%s)", n, m.Path)
	case m.Command != "" || m.Path != "":
		return "", "", fmt.Errorf("match.command/path need a bash or file tool")
	}
	return matcher, ifRule, nil
}

// settings plans everything that lives in ~/.claude/settings.json (permissions, hooks) as ONE file
// write, so backups and rollback see one change. Hook scripts are separate owned files.
func (b *builder) settings(p *merge.Projection) {
	env := b.env
	path := filepath.Join(env.ClaudeDir, "settings.json")
	orig, _, err := readOptional(path)
	if err != nil {
		b.fail(err)
		return
	}
	work := orig
	var ops []engine.Op

	// ---- permissions (add-only union; provably shadowed allows are dropped and reported) ----
	pp, err := PlanPermissions(env.Plat, work, p.Permissions())
	if err != nil {
		b.fail(fmt.Errorf("%s: %w", env.short(path), err))
		return
	}
	for _, d := range pp.Dropped {
		b.note("permissions.%s %s %s", d.List, d.Rule, d.Reason)
	}
	if !pp.Empty() {
		if work, err = pp.Apply(work); err != nil {
			b.fail(err)
			return
		}
	}
	permOp := engine.Op{Category: "permission", Key: "settings.json", Symbol: engine.Unchanged, Summary: env.short(path) + "   (up to date)"}
	if !pp.Empty() {
		permOp.Symbol = engine.Update
		permOp.Summary = env.short(path)
		for _, c := range pp.Adds {
			permOp.Detail = append(permOp.Detail, fmt.Sprintf("+ %-5s %s", c.List, c.Rule))
			permOp.Items = append(permOp.Items, state.Item{
				Category: "permission", Key: c.List, Kind: state.KindJSONList, Path: path,
				Detail: map[string]string{"list": "permissions." + c.List, "value": c.Rule},
			})
		}
	}
	// rules Rigfile added on an earlier run that the rig STILL wants stay owned (they are "present" now)
	if env.State != nil {
		for _, c := range pp.Present {
			for _, it := range env.State.Items {
				if it.Kind == state.KindJSONList && it.Path == path && it.Detail["list"] == "permissions."+c.List && it.Detail["value"] == c.Rule {
					permOp.Items = appendUniqueItem(permOp.Items, it)
				}
			}
		}
	}
	if len(p.Deny)+len(p.Ask)+len(p.Allow) > 0 || !pp.Empty() {
		ops = append(ops, permOp)
	}

	// ---- base-secure settings ----
	if env.BaseSecure {
		if op, ok := b.settingOp(path, &work, "permissions.disableBypassPermissionsMode", "disable",
			"bypassPermissions mode skips the prompts that protect .git, .claude and shell rc files; base-secure turns it off (decision O8)"); ok {
			ops = append(ops, op)
		}
	}

	// ---- hooks ----
	for _, h := range p.Hooks {
		op, ok := b.hookOp(path, &work, h, p.OS)
		if ok {
			ops = append(ops, op)
		}
	}
	for _, id := range p.Unrunnable {
		b.note("hook %q has no command for %s; not installed", id, p.OS)
	}

	// ---- things Rigfile added earlier that the rig no longer wants ----
	b.keepOwnedOnConflict(ops)
	if env.State != nil {
		ids := b.plan.Identities()
		for _, o := range ops {
			for _, it := range append(append([]state.Item(nil), o.Items...), o.Keep...) {
				ids[state.Identity(it)] = true
			}
		}
		for _, prev := range env.State.Items {
			if prev.Path != path || ids[state.Identity(prev)] {
				continue
			}
			switch prev.Kind {
			case state.KindJSONList:
				list := strings.Split(prev.Detail["list"], ".")
				if prev.Detail["list"] == "permissions.deny" {
					b.note("deny rule %s is no longer in the rig; left in place (remove it by hand if you want it gone)", prev.Detail["value"])
					continue
				}
				next, n, err := jsonedit.RemoveStrings(work, list, []string{prev.Detail["value"]})
				if err != nil {
					b.fail(err)
					return
				}
				if n > 0 {
					work = next
					ops = append(ops, engine.Op{Category: "permission", Key: prev.Key, Symbol: engine.Removal,
						Summary: env.short(path), Detail: []string{fmt.Sprintf("- %-5s %s   (no longer in the rig)", prev.Key, prev.Detail["value"])}})
				}
			case state.KindJSONRaw:
				next, n, err := jsonedit.RemoveRaw(work, strings.Split(prev.Detail["list"], "."), []string{prev.Detail["raw"]})
				if err != nil {
					b.fail(err)
					return
				}
				if n > 0 {
					work = next
					ops = append(ops, engine.Op{Category: "hook", Key: prev.Key, Symbol: engine.Removal, Runs: true,
						Summary: prev.Key + "   (no longer in the rig)"})
				} else {
					b.note("hook %q is no longer in the rig but its settings entry was edited or removed by hand; nothing removed", prev.Key)
				}
			}
		}
	}

	// ---- one write for the whole file, attached to the first actionable op ----
	if !bytes.Equal(work, orig) {
		final := work
		for i := range ops {
			if ops[i].Actionable() {
				ops[i].Do = func(x *engine.Exec) error { _, err := x.W.WriteFile(path, final); return err }
				break
			}
		}
	}
	b.plan.Ops = append(b.plan.Ops, ops...)
}

// settingOp ensures a scalar setting exists with the wanted value, without ever overwriting the user's own
// choice: an existing different value is reported and left alone.
func (b *builder) settingOp(settingsPath string, work *[]byte, dotted, want, why string) (engine.Op, bool) {
	env := b.env
	segs := strings.Split(dotted, ".")
	next, existing, added, err := jsonedit.SetMissingString(*work, segs, want)
	if err != nil {
		b.fail(fmt.Errorf("%s: %w", env.short(settingsPath), err))
		return engine.Op{}, false
	}
	item := state.Item{Category: "setting", Key: dotted, Kind: state.KindJSONValue, Path: settingsPath, Hash: hashing.Bytes([]byte(want)),
		Detail: map[string]string{"path": dotted, "value": want}}
	op := engine.Op{Category: "setting", Key: dotted, Detail: []string{why}}
	label := env.short(settingsPath) + "   " + dotted + " = \"" + want + "\""
	switch {
	case added:
		*work = next
		op.Symbol, op.Summary, op.Items = engine.Update, label, []state.Item{item}
	case existing == want:
		op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
		if _, mine := env.owned("setting", dotted, settingsPath); mine {
			op.Items = []state.Item{item}
		}
	default:
		op.Symbol, op.Summary = engine.Conflict, label+"   your settings already say "+existing+"; not changed"
		if prev, mine := env.owned("setting", dotted, settingsPath); mine {
			op.Keep = []state.Item{prev}
		}
	}
	return op, true
}

func appendUniqueItem(list []state.Item, it state.Item) []state.Item {
	for _, e := range list {
		if e.Kind == it.Kind && e.Path == it.Path && e.Detail["list"] == it.Detail["list"] && e.Detail["value"] == it.Detail["value"] {
			return list
		}
	}
	return append(list, it)
}

// hookOp adds one hook to *work and returns its op. Script hooks also get an owned script file op
// (appended to the plan directly); the settings entry is what the returned op describes.
func (b *builder) hookOp(settingsPath string, work *[]byte, h merge.Prov[manifest.Hook], osName string) (engine.Op, bool) {
	env := b.env
	id := h.V.Key()
	event, ok := eventNames[h.V.Event]
	if !ok {
		b.fail(fmt.Errorf("hook %s: unsupported event %q", id, h.V.Event))
		return engine.Op{}, false
	}
	matcher, ifRule, err := matcherFor(h.V.Match)
	if err != nil {
		b.fail(fmt.Errorf("hook %s: %w", id, err))
		return engine.Op{}, false
	}
	run := h.V.Run.For(osName)
	if run == "" {
		return engine.Op{}, false // no version for this OS: reported as a note from Projection.Unrunnable
	}
	handler := hookHandler{Type: "command", If: ifRule, Timeout: h.V.TimeoutSeconds}
	summary := fmt.Sprintf("%s [%s]", id, event)
	var detail []string

	if name, isBuiltin := manifest.Builtin(run); isBuiltin {
		if !hook.KnownBuiltin(name) {
			return engine.Op{Category: "hook", Key: id, Symbol: engine.Conflict,
				Summary: fmt.Sprintf("%s [%s]   unknown built-in hook %q; not installed", id, event, name)}, true
		}
		if !hook.SupportsEvent(name, h.V.Event) {
			return engine.Op{Category: "hook", Key: id, Symbol: engine.Conflict,
				Summary: fmt.Sprintf("%s [%s]   built-in %q does not handle this event; not installed", id, event, name)}, true
		}
		handler.Command, handler.Args = env.rigfile(), []string{"hook", "run", name}
		detail = append(detail, "runs the built-in `rigfile hook run "+name+"` (native binary, same on every OS)")
	} else {
		src, err := srcPath(h.Dir, run)
		if err != nil {
			b.fail(fmt.Errorf("hook %s: %w", id, err))
			return engine.Op{}, false
		}
		data, err := os.ReadFile(src)
		if err != nil {
			b.fail(err)
			return engine.Op{}, false
		}
		dest := filepath.Join(env.ClaudeDir, "rigfile", "hooks", keySlug(id), filepath.Base(run))
		b.fileOp("hook", id+" script", dest, data, 0o755, h.Layer, true)
		handler.Command, handler.Args = scriptCommand(env.Plat, dest)
		detail = append(detail, "runs "+env.short(dest)+"   [view source: "+env.short(src)+"]")
	}

	group := hookGroup{Matcher: matcher, Hooks: []hookHandler{handler}}
	raw, err := json.Marshal(group)
	if err != nil {
		b.fail(err)
		return engine.Op{}, false
	}
	list := []string{"hooks", event}
	next, added, err := jsonedit.AppendRaw(*work, list, []string{string(raw)})
	if err != nil {
		b.fail(fmt.Errorf("hook %s: %w", id, err))
		return engine.Op{}, false
	}
	compacted, _ := compactJSON(string(raw))
	op := engine.Op{
		Category: "hook", Key: id, Runs: true, Detail: detail,
		Items: []state.Item{{Category: "hook", Key: id, Kind: state.KindJSONRaw, Path: settingsPath,
			Hash: hashing.Bytes([]byte(compacted)), Detail: map[string]string{"list": "hooks." + event, "raw": compacted}}},
	}
	if len(added) == 0 {
		op.Symbol, op.Summary = engine.Unchanged, summary+"   (up to date)"
		return op, true
	}
	*work = next
	op.Symbol, op.Summary = engine.New, summary
	return op, true
}

func compactJSON(raw string) (string, error) {
	var out bytes.Buffer
	err := json.Compact(&out, []byte(raw))
	return out.String(), err
}

// scriptCommand returns the exec-form command for a hook script (docs/targets/claude-code.md §7): on
// Windows a .ps1 is launched through PowerShell; everything else is spawned directly.
func scriptCommand(pi *platform.Info, path string) (string, []string) {
	if pi.OS == platform.Windows && strings.EqualFold(filepath.Ext(path), ".ps1") {
		return "powershell.exe", []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", path}
	}
	return path, []string{}
}
