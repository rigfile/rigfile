package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/rigfile/rigfile/internal/adapters/claudecode"
	"github.com/rigfile/rigfile/internal/capture"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/publish"
	"github.com/rigfile/rigfile/internal/regclient"
	"github.com/rigfile/rigfile/internal/state"
	"github.com/rigfile/rigfile/internal/targets"
	"github.com/rigfile/rigfile/internal/tui"
)

// cmdPublish writes a clean, scrubbed rig repository (docs/sharing.md §6): from a rig directory, or from a capture of
// this machine's tool setup with a checklist to choose what goes in. It never uploads anything.
func cmdPublish(args []string, e env) int {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(e.err)
	toGit := fs.String("to-git", "", "directory to write the clean repository into (must be empty or absent)")
	name := fs.String("name", "", "rig name, owner/name (required when capturing this machine)")
	from := fs.String("from", "claude-code", "tool to capture when no rig directory is given: "+strings.Join(targets.Names(), ", "))
	dir := fs.String("dir", "", "config directory of the tool given by --from (default: its usual location)")
	claudeDir := fs.String("claude-dir", "", "Claude Code config directory (default ~/.claude or $CLAUDE_CONFIG_DIR)")
	all := fs.Bool("all", false, "include everything that was captured (no checklist)")
	ack := fs.Bool("ack-personal", false, "you reviewed the personal-information list and accept it")
	gitInit := fs.Bool("git-init", false, "run `git init` and make one commit in the output directory")
	toReg := fs.Bool("to-registry", false, "publish to the Rigfile registry (private unless --public); needs `rigfile login`")
	public := fs.Bool("public", false, "with --to-registry: make the rig public once the registry scan has published it")
	regFlag := fs.String("registry", "", "registry address (default $RIGFILE_REGISTRY)")
	tarFile := fs.String("write-tarball", "", "write the exact tarball that would be published to this file (to sign it with cosign), then continue")
	signBundle := fs.String("sign-bundle", "", "with --to-registry: a Sigstore bundle for the rig's tarball (sign the file written by --write-tarball with `cosign sign-blob --bundle`); the registry and pullers verify it")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if (*toGit == "" && !*toReg && *tarFile == "") || len(pos) > 1 || (*public && !*toReg) || (*signBundle != "" && !*toReg) {
		fmt.Fprintln(e.err, "usage: rigfile publish [<rig-dir>] [--to-git <dir>] [--to-registry [--public]] [--name owner/name] [--from target] [--all] [--ack-personal] [--git-init]")
		return 2
	}
	var regBase string
	if *toReg {
		b, err := registryBase(e, *regFlag)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		regBase = b
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
	if st.UnsafeBase != nil {
		fmt.Fprintln(e.err, "rigfile: refusing to publish: rigfile/base-secure was skipped on this machine (--i-understand-unsafe-base, since "+st.UnsafeBase.Since+"). Run a normal `rigfile apply` first.")
		return 1
	}
	if ents, err := os.ReadDir(*toGit); *toGit != "" && err == nil && len(ents) > 0 {
		fmt.Fprintf(e.err, "rigfile: %s already has files; choose an empty directory\n", *toGit)
		return 1
	}

	var files map[string][]byte
	switch {
	case len(pos) == 1:
		files, err = publish.FromDir(pos[0])
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
	default:
		if *name == "" {
			fmt.Fprintln(e.err, "rigfile: publishing a capture of this machine needs --name owner/name")
			return 2
		}
		var code int
		if files, code = captureForPublish(e, pi, st, home, *from, *dir, *claudeDir, *name, *all); files == nil {
			return code
		}
	}

	user := e.getenv("USER")
	if user == "" {
		user = e.getenv("USERNAME")
	}
	if user == "" {
		user = filepath.Base(home)
	}
	host, _ := os.Hostname()
	p, err := publish.Prepare(publish.Input{Files: files, Home: home, User: user, Host: host, AckPersonal: *ack})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	printPublishReport(e, p)
	if b := p.Blocked(); len(b) > 0 {
		fmt.Fprintln(e.err, "\nrigfile: not published:")
		for _, x := range b {
			fmt.Fprintln(e.err, "  -", x)
		}
		return 1
	}
	if *tarFile != "" {
		tb, err := p.Tarball()
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		if err := os.WriteFile(*tarFile, tb, 0o644); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		fmt.Fprintf(e.out, "\nwrote %s (%d bytes). Sign it: cosign sign-blob --bundle bundle.json %s\n", *tarFile, len(tb), *tarFile)
	}
	if *toGit != "" {
		if err := p.Write(*toGit); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		fmt.Fprintf(e.out, "\nscan proof: %d finding(s) in %d file(s) written to %s\n", p.Proof.Findings, p.Proof.Files, *toGit)
	} else {
		fmt.Fprintf(e.out, "\nscan proof: %d finding(s) in %d file(s)\n", p.Proof.Findings, p.Proof.Files)
	}
	if *toGit == "" && !*toReg {
		return 0
	}
	if *toReg {
		if code := publishToRegistry(e, p, regBase, *public, *signBundle); code != 0 {
			return code
		}
	}
	if *toGit == "" {
		return 0
	}
	if *gitInit {
		if err := gitInitCommit(*toGit, p.Manifest.Name+"@"+p.Manifest.Version); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		fmt.Fprintln(e.out, "committed; create the remote repository and push it yourself (Rigfile never uploads).")
	} else {
		fmt.Fprintf(e.out, "Next: review the files, then `git init && git add -A && git commit` in %s and push it to a public repository.\n", *toGit)
	}
	fmt.Fprintf(e.out, "Others pull it with: rigfile pull github.com/%s\n", p.Manifest.Name)
	return 0
}

func printPublishReport(e env, p *publish.Prepared) {
	for _, r := range p.Rewritten {
		fmt.Fprintf(e.out, "rewritten   %s: %s\n", r.File, r.Sample)
	}
	for _, f := range p.Personal {
		fmt.Fprintf(e.out, "PERSONAL    %s:%d  %s  %s\n", f.File, f.Line, f.Rule, f.Sample)
	}
	for _, f := range p.Secrets {
		fmt.Fprintf(e.out, "SECRET      %s:%d  %s  (value not shown)\n", f.File, f.Line, f.Rule)
	}
	for _, pr := range p.Problems {
		fmt.Fprintln(e.out, pr)
	}
}

// captureForPublish captures the chosen tool, lets the user untick items in a checklist, and captures again without
// the unticked ones. It returns rig files (nil on failure, with the exit code).
func captureForPublish(e env, pi *platform.Info, st *state.State, home, from, dir, claudeDir, name string, all bool) (map[string][]byte, int) {
	owned := map[string]bool{}
	if ts := st.Targets[from]; ts != nil {
		for _, it := range ts.Items {
			owned[it.Category+"|"+it.Key] = true
			if it.Kind == state.KindMCP {
				owned["mcp|"+it.Key] = true
			}
		}
	}
	deselected := map[string]bool{}
	run := func() (*capture.Result, int) {
		skip := func(cat, key string) bool { return owned[cat+"|"+key] || deselected[cat+"|"+key] }
		if from == "claude-code" {
			cd := claudeDir
			var err error
			if cd == "" {
				if cd, err = claudecode.ClaudeDirFor(pi, e.getenv); err != nil {
					fmt.Fprintln(e.err, "rigfile:", err)
					return nil, 1
				}
			}
			cj := filepath.Join(home, ".claude.json")
			if d := e.getenv("CLAUDE_CONFIG_DIR"); d != "" && claudeDir == "" {
				cj = filepath.Join(d, ".claude.json")
			} else if claudeDir != "" {
				cj = filepath.Join(filepath.Dir(cd), ".claude.json")
			}
			cp, err := claudecode.Capture(claudecode.CaptureOptions{ClaudeDir: cd, ClaudeJSON: cj, Home: home, Name: name, Skip: skip})
			if err != nil {
				fmt.Fprintln(e.err, "rigfile:", err)
				return nil, 1
			}
			res := &capture.Result{Manifest: cp.Manifest, Files: cp.Files}
			for _, f := range cp.Report {
				res.Report = append(res.Report, capture.Finding(f))
			}
			return res, 0
		}
		t, ok := targets.Get(from)
		if !ok || t.Capture == nil {
			fmt.Fprintf(e.err, "rigfile: cannot capture from %q (known: %s)\n", from, strings.Join(targets.Names(), ", "))
			return nil, 2
		}
		res, err := t.Capture(targets.Ctx{Plat: pi, Getenv: e.getenv, Dir: dir}, capture.Options{Home: home, Name: name, Skip: skip})
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return nil, 1
		}
		return res, 0
	}
	res, code := run()
	if res == nil {
		return nil, code
	}
	if !all {
		items, keys := checklistItems(res)
		if len(items) > 0 {
			sel, ok, err := runChecklist(e, items)
			if err != nil {
				fmt.Fprintln(e.err, "rigfile:", err)
				return nil, 1
			}
			if !ok {
				fmt.Fprintln(e.out, "cancelled; nothing was written")
				return nil, 1
			}
			for i, on := range sel {
				if !on {
					deselected[keys[i]] = true
				}
			}
			if len(deselected) > 0 {
				if res, code = run(); res == nil {
					return nil, code
				}
			}
		}
	}
	files := map[string][]byte{"rigfile.yaml": stripCaptureHeader(res.Manifest)}
	for k, v := range res.Files {
		files[k] = v
	}
	return files, 0
}

// checklistItems turns the captured report lines into checkboxes, in a stable order. Items that carry personal
// content or execute code start unticked, with the reason shown.
func checklistItems(res *capture.Result) ([]tui.Item, []string) {
	type row struct {
		it  tui.Item
		key string
	}
	var rows []row
	order := map[string]int{"instruction": 0, "skill": 1, "agent": 2, "command": 3, "mcp": 4, "hook": 5}
	for _, f := range res.Report {
		if f.Level != "captured" {
			continue
		}
		if _, ok := order[f.Category]; !ok {
			continue // permissions and settings are not individually selectable
		}
		it := tui.Item{Group: f.Category, Label: f.Item, Detail: f.Msg, Checked: true}
		switch {
		case f.Category == "instruction":
			it.Checked = false
			it.Warn = "your own text: often holds personal information; tick it only after reading it"
		case f.Category == "hook":
			it.Warn = "executes code on the machine of everyone who pulls this rig"
		case f.Category == "mcp":
			it.Warn = "runs a program on the machine of everyone who pulls this rig"
		}
		rows = append(rows, row{it, f.Category + "|" + f.Item})
	}
	sort.SliceStable(rows, func(i, j int) bool { return order[rows[i].it.Group] < order[rows[j].it.Group] })
	items := make([]tui.Item, len(rows))
	keys := make([]string, len(rows))
	for i, r := range rows {
		items[i], keys[i] = r.it, r.key
	}
	return items, keys
}

// runChecklist shows the checklist on a real terminal (raw mode, redrawn in place) or, when the environment says it
// is interactive without being a terminal (tests), reads key presses from stdin as they are.
func runChecklist(e env, items []tui.Item) ([]bool, bool, error) {
	opts := tui.Options{Title: "Choose what to publish"}
	if f, ok := e.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		old, err := term.MakeRaw(int(f.Fd()))
		if err != nil {
			return nil, false, err
		}
		defer term.Restore(int(f.Fd()), old)
		opts.ANSI = true
		// raw mode does not translate "\n" to "\r\n"
		return tui.Checklist(f, crlf{e.out}, items, opts)
	}
	if !e.interactive {
		return nil, false, fmt.Errorf("the checklist needs a terminal; pass --all to publish everything that was captured")
	}
	return tui.Checklist(e.in, e.out, items, opts)
}

type crlf struct {
	w interface{ Write([]byte) (int, error) }
}

func (c crlf) Write(p []byte) (int, error) {
	if _, err := c.w.Write(bytes.ReplaceAll(p, []byte("\n"), []byte("\r\n"))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func stripCaptureHeader(m []byte) []byte {
	lines := strings.SplitAfter(string(m), "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], "# ") {
		i++
	}
	return []byte(strings.Join(lines[i:], ""))
}

func gitInitCommit(dir, label string) error {
	run := func(args ...string) error {
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %v\n%s", args[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run("init", "-q", "-b", "main"); err != nil {
		return err
	}
	if err := run("add", "-A"); err != nil {
		return err
	}
	return run("commit", "-q", "-m", "Publish "+label) // through the user's own hooks
}

// publishToRegistry uploads the prepared rig and waits for the registry's scan.
func publishToRegistry(e env, p *publish.Prepared, base string, public bool, bundlePath string) int {
	tb, err := p.Tarball()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	owner, name, _ := strings.Cut(p.Manifest.Name, "/")
	c := regClient(e, base)
	if storedToken(e, base) == "" {
		fmt.Fprintln(e.err, "rigfile: not signed in to", base, "- run `rigfile login` first")
		return 1
	}
	var v *regclient.Version
	if bundlePath != "" {
		bundle, rerr := os.ReadFile(bundlePath)
		if rerr != nil {
			fmt.Fprintln(e.err, "rigfile:", rerr)
			return 1
		}
		v, err = c.UploadSigned(context.Background(), owner, name, tb, bundle)
	} else {
		v, err = c.Upload(context.Background(), owner, name, tb)
	}
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if code := pollPublished(e, c, owner, name, v.Version); code != 0 {
		return code
	}
	if !public {
		fmt.Fprintf(e.out, "%s/%s is private: only you can pull it. Make it public with: rigfile publish ... --to-registry --public (or the rig page)\n", owner, name)
		return 0
	}
	if err := c.SetVisibility(context.Background(), owner, name, "public"); err != nil {
		fmt.Fprintln(e.err, "rigfile: published, but it could not be made public:", err)
		return 1
	}
	fmt.Fprintf(e.out, "%s/%s is public. Others pull it with: rigfile pull %s/%s --registry %s\n", owner, name, owner, name, base)
	return 0
}
