package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/analyze"
	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/gitmod"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/session"
	"github.com/digitaldreamer3462/rigfile/internal/state"
	"github.com/digitaldreamer3462/rigfile/internal/targets"
	"github.com/digitaldreamer3462/rigfile/internal/tools"
)

// ---- validate ---------------------------------------------------------------------------------

func cmdValidate(args []string, e env) int {
	if len(args) != 1 {
		fmt.Fprintln(e.err, "usage: rigfile validate <rig-dir|rigfile.yaml>")
		return 2
	}
	st, err := os.Stat(args[0])
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if !st.IsDir() { // a single manifest file: schema only (its referenced files are relative to a rig dir)
		data, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		if _, err := manifest.Parse(data); err != nil {
			fmt.Fprintln(e.err, err)
			return 1
		}
		fmt.Fprintf(e.out, "%s: valid (schema only)\n", args[0])
		return 0
	}
	l, err := manifest.Load(args[0])
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	ps := manifest.Check(l)
	for _, p := range ps {
		fmt.Fprintln(e.err, p)
	}
	if manifest.HasErrors(ps) {
		return 1
	}
	fmt.Fprintf(e.out, "%s: valid (%d warning(s))\n", args[0], len(ps))
	return 0
}

// ---- plan / apply -----------------------------------------------------------------------------

type rigFlags struct {
	layers, project, claudeDir, registry, models                               string
	overwrite, yes, updateLock, noTools, noGit, unsafeBase, sandbox, noSandbox bool
	acceptDanger                                                               bool
	targets                                                                    kvFlags
	pulled                                                                     *pulledRig // set by `pull` and `update`
}

// pulledRig describes a rig that was fetched from a git source: recorded in state.json and shown on the screen.
type pulledRig struct {
	Source, Commit, Tree, Signer string
	Banner                       []string
}

func rigFlagSet(name string, e env, f *rigFlags, withApply bool) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.err)
	fs.StringVar(&f.layers, "layers", "", "directory holding inherited layers: <dir>/<owner>/<name>/rigfile.yaml")
	fs.StringVar(&f.project, "project", "", "project directory for scope: project instructions")
	fs.StringVar(&f.registry, "registry", "", "Rigfile registry to resolve owner/name layers from (default $RIGFILE_REGISTRY)")
	fs.StringVar(&f.claudeDir, "claude-dir", "", "Claude Code config directory (default ~/.claude or $CLAUDE_CONFIG_DIR)")
	fs.BoolVar(&f.unsafeBase, "i-understand-unsafe-base", false, "DANGEROUS: skip rigfile/base-secure (local only; recorded in state; doctor shows it red)")
	fs.BoolVar(&f.sandbox, "sandbox", false, "also turn on Claude Code's OS-level sandbox with base-secure's credential denies (remembered; macOS/Linux/WSL2)")
	fs.BoolVar(&f.noSandbox, "no-sandbox", false, "stop managing the sandbox profile (settings already added stay until you delete them)")
	fs.Var(&f.targets, "target", "also configure this target even if it is not detected (repeatable): "+strings.Join(targets.Names(), ", "))
	fs.BoolVar(&f.noGit, "no-git", false, "skip the git protections (global gitignore and secret-scanning hooks)")
	fs.BoolVar(&f.overwrite, "overwrite", false, "replace hand-edited managed content and items Rigfile does not own")
	fs.StringVar(&f.models, "models", "", "the rig's local models: now (download and set up), later (default with --yes), or skip (also skips the engine install)")
	if withApply {
		fs.BoolVar(&f.yes, "yes", false, "apply without asking")
		fs.BoolVar(&f.acceptDanger, "accept-danger", false, "with --yes: apply even though static analysis found danger-level patterns in a rig you pulled")
		fs.BoolVar(&f.noTools, "no-tools", false, "do not install tools; only show what would be installed")
		fs.BoolVar(&f.updateLock, "update-lock", false, "accept a changed rig: rewrite rigfile.lock")
	}
	return fs
}

func prepare(e env, rigDir string, f rigFlags) (*session.Prepared, int) {
	p, err := session.Prepare(session.Options{
		RigDir: rigDir, LayersDir: f.layers, Getenv: e.getenv, StateDir: e.stateDir,
		ClaudeDir: f.claudeDir, ProjectDir: f.project, MCP: mcpClient(e), Sources: e.sources, Registry: registryBaseQuiet(e, f.registry), RegistryToken: regToken(e), Source: f.pulled.source(), Commit: f.pulled.commit(), Tree: f.pulled.tree(), Signer: f.pulled.signer(), Overwrite: f.overwrite, ToolsHost: e.tools, Hardware: e.hardware, SkipModels: f.models == "skip", NoGit: f.noGit, UnsafeBase: f.unsafeBase, SandboxOn: f.sandbox, SandboxOff: f.noSandbox, Targets: []string(f.targets), Have: func(c string) bool { _, err := e.look(c); return err == nil },
	})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return nil, 1
	}
	return p, 0
}

func cmdPlanApply(verb string, args []string, e env) int {
	var f rigFlags
	fs := rigFlagSet(verb, e, &f, verb == "apply")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 1 {
		fmt.Fprintf(e.err, "usage: rigfile %s [<rig-dir>] [flags]\n", verb)
		return 2
	}
	rigDir := "."
	if len(pos) == 1 {
		rigDir = pos[0]
	}
	return planApply(verb, rigDir, f, e)
}

func (p *pulledRig) source() string {
	if p == nil {
		return ""
	}
	return p.Source
}

func (p *pulledRig) commit() string {
	if p == nil {
		return ""
	}
	return p.Commit
}

func (p *pulledRig) signer() string {
	if p == nil {
		return ""
	}
	return p.Signer
}

func (p *pulledRig) tree() string {
	if p == nil {
		return ""
	}
	return p.Tree
}

// planApply is the review-and-apply flow shared by plan, apply, pull and update.
func planApply(verb, rigDir string, f rigFlags, e env) int {
	p, code := prepare(e, rigDir, f)
	if p == nil {
		return code
	}
	defer p.Close()

	if f.pulled != nil {
		for _, l := range f.pulled.Banner {
			fmt.Fprintln(e.out, l)
		}
		fmt.Fprintln(e.out)
	}
	fmt.Fprintln(e.out, p.Header())
	fmt.Fprintln(e.out)
	if f.unsafeBase {
		fmt.Fprintln(e.out, "!!! --i-understand-unsafe-base: rigfile/base-secure is SKIPPED. Nothing protects secrets from the agent or from git")
		fmt.Fprintln(e.out, "!!! beyond what you already have. This is recorded in state.json and shown as a red item by `rigfile doctor`.")
		fmt.Fprintln(e.out)
	}
	for _, pr := range p.Problems {
		fmt.Fprintln(e.out, pr)
	}
	if manifest.HasErrors(p.Problems) {
		fmt.Fprintln(e.err, "\nrigfile: the rig has errors; fix them first")
		return 1
	}
	if len(p.Problems) > 0 {
		fmt.Fprintln(e.out)
	}
	for i, tp := range p.Targets {
		if i > 0 {
			fmt.Fprintln(e.out)
		}
		tp.Plan.Render(e.out)
	}
	for _, sk := range p.Skipped {
		fmt.Fprintf(e.out, "\nNOT CONFIGURED  %s\n", sk)
	}
	if p.GitPlan != nil && (len(p.GitPlan.Ops) > 0 || len(p.GitPlan.Notes) > 0) {
		fmt.Fprintln(e.out)
		p.GitPlan.Render(e.out)
	}
	dangers := 0
	if f.pulled != nil {
		dangers = printAnalysis(e, p)
	}
	printTools(e, p.Tools)
	printModels(e, p)
	printNeeds(e, p)
	if p.HasLock && len(p.LockDiffs) > 0 {
		fmt.Fprintf(e.out, "\nrigfile.lock does not match the rig:\n%s\n", indent(strings.Join(p.LockDiffs, "\n"), "  "))
	} else if !p.HasLock {
		fmt.Fprintln(e.out, "\nNo rigfile.lock yet; apply will create one next to the rig.")
	}
	if verb == "plan" {
		return 0
	}

	if p.HasLock && len(p.LockDiffs) > 0 && !f.updateLock {
		fmt.Fprintln(e.err, "\nrigfile: refusing to apply a rig that no longer matches its lockfile. Review the differences above, then re-run with --update-lock to accept them.")
		return 1
	}
	if dangers > 0 && f.yes && !f.acceptDanger {
		fmt.Fprintf(e.err, "\nrigfile: refusing to apply without asking: static analysis found %d danger-level pattern(s) in code you pulled (listed above). Read them, then run again without --yes, or add --accept-danger if you have reviewed them.\n", dangers)
		return 1
	}
	runTools := p.Tools.Runnable()
	if f.noTools {
		runTools = nil
	}
	if (p.Changes() > 0 || len(runTools) > 0) && !f.yes {
		if !confirm(e, fmt.Sprintf("\nApply %d change(s)%s?  [a]pply  [q]uit ", p.Changes(), plural(len(runTools), " and install 1 tool", fmt.Sprintf(" and install %d tools", len(runTools))))) {
			fmt.Fprintln(e.out, "aborted; nothing changed")
			return 1
		}
	}
	exit := 0
	if len(runTools) > 0 {
		fmt.Fprintln(e.out)
		th := e.tools
		if th == nil {
			th = tools.SystemHost{}
		}
		tp := p.Tools
		r := tools.Execute(context.Background(), tp, th, e.out)
		for _, msg := range r.Failed {
			fmt.Fprintln(e.err, "rigfile: tool install failed:", msg)
			exit = 1
		}
		if len(r.Failed) > 0 {
			fmt.Fprintln(e.out, "continuing with the configuration; re-run `rigfile apply` after fixing the tool problem")
		}
	}
	res, err := p.Execute(session.ExecOptions{UpdateLock: f.updateLock, Note: "apply " + p.Top.M.Name + "@" + p.Top.M.Version})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	switch {
	case res.RunID != "":
		fmt.Fprintf(e.out, "\napplied %d change(s). Run %s   (undo: rigfile rollback %s)\n", res.Applied, res.RunID, res.RunID)
	default:
		fmt.Fprintln(e.out, "\nnothing to change; the machine already matches the rig")
	}
	if res.LockWritten {
		fmt.Fprintf(e.out, "wrote %s\n", p.LockPath)
	}
	if res.Refused > 0 {
		fmt.Fprintf(e.out, "%d item(s) were refused and left untouched (see ! lines above)\n", res.Refused)
		return 3
	}
	if len(runTools) > 0 {
		fmt.Fprintln(e.out, "note: `rigfile rollback` does not uninstall tools")
	}
	if !f.yes {
		promptMissingSecrets(e, p)
	}
	if code := applyModels(e, p, f); code != 0 && exit == 0 {
		exit = code
	}
	return exit
}

// printTools shows the TOOLS section of the review screen.
func printTools(e env, tp tools.Plan) {
	if tp.Empty() && len(tp.Present) == 0 {
		return
	}
	fmt.Fprintln(e.out, "\nTOOLS")
	for _, s := range tp.Steps {
		if s.Manual {
			fmt.Fprintf(e.out, "  !  %s   (run it yourself: %s)\n", s.Command(), s.Why)
		} else {
			fmt.Fprintf(e.out, "  +  %s   (runs after you approve)\n", s.Command())
		}
		if s.Warn != "" {
			fmt.Fprintf(e.out, "       ⚠ %s\n", s.Warn)
		}
	}
	for _, n := range tp.Present {
		fmt.Fprintf(e.out, "  =  %s   already installed\n", n)
	}
	for _, u := range tp.Unresolved {
		fmt.Fprintf(e.out, "  ?  %s\n", u)
	}
}

// readLine reads up to a newline one byte at a time, so no input beyond the line is consumed (several
// prompts can share one stdin).
func readLine(r io.Reader) string {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				break
			}
			sb.WriteByte(buf[0])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}

func confirm(e env, prompt string) bool {
	fmt.Fprint(e.out, prompt)
	line := readLine(e.in)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "a", "y", "yes", "apply":
		return true
	}
	return false
}

// printNeeds lists secrets and logins the rig needs, with set/not-set status when it can be read
// without prompting (OS keychain only).
func printNeeds(e env, p *session.Prepared) {
	needs := p.Needs()
	if len(needs) == 0 {
		return
	}
	status := secretStatuses(e, p.Plat, secretRefs(needs))
	fmt.Fprintln(e.out)
	loginsShown := false
	defer func() {
		if loginsShown {
			fmt.Fprintln(e.out, "               `rigfile logins` walks through all of them, one after another")
		}
	}()
	for _, n := range needs {
		switch n.Kind {
		case "secret":
			line := fmt.Sprintf("SECRET NEEDED  %s", n.Ref)
			if st, ok := status[n.Ref]; ok {
				line += "   [" + st + "]"
			}
			if n.Description != "" {
				line += "   " + n.Description
			}
			fmt.Fprintln(e.out, line)
			if n.ObtainURL != "" {
				fmt.Fprintf(e.out, "               get it: %s\n", n.ObtainURL)
			}
			if status[n.Ref] == "not set" {
				fmt.Fprintf(e.out, "               then run: rigfile secrets set %s\n", n.Ref)
			}
		case "login":
			loginsShown = true
			how := n.Method
			if how == "" {
				how = "sign in"
			}
			fmt.Fprintf(e.out, "LOGIN NEEDED   %s (%s)   %s\n", n.Ref, how, n.Description)
			if h, ok := loginHints[n.Ref]; ok {
				fmt.Fprintf(e.out, "               %s\n", h)
			}
		}
	}
}

// ---- diff -------------------------------------------------------------------------------------

func cmdDiff(args []string, e env) int {
	if len(args) != 0 {
		fmt.Fprintln(e.err, "usage: rigfile diff")
		return 2
	}
	pi, err := platformInfo(e)
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
	gts := st.Targets[gitmod.Target]
	applied := appliedTargets(st)
	if len(applied) == 0 && (gts == nil || len(gts.Items) == 0) {
		fmt.Fprintln(e.out, "nothing has been applied yet")
		return 0
	}
	var ds []state.Drift
	for _, at := range applied {
		fmt.Fprintf(e.out, "%-13s %s@%s   applied %s   run %s\n", at.T.Title, at.TS.Rig.Name, at.TS.Rig.Version, at.TS.AppliedAt, at.TS.RunID)
		probes := map[string]state.Probe{}
		if at.T.Name == "claude-code" {
			probes[state.KindMCP] = claudecode.MCPProbe(mcpClient(e))
		}
		ds = append(ds, state.Check(at.TS.Items, probes)...)
	}
	if gts != nil && len(gts.Items) > 0 {
		fmt.Fprintf(e.out, "Git protections (base-secure)   applied %s   run %s\n", gts.AppliedAt, gts.RunID)
		ds = append(ds, state.Check(gts.Items, nil)...)
	}
	bad := 0
	for _, d := range ds {
		mark := "✔"
		if d.Status != state.OK {
			mark, bad = "✘", bad+1
		}
		line := fmt.Sprintf("  %s %-11s %s", mark, d.Item.Category, describeItem(d.Item))
		if d.Status != state.OK {
			line += fmt.Sprintf("   %s", d.Status)
			if d.Detail != "" {
				line += ": " + d.Detail
			}
		}
		fmt.Fprintln(e.out, line)
	}
	if bad == 0 {
		fmt.Fprintln(e.out, "no drift")
		return 0
	}
	fmt.Fprintf(e.out, "%d of %d item(s) differ from what Rigfile applied\n", bad, len(ds))
	return 1
}

func describeItem(it state.Item) string {
	switch it.Kind {
	case state.KindJSONList:
		return it.Detail["list"] + " " + it.Detail["value"]
	case state.KindRegion:
		return "section " + it.Detail["region"] + " in " + filepath.Base(it.Path)
	case state.KindJSONRaw:
		return it.Key + " [" + it.Detail["list"] + "]"
	case state.KindMCP:
		return it.Key
	}
	return it.Key + "   " + it.Path
}

// ---- rollback ---------------------------------------------------------------------------------

func cmdRollback(args []string, e env) int {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.SetOutput(e.err)
	list := fs.Bool("list", false, "list runs")
	force := fs.Bool("force", false, "overwrite files edited after Rigfile wrote them")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) > 1 {
		return 2
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	root := filepath.Join(sd, "backups")
	runs, err := apply.ListRuns(root)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if *list {
		if len(runs) == 0 {
			fmt.Fprintln(e.out, "no runs recorded")
		}
		for _, r := range runs {
			fmt.Fprintf(e.out, "%s   %d file(s)   %s\n", r.ID, len(r.Files), r.Note)
		}
		return 0
	}
	if len(runs) == 0 {
		fmt.Fprintln(e.err, "rigfile: no runs to roll back")
		return 1
	}
	id := runs[0].ID
	if len(pos) == 1 {
		id = pos[0]
	} else {
		fmt.Fprintf(e.out, "rolling back the newest run: %s\n", id)
	}
	out, err := apply.Rollback(root, id, *force)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	skipped := 0
	for _, o := range out {
		line := fmt.Sprintf("  %-8s %s", o.Action, o.Path)
		if o.Reason != "" {
			line += "   (" + o.Reason + ")"
		}
		fmt.Fprintln(e.out, line)
		if o.Action == "skipped" {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Fprintf(e.out, "%d file(s) skipped because they changed after Rigfile wrote them; --force overwrites them\n", skipped)
		return 3
	}
	fmt.Fprintln(e.out, "rolled back")
	return 0
}

// ---- lock -------------------------------------------------------------------------------------

func cmdLock(args []string, e env) int {
	var f rigFlags
	fs := rigFlagSet("lock", e, &f, false)
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) > 1 {
		return 2
	}
	rigDir := "."
	if len(pos) == 1 {
		rigDir = pos[0]
	}
	p, code := prepare(e, rigDir, f)
	if p == nil {
		return code
	}
	defer p.Close()
	if manifest.HasErrors(p.Problems) {
		for _, pr := range p.Problems {
			fmt.Fprintln(e.err, pr)
		}
		return 1
	}
	b, err := p.Lock.Marshal()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	w := &apply.Writer{BackupRoot: filepath.Join(p.StateDir, "backups")}
	res, err := w.WriteFileMode(p.LockPath, b, 0o644)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if _, err := w.Commit("lock " + p.Top.M.Name); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if !res.Changed {
		fmt.Fprintf(e.out, "%s is already up to date\n", p.LockPath)
		return 0
	}
	fmt.Fprintf(e.out, "wrote %s (%d layer(s))\n", p.LockPath, len(p.Lock.Layers))
	return 0
}

// loginHints say how to sign in with the vendor's own tool. Rigfile never handles these credentials.
var loginHints = map[string]string{
	"claude-code": "run `claude`, then use /login (or set up an API key / gateway)",
	"github":      "run `gh auth login`",
}

type appliedTarget struct {
	T  targets.Target
	TS *state.TargetState
}

// appliedTargets lists the registered targets that have something applied, in registry order.
func appliedTargets(st *state.State) []appliedTarget {
	var out []appliedTarget
	for _, t := range targets.All() {
		if ts := st.Targets[t.Name]; ts != nil && len(ts.Items) > 0 {
			out = append(out, appliedTarget{t, ts})
		}
	}
	return out
}

// primaryApplied is the first applied target (Claude Code when present): it carries the rig identity and
// the secrets/logins the rig needs.
func primaryApplied(st *state.State) *state.TargetState {
	if a := appliedTargets(st); len(a) > 0 {
		return a[0].TS
	}
	return nil
}

// printAnalysis runs static analysis over a pulled rig and the layers it inherited from remote sources, and prints the
// findings (docs/trust.md §2). It returns the number of danger-level findings. Findings never quote the analysed text.
func printAnalysis(e env, p *session.Prepared) int {
	type unit struct{ name, dir string }
	units := []unit{{p.Top.M.Name, p.Top.Dir}}
	for name := range p.Layers.Remotes {
		if l := p.Layers.Loaded[name]; l != nil {
			units = append(units, unit{name, l.Dir})
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].name < units[j].name })
	total, dangers := 0, 0
	var lines []string
	notices := 0
	for _, u := range units {
		rep, err := analyze.Dir(u.dir)
		if err != nil {
			continue
		}
		for _, f := range rep.Findings {
			total++
			switch f.Level {
			case analyze.Danger:
				dangers++
			case analyze.Notice:
				notices++
				continue
			}
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			tag := strings.ToUpper(string(f.Level))
			lines = append(lines, fmt.Sprintf("  %-8s %s: %s   [%s]", tag, loc, f.Message, f.Rule))
		}
	}
	if total == 0 {
		fmt.Fprintln(e.out, "\nANALYSIS  no suspicious patterns found in the rig's scripts, hooks or instructions (static analysis is a heuristic, not a guarantee)")
		return 0
	}
	fmt.Fprintf(e.out, "\nANALYSIS  %d danger, %d caution, %d notice (heuristics: they miss things and can be wrong)\n", dangers, len(lines)-dangers, notices)
	for _, l := range lines {
		fmt.Fprintln(e.out, l)
	}
	return dangers
}

// printModels shows the MODELS section: what would be downloaded and run, and what is deliberately not wired.
func printModels(e env, p *session.Prepared) {
	if len(p.Models) == 0 && len(p.ModelNotes) == 0 {
		return
	}
	fmt.Fprintln(e.out, "\nMODELS   (nothing is downloaded until you approve; `--models now|later|skip`)")
	for _, m := range p.Models {
		m.Render(e.out)
	}
	for _, n := range p.ModelNotes {
		fmt.Fprintf(e.out, "  ·  %s\n", n)
	}
}
