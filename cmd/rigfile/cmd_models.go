package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/models"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/session"
)

const modelsUsage = `usage: rigfile models <command>

  list                          the catalog's roles and what would be chosen on this machine
  pull [<rig-dir>] [--name N]   download and set up the rig's local models (verified, pinned)
  status                        the models set up on this machine and whether their servers answer
  serve <name>                  run a model's server in the foreground
  url <name>                    print the model's OpenAI-style base URL (for scripts, Aider, OpenCode, ...)
  run codex|claude [--name N] [-- args...]   start the agent against an Ollama model (Claude Code: experimental)
  rm <name>                     remove a model's service and files
`

// detectHardware reads this machine (or the injected hardware in tests).
func detectHardware(e env) (platform.Hardware, error) {
	if e.hardware != nil {
		return *e.hardware, nil
	}
	pi, err := platformInfo(e)
	if err != nil {
		return platform.Hardware{}, err
	}
	home, err := pi.Home()
	if err != nil {
		return platform.Hardware{}, err
	}
	return platform.DetectHardware(platform.RealProbe(runtime.GOOS, runtime.GOARCH, filepath.Join(home, ".cache"))), nil
}

// modelDeps wires Setup to this machine. Tests adjust it through env.modelDeps.
func modelDeps(e env) (models.Deps, string, error) {
	pi, err := platformInfo(e)
	if err != nil {
		return models.Deps{}, "", err
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		return models.Deps{}, "", err
	}
	home, err := pi.Home()
	if err != nil {
		return models.Deps{}, "", err
	}
	cache := filepath.Join(home, ".cache", "huggingface", "hub")
	if v := e.getenv("HF_HUB_CACHE"); v != "" {
		cache = v
	} else if v := e.getenv("HF_HOME"); v != "" {
		cache = filepath.Join(v, "hub")
	}
	var act = e.brokerAct
	if act == nil {
		act = execActivator{}
	}
	d := models.Deps{
		HF: &models.HF{}, LookPath: e.look, Act: act, GOOS: goosFor(pi), Home: home, UID: strconv.Itoa(os.Getuid()), StateDir: sd,
		CacheRoot: cache, BackupRoot: filepath.Join(sd, "backups"), Out: e.out,
		Run: func(ctx context.Context, argv, env []string, out io.Writer) error {
			c := exec.CommandContext(ctx, argv[0], argv[1:]...)
			c.Env = append(os.Environ(), env...)
			c.Stdout, c.Stderr = out, out
			return c.Run()
		},
		StartTemp: func(ctx context.Context, argv, env []string) (func(), error) {
			c := exec.Command(argv[0], argv[1:]...)
			c.Env = append(os.Environ(), env...)
			if err := c.Start(); err != nil {
				return nil, err
			}
			return func() { _ = c.Process.Kill(); _ = c.Wait() }, nil
		},
	}
	if e.modelDeps != nil {
		d = e.modelDeps(d)
	}
	return d, sd, nil
}

func cmdModels(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.err, modelsUsage)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "list":
		return modelsList(e)
	case "pull":
		return modelsPull(rest, e)
	case "status":
		return modelsStatus(e)
	case "serve":
		return modelsServe(rest, e)
	case "url":
		return modelsURL(rest, e)
	case "run":
		return modelsRun(rest, e)
	case "rm":
		return modelsRm(rest, e)
	}
	fmt.Fprint(e.err, modelsUsage)
	return 2
}

func modelsList(e env) int {
	cat, err := models.LoadCatalog()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	hw, err := detectHardware(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintf(e.out, "This machine: %s/%s, %.0f GB memory, %.0f GB free disk", hw.OS, hw.Arch, hw.MemoryGB, hw.FreeDiskGB)
	for _, g := range hw.GPUs {
		fmt.Fprintf(e.out, ", GPU %s (%.0f GB)", g.Name, g.VRAMGB)
	}
	fmt.Fprintf(e.out, "\nCatalog checked %s\n", cat.Checked)
	names := make([]string, 0, len(cat.Roles))
	for n := range cat.Roles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := cat.Roles[n]
		fmt.Fprintf(e.out, "\n%s: %s\n", n, r.Description)
		models.Resolve(n, manifest.Model{Role: n}, cat, hw).Render(e.out)
	}
	return 0
}

// applyModels is the last step of `apply`: set up the rig's models as the person chose (--models, or asking).
func applyModels(e env, p *session.Prepared, f rigFlags) int {
	var todo []*models.Plan
	var bytes int64
	for _, m := range p.Models {
		if m.Chosen != nil {
			todo = append(todo, m)
			bytes += m.DownloadBytes
		}
	}
	if len(todo) == 0 {
		return 0
	}
	mode := f.models
	if mode == "" {
		if f.yes || !isInteractive(e) {
			mode = "later"
		} else if confirm(e, fmt.Sprintf("\nDownload and set up %d model(s) now (%s)?  [d]ownload  [l]ater ", len(todo), models.HumanBytes(bytes))) {
			mode = "now"
		} else {
			mode = "later"
		}
	}
	switch mode {
	case "now":
		return setupPlans(e, todo)
	case "later", "skip":
		fmt.Fprintln(e.out, "\nLocal models were not downloaded. Run `rigfile models pull` when you want them.")
		return 0
	}
	fmt.Fprintf(e.err, "rigfile: --models wants now, later or skip, got %q\n", mode)
	return 2
}

func setupPlans(e env, todo []*models.Plan) int {
	d, sd, err := modelDeps(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	recs, err := models.Load(sd)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	code := 0
	for _, m := range todo {
		fmt.Fprintf(e.out, "\nSetting up model %s ...\n", m.Name)
		rec, err := models.Setup(context.Background(), m, d)
		if err != nil {
			fmt.Fprintf(e.err, "rigfile: model %s: %v\n", m.Name, err)
			code = 1
			continue
		}
		recs[m.Name] = *rec
		fmt.Fprintf(e.out, "Model %s is ready: %s   (rigfile models url %s)\n", m.Name, rec.API, m.Name)
	}
	if err := recs.Save(sd); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	return code
}

func modelsPull(args []string, e env) int {
	var f rigFlags
	fs := rigFlagSet("models pull", e, &f, true)
	name := fs.String("name", "", "set up only this model")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) > 1 {
		fmt.Fprint(e.err, modelsUsage)
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
	var todo []*models.Plan
	for _, m := range p.Models {
		if *name == "" || m.Name == *name {
			if m.Chosen == nil {
				fmt.Fprintf(e.err, "rigfile: model %s cannot be applied here: %s\n", m.Name, m.Blocked)
				return 1
			}
			todo = append(todo, m)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(e.err, "rigfile: the rig has no such local model")
		return 1
	}
	for _, m := range todo {
		m.Render(e.out)
	}
	if !f.yes && isInteractive(e) && !confirm(e, "\nDownload and set up now?  [d]ownload  [q]uit ") {
		fmt.Fprintln(e.out, "aborted; nothing changed")
		return 1
	}
	return setupPlans(e, todo)
}

func healthURL(r models.Installed) string {
	if r.Engine == "ollama" {
		return "http://" + hostPort(r) + "/api/tags"
	}
	return r.API + "/models"
}

func hostPort(r models.Installed) string {
	h := r.Host
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	return h + ":" + strconv.Itoa(r.Port)
}

// probe checks a server; a variable so tests need no real port.
var probe = answers

func answers(u string) bool {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(u)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func modelsStatus(e env) int {
	_, sd, err := modelDeps(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	recs, err := models.Load(sd)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if len(recs) == 0 {
		fmt.Fprintln(e.out, "no local models are set up (rigfile models pull)")
		return 0
	}
	for _, n := range recs.Names() {
		r := recs[n]
		state := "not answering"
		if probe(healthURL(r)) {
			state = "answering"
		}
		svcs := "started by you (rigfile models serve " + n + ")"
		if r.Service != "" {
			svcs = "service " + r.Service
		}
		fmt.Fprintf(e.out, "%s: %s via %s on %s: %s; %s\n", n, r.Model, r.Engine, hostPort(r), state, svcs)
	}
	return 0
}

func loadRecord(e env, name string) (models.Installed, string, int) {
	_, sd, err := modelDeps(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return models.Installed{}, "", 1
	}
	recs, err := models.Load(sd)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return models.Installed{}, "", 1
	}
	if name == "" && len(recs) == 1 {
		name = recs.Names()[0]
	}
	r, ok := recs[name]
	if !ok {
		fmt.Fprintf(e.err, "rigfile: no local model %q is set up (rigfile models status)\n", name)
		return models.Installed{}, "", 1
	}
	return r, sd, 0
}

func modelsURL(args []string, e env) int {
	name := ""
	if len(args) > 1 {
		fmt.Fprint(e.err, modelsUsage)
		return 2
	}
	if len(args) == 1 {
		name = args[0]
	}
	r, _, code := loadRecord(e, name)
	if code != 0 {
		return code
	}
	fmt.Fprintln(e.out, r.API)
	return 0
}

func modelsServe(args []string, e env) int {
	if len(args) != 1 {
		fmt.Fprint(e.err, modelsUsage)
		return 2
	}
	r, _, code := loadRecord(e, args[0])
	if code != 0 {
		return code
	}
	if r.Exe == "" {
		fmt.Fprintln(e.err, "rigfile: this model has no recorded server command; run `rigfile models pull` again")
		return 1
	}
	if !models.IsLoopbackHost(r.Host) {
		fmt.Fprintln(e.err, "rigfile: refusing to serve on a non-loopback address")
		return 1
	}
	var env []string
	for k, v := range r.Env {
		env = append(env, k+"="+v)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := exec.CommandContext(ctx, r.Exe, r.Args...)
	c.Env = append(os.Environ(), env...)
	c.Stdout, c.Stderr = e.out, e.err
	fmt.Fprintf(e.out, "serving %s on %s (Ctrl-C stops it)\n", r.Model, hostPort(r))
	if err := c.Run(); err != nil && ctx.Err() == nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	return 0
}

func modelsRm(args []string, e env) int {
	if len(args) != 1 {
		fmt.Fprint(e.err, modelsUsage)
		return 2
	}
	r, sd, code := loadRecord(e, args[0])
	if code != 0 {
		return code
	}
	d, _, err := modelDeps(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	for _, n := range models.Remove(context.Background(), r, d) {
		fmt.Fprintln(e.out, n)
	}
	recs, _ := models.Load(sd)
	delete(recs, args[0])
	if err := recs.Save(sd); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintf(e.out, "Removed model %s.\n", args[0])
	return 0
}

// modelsRun starts an agent against an Ollama model, for that process only: no configuration file is written.
func modelsRun(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.err, modelsUsage)
		return 2
	}
	agent, rest := args[0], args[1:]
	var passthrough []string
	for i, a := range rest {
		if a == "--" {
			passthrough, rest = rest[i+1:], rest[:i]
			break
		}
	}
	fs := flagSetFor("models run", e)
	name := fs.String("name", "", "which model (default: the only one)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	r, _, code := loadRecord(e, *name)
	if code != 0 {
		return code
	}
	if r.Engine != "ollama" {
		fmt.Fprintf(e.err, "rigfile: %s is served by %s, which neither Codex nor Claude Code can use without a bridge that was never verified (docs/models.md §4). Only Ollama models can be run through an agent.\n", r.Model, r.Engine)
		return 1
	}
	if !probe("http://" + hostPort(r) + "/api/tags") {
		fmt.Fprintf(e.err, "rigfile: the model server is not answering on %s (start it: rigfile models serve %s, or the service)\n", hostPort(r), *name)
		return 1
	}
	var prog string
	var pargs, env, drop []string
	switch agent {
	case "codex":
		if r.Port != models.OllamaPort {
			fmt.Fprintf(e.err, "rigfile: Codex's --oss mode was documented only for Ollama on its default port %d; this model is on %d, and how Codex would be told otherwise was not verified (UNVERIFIED). Serve it on %d, or use `rigfile models url` with another client.\n", models.OllamaPort, r.Port, models.OllamaPort)
			return 1
		}
		prog = "codex"
		pargs = append([]string{"--oss", "--local-provider", "ollama", "-m", r.Model}, passthrough...)
		fmt.Fprintln(e.err, "note: combining --oss with -m was not verified against Codex's documentation; if Codex ignores the model, pick it in Codex.")
	case "claude":
		prog = "claude"
		pargs = append([]string{"--model", r.Model}, passthrough...)
		env = []string{"ANTHROPIC_BASE_URL=http://" + hostPort(r), "ANTHROPIC_AUTH_TOKEN=ollama"}
		drop = []string{"ANTHROPIC_API_KEY"} // never hand a real Anthropic key to a local server
		fmt.Fprintln(e.err, "EXPERIMENTAL: Anthropic does not support routing Claude Code to non-Claude models. Ollama documents this route; expect gaps (tool_choice, deferred tools, web search).")
	default:
		fmt.Fprint(e.err, modelsUsage)
		return 2
	}
	path, err := e.look(prog)
	if err != nil {
		fmt.Fprintf(e.err, "rigfile: %s was not found on PATH\n", prog)
		return 127
	}
	if e.runAgent != nil {
		return e.runAgent(path, pargs, env, drop)
	}
	c := exec.Command(path, pargs...)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		skip := false
		for _, d := range drop {
			if strings.EqualFold(k, d) {
				skip = true
			}
		}
		if !skip {
			c.Env = append(c.Env, kv)
		}
	}
	c.Env = append(c.Env, env...)
	c.Stdin, c.Stdout, c.Stderr = e.in, e.out, e.err
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	return 0
}

func flagSetFor(name string, e env) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.err)
	return fs
}
