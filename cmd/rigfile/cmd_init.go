package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/capture"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/models"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
	"github.com/digitaldreamer3462/rigfile/internal/targets"
)

// cmdInit captures an existing Claude Code setup into a new rig directory. It only READS the Claude
// Code config; it writes only inside --out, which must not already hold files.
func cmdInit(args []string, e env) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(e.err)
	claudeDir := fs.String("claude-dir", "", "Claude Code config directory (default ~/.claude or $CLAUDE_CONFIG_DIR)")
	out := fs.String("out", "rig", "directory to create the rig in (must be empty or absent)")
	name := fs.String("name", "local/my-rig", "rig name, owner/name (lowercase)")
	from := fs.String("from", "claude-code", "tool to capture: "+strings.Join(targets.Names(), ", "))
	dirFlag := fs.String("dir", "", "config directory of the tool given by --from (default: its usual location)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	home, err := pi.Home()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if *from != "claude-code" {
		return initFromTarget(e, pi, home, *from, *dirFlag, *out, *name)
	}
	cd := *claudeDir
	if cd == "" {
		if cd, err = claudecode.ClaudeDirFor(pi, e.getenv); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
	}
	cj := filepath.Join(home, ".claude.json")
	if d := e.getenv("CLAUDE_CONFIG_DIR"); d != "" && *claudeDir == "" {
		cj = filepath.Join(d, ".claude.json")
	} else if *claudeDir != "" {
		cj = filepath.Join(filepath.Dir(cd), ".claude.json")
	}
	if ents, err := os.ReadDir(*out); err == nil && len(ents) > 0 {
		fmt.Fprintf(e.err, "rigfile: %s already has files; choose an empty --out directory\n", *out)
		return 1
	}

	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	st, err := state.Load(sd)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	owned := map[string]bool{}
	if ts := st.Targets[claudecode.StateTarget]; ts != nil {
		for _, it := range ts.Items {
			switch {
			case it.Kind == state.KindJSONList && strings.HasPrefix(it.Detail["list"], "permissions."):
				owned["permission|"+strings.TrimPrefix(it.Detail["list"], "permissions.")+":"+it.Detail["value"]] = true
			case it.Kind == state.KindMCP:
				owned["mcp|"+it.Key] = true
			default:
				owned[it.Category+"|"+it.Key] = true
			}
		}
	}

	cp, err := claudecode.Capture(claudecode.CaptureOptions{
		ClaudeDir: cd, ClaudeJSON: cj, Home: home, Name: *name,
		Skip: func(cat, key string) bool { return owned[cat+"|"+key] },
	})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if err := cp.Problems(); err != nil {
		fmt.Fprintln(e.err, "rigfile: the captured rig does not validate (nothing written):", err)
		return 1
	}

	res := &capture.Result{Manifest: cp.Manifest, Files: cp.Files}
	for _, f := range cp.Report {
		res.Report = append(res.Report, capture.Finding(f))
	}
	return writeCaptured(e, res, *out)
}

// initFromTarget captures a non-Claude-Code tool. Items Rigfile applied earlier (state.json) are left out.
func initFromTarget(e env, pi *platform.Info, home, from, dir, out, name string) int {
	t, ok := targets.Get(from)
	if !ok || t.Capture == nil {
		fmt.Fprintf(e.err, "rigfile: cannot capture from %q (known: %s)\n", from, strings.Join(targets.Names(), ", "))
		return 2
	}
	if ents, err := os.ReadDir(out); err == nil && len(ents) > 0 {
		fmt.Fprintf(e.err, "rigfile: %s already has files; choose an empty --out directory\n", out)
		return 1
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	st, err := state.Load(sd)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	owned := map[string]bool{}
	if ts := st.Targets[from]; ts != nil {
		for _, it := range ts.Items {
			owned[it.Category+"|"+it.Key] = true
		}
	}
	res, err := t.Capture(targets.Ctx{Plat: pi, Getenv: e.getenv, Dir: dir}, capture.Options{
		Home: home, Name: name, Skip: func(cat, key string) bool { return owned[cat+"|"+key] },
	})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	return writeCaptured(e, res, out)
}

// addRunningModels appends a `models:` block for a local Ollama server answering on loopback. It probes the network port
// only. The block is kept only if the whole manifest still validates.
func addRunningModels(e env, cp *capture.Result) {
	var ds []models.Detected
	if e.detectModels != nil {
		ds = e.detectModels()
	} else {
		ds = models.DetectOllama(context.Background(), nil, models.OllamaPort)
	}
	section, notes := models.ManifestSection(ds)
	if section == "" {
		return
	}
	candidate := append(append([]byte(nil), cp.Manifest...), []byte("\n"+section)...)
	if _, err := manifest.Parse(candidate); err != nil {
		cp.Report = append(cp.Report, capture.Finding{Level: "note", Category: "models", Item: "ollama", Msg: "a running Ollama was found but its models could not be captured: " + err.Error()})
		return
	}
	cp.Manifest = candidate
	for _, d := range ds {
		cp.Report = append(cp.Report, capture.Finding{Level: "captured", Category: "model", Item: d.Model, Msg: "from the Ollama server on 127.0.0.1:" + strconv.Itoa(d.Port) + ", pinned by digest; only the network port was read"})
	}
	for _, n := range notes {
		cp.Report = append(cp.Report, capture.Finding{Level: "note", Category: "models", Item: "ollama", Msg: n})
	}
}

func writeCaptured(e env, cp *capture.Result, out string) int {
	addRunningModels(e, cp)
	if _, err := manifest.Parse(cp.Manifest); err != nil {
		fmt.Fprintln(e.err, "rigfile: the captured rig does not validate (nothing written):", err)
		return 1
	}
	paths := make([]string, 0, len(cp.Files)+1)
	for p := range cp.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	write := func(rel string, data []byte) error {
		dst := filepath.Join(out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		return os.WriteFile(dst, data, mode)
	}
	if err := write("rigfile.yaml", cp.Manifest); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	for _, p := range paths {
		if err := write(p, cp.Files[p]); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
	}

	printCaptureReport(e, cp)
	fmt.Fprintf(e.out, "\nWrote %s (%d file(s) plus rigfile.yaml)\n", out, len(paths))

	loaded, err := manifest.Load(out)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	problems := manifest.Check(loaded)
	for _, pr := range problems {
		fmt.Fprintln(e.out, pr)
	}
	if manifest.HasErrors(problems) {
		fmt.Fprintln(e.err, "rigfile: the captured rig has errors (above); fix them, then run `rigfile validate`")
		return 1
	}
	fmt.Fprintln(e.out, "\nNext: review the files (they are YOUR text), then `rigfile plan` to see what applying it would change.")
	return 0
}

func printCaptureReport(e env, cp *capture.Result) {
	order := []struct{ level, title string }{
		{"captured", "Captured"}, {"redacted", "Secrets: values NOT captured"}, {"skipped", "Skipped"}, {"note", "Notes"},
	}
	for _, o := range order {
		var lines []string
		for _, f := range cp.Report {
			if f.Level != o.level {
				continue
			}
			l := fmt.Sprintf("  %-11s %s", f.Category, f.Item)
			if f.Msg != "" {
				l += "   " + f.Msg
			}
			lines = append(lines, l)
		}
		if len(lines) > 0 {
			fmt.Fprintf(e.out, "%s (%d)\n%s\n\n", o.title, len(lines), strings.Join(lines, "\n"))
		}
	}
}
