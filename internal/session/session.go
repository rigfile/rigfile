// Package session orchestrates one plan/apply for one rig and one target: load and check the rig,
// resolve its layers, merge, project onto this OS, ask the adapter for a plan, verify the lockfile,
// and (for apply) execute everything through ONE journaled writer, including state.json and
// rigfile.lock, so a single `rollback` undoes the whole run.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	basesecure "github.com/digitaldreamer3462/rigfile/base-secure"
	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/gitmod"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/layers"
	"github.com/digitaldreamer3462/rigfile/internal/lock"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
	"github.com/digitaldreamer3462/rigfile/internal/tools"
)

// Target is the only adapter in Stage 1.
const Target = claudecode.StateTarget

// Options describes one invocation. Zero values mean "use the real machine".
type Options struct {
	RigDir     string
	LayersDir  string // where `from:` layers live: <LayersDir>/<owner>/<name>/rigfile.yaml
	Getenv     func(string) string
	Plat       *platform.Info
	StateDir   string
	ClaudeDir  string
	ProjectDir string
	MCP        claudecode.MCPClient
	Overwrite  bool
	ToolsHost  tools.Host // nil = the real machine

	NoGit      bool   // skip the git module (global gitignore + secret-scanning hooks)
	RigfileBin string // absolute path of the rigfile executable written into the git hooks; "" = this executable
	NoBackstop bool   // do not install the reference-transaction backstop (decision O9: on by default)

	// UnsafeBase skips rigfile/base-secure (--i-understand-unsafe-base): local only, recorded in state.json,
	// shown red by doctor, and never accepted by a future `publish`.
	UnsafeBase bool

	// SandboxOn / SandboxOff set the sticky opt-in for Claude Code's sandbox profile (S2-M5b); neither keeps the
	// choice recorded by the last apply.
	SandboxOn, SandboxOff bool
}

// Prepared is everything computed before anything is written.
type Prepared struct {
	Opts       Options
	Plat       *platform.Info
	StateDir   string
	Top        *manifest.Loaded
	Layers     *layers.Result
	UnsafeBase bool
	Merged     *merge.Merged
	Proj       *merge.Projection
	Problems   []manifest.Problem
	State      *state.State
	Plan       *engine.Plan // nil if the rig has errors
	Tools      tools.Plan   // what apply would install (planned, never run here)
	GitPlan    *engine.Plan // the git module (base-secure); nil with --no-git
	cleanup    func()       // removes the extracted base-secure files
	Sandbox    bool         // effective sandbox opt-in for this run

	Lock      *lock.Lock
	LockPath  string
	LockDiffs []string // differences against an existing lockfile (empty if none or identical)
	HasLock   bool
	MergedSHA string
}

// ErrProblems is returned by Execute when the rig has errors.
var ErrProblems = errors.New("the rig has errors")

// Prepare computes the plan. It reads the machine and the rig; it writes nothing.
func Prepare(o Options) (pp *Prepared, err error) {
	var cleanup func()
	defer func() {
		if err != nil && cleanup != nil {
			cleanup() // an error return leaves no caller to Close(): do not leak the extracted files
		}
	}()
	return prepare(o, &cleanup)
}

func prepare(o Options, cleanupOut *func()) (*Prepared, error) {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	pi := o.Plat
	if pi == nil {
		var err error
		if pi, err = platform.New(platform.Options{Getenv: o.Getenv}); err != nil {
			return nil, err
		}
	}
	sd := o.StateDir
	if sd == "" {
		var err error
		if sd, err = pi.StateDir(); err != nil {
			return nil, err
		}
	}
	claudeDir := o.ClaudeDir
	if claudeDir == "" {
		var err error
		if claudeDir, err = claudecode.ClaudeDirFor(pi, o.Getenv); err != nil {
			return nil, err
		}
	}
	p := &Prepared{Opts: o, Plat: pi, StateDir: sd, UnsafeBase: o.UnsafeBase}
	if st0, err := state.Load(sd); err == nil {
		p.Sandbox = st0.Prefs.Sandbox
	}
	if o.SandboxOn {
		p.Sandbox = true
	}
	if o.SandboxOff {
		p.Sandbox = false
	}

	top, err := manifest.Load(o.RigDir)
	if err != nil {
		return nil, err
	}
	p.Top = top
	if strings.HasPrefix(top.M.Name, "rigfile/") {
		return nil, fmt.Errorf("the name %q is reserved for rigfiles published by the Rigfile project (plan §10.3); rename the rig", top.M.Name)
	}
	var base *manifest.Loaded
	if !o.UnsafeBase {
		bdir, cleanup, err := basesecure.Extract()
		if err != nil {
			return nil, err
		}
		p.cleanup = cleanup
		*cleanupOut = cleanup
		if base, err = manifest.Load(bdir); err != nil {
			return nil, fmt.Errorf("the embedded %s layer failed to load: %w", basesecure.Name, err)
		}
	}
	res, err := layers.Resolve(top, layers.WithBase(base, layers.DirSource{Root: o.LayersDir}))
	if err != nil {
		return nil, err
	}
	p.Layers = res
	if o.UnsafeBase {
		res.Warnings = append(res.Warnings, "rigfile/base-secure is DISABLED (--i-understand-unsafe-base): no secret protections, denies or guard hooks from the base layer")
	}
	for _, l := range res.Layers {
		for _, pr := range manifest.Check(res.Loaded[l.Name]) {
			if l.Name != top.M.Name {
				pr.Where = l.Name + ": " + pr.Where
			}
			p.Problems = append(p.Problems, pr)
		}
	}
	sort.SliceStable(p.Problems, func(i, j int) bool {
		return p.Problems[i].Level == manifest.Error && p.Problems[j].Level != manifest.Error
	})

	m, err := merge.Merge(res.Layers, Target)
	if err != nil {
		return nil, err
	}
	p.Merged = m
	if p.MergedSHA, err = m.Hash(); err != nil {
		return nil, err
	}
	p.Proj = m.Project(string(pi.OS), Target)

	if p.State, err = state.Load(sd); err != nil {
		return nil, err
	}
	if manifest.HasErrors(p.Problems) {
		return p, nil // no lock, no plan: the caller prints the problems (hashing would trip over the same missing files)
	}
	fresh, err := lock.Build(res, map[string]string{Target: p.MergedSHA})
	if err != nil {
		return nil, err
	}
	p.Lock = fresh
	p.LockPath = filepath.Join(top.Dir, lock.FileName)
	if b, err := os.ReadFile(p.LockPath); err == nil {
		existing, err := lock.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.LockPath, err)
		}
		p.HasLock = true
		p.LockDiffs = lock.Verify(existing, fresh)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	env := claudecode.Env{
		Plat: pi, ClaudeDir: claudeDir, ProjectDir: o.ProjectDir, State: p.State.Targets[Target],
		MCP: o.MCP, Overwrite: o.Overwrite, BaseSecure: !o.UnsafeBase, Sandbox: p.Sandbox,
	}
	if p.Plan, err = claudecode.Build(env, p.Proj); err != nil {
		return nil, err
	}
	if !o.NoGit {
		bin := o.RigfileBin
		if bin == "" {
			if bin, err = os.Executable(); err == nil {
				if r, rerr := filepath.EvalSymlinks(bin); rerr == nil {
					bin = r
				}
			}
			if err != nil {
				return nil, err
			}
		}
		if p.GitPlan, err = gitmod.Build(gitmod.Options{Plat: pi, Getenv: o.Getenv, Rigfile: bin, Backstop: !o.NoBackstop,
			State: p.State.Targets[gitmod.Target], Overwrite: o.Overwrite}); err != nil {
			return nil, err
		}
	}
	cat, err := tools.LoadCatalog()
	if err != nil {
		return nil, err
	}
	th := o.ToolsHost
	if th == nil {
		th = tools.SystemHost{}
	}
	in := tools.Input{}
	for k, v := range p.Merged.Tools {
		for _, e := range v {
			in[k] = append(in[k], e.V)
		}
	}
	p.Tools = tools.Build(cat, in, string(pi.OS), th)
	p.Plan.Replaced = p.Merged.Replaced
	for _, w := range append(append([]string(nil), res.Warnings...), m.Warnings...) {
		p.Plan.Notes = append(p.Plan.Notes, w)
	}
	return p, nil
}

// Close removes the temporary files of this run (the extracted base-secure layer). Safe to call twice.
func (p *Prepared) Close() {
	if p != nil && p.cleanup != nil {
		p.cleanup()
		p.cleanup = nil
	}
}

// Changes counts every actionable change, including the git module's.
func (p *Prepared) Changes() int {
	n := p.Plan.Changes()
	if p.GitPlan != nil {
		n += p.GitPlan.Changes()
	}
	return n
}

// Refused counts items left untouched because something else was in the way.
func (p *Prepared) Refused() int {
	n := len(p.Plan.Conflicts())
	if p.GitPlan != nil {
		n += len(p.GitPlan.Conflicts())
	}
	return n
}

// Needs lists the secrets and logins the rig requires, from the merged model (names and descriptions
// only). A secret is needed if it is declared or referenced by an installed MCP server.
func (p *Prepared) Needs() []state.Need {
	var out []state.Need
	seen := map[string]bool{}
	add := func(n state.Need) {
		k := n.Kind + "|" + n.Ref
		if !seen[k] {
			seen[k] = true
			out = append(out, n)
		}
	}
	for _, s := range p.Proj.MCPServers {
		for _, v := range s.P.V.Env {
			if ref, ok := manifest.SecretRef(v); ok {
				d := p.Merged.Secrets[ref].V
				add(state.Need{Kind: "secret", Ref: ref, Description: d.Description, ObtainURL: d.ObtainURL})
			}
		}
	}
	keys := make([]string, 0, len(p.Merged.Secrets))
	for k := range p.Merged.Secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := p.Merged.Secrets[k].V
		if !d.Optional {
			add(state.Need{Kind: "secret", Ref: k, Description: d.Description, ObtainURL: d.ObtainURL})
		}
	}
	for _, l := range p.Proj.Logins {
		add(state.Need{Kind: "login", Ref: l.V.Provider, Description: l.V.Reason, Method: l.V.Method})
	}
	return out
}

// Header is the first lines of the review screen.
func (p *Prepared) Header() string {
	var chain []string
	for _, l := range p.Layers.Layers {
		chain = append(chain, l.Name)
	}
	return fmt.Sprintf("Rig: %s@%s   (layers: %s)\nTarget: claude-code on %s", p.Top.M.Name, p.Top.M.Version, strings.Join(chain, " → "), p.Plat.OS)
}

// ExecOptions tune Execute.
type ExecOptions struct {
	UpdateLock bool // accept a changed rig: rewrite rigfile.lock instead of refusing
	Note       string
	Now        func() time.Time
}

// Result is what Execute did.
type Result struct {
	RunID       string
	LockWritten bool
	Applied     int
	Refused     int
}

// Execute applies the plan. It refuses if the rig has errors or the lockfile no longer matches (unless
// UpdateLock). All writes, including rigfile.lock and state.json, go through one journaled writer.
func (p *Prepared) Execute(x ExecOptions) (*Result, error) {
	if p.Plan == nil || manifest.HasErrors(p.Problems) {
		return nil, ErrProblems
	}
	if p.HasLock && len(p.LockDiffs) > 0 && !x.UpdateLock {
		return nil, lock.Mismatch(p.LockDiffs)
	}
	now := x.Now
	if now == nil {
		now = time.Now
	}
	w := &apply.Writer{BackupRoot: filepath.Join(p.StateDir, "backups"), Now: now}
	runID, err := w.RunID()
	if err != nil {
		return nil, err
	}
	res := &Result{Applied: p.Changes(), Refused: p.Refused()}

	// Fail BEFORE touching the machine if rigfile.lock cannot be written next to the rig.
	needLock := !p.HasLock || len(p.LockDiffs) > 0
	if needLock {
		if f, err := os.CreateTemp(filepath.Dir(p.LockPath), ".rigfile-probe-*"); err != nil {
			return nil, fmt.Errorf("cannot write %s: %w\n(the rig directory must be writable so the lockfile can live next to it)", p.LockPath, err)
		} else {
			f.Close()
			_ = os.Remove(f.Name())
		}
	}

	ts := p.State.Target(Target)
	if err := p.Plan.Apply(&engine.Exec{W: w}, ts); err != nil {
		// whatever already happened is journaled: commit it so `rollback` can undo the partial run
		_, _ = w.Commit("FAILED: " + x.Note)
		return nil, err
	}
	// From here on the machine has changed: any failure must still commit the journal so `rollback` works.
	fail := func(err error) (*Result, error) {
		_, _ = w.Commit("FAILED: " + x.Note)
		return nil, err
	}
	if p.GitPlan != nil {
		gts := p.State.Target(gitmod.Target)
		if err := p.GitPlan.Apply(&engine.Exec{W: w}, gts); err != nil {
			_, _ = w.Commit("FAILED: " + x.Note)
			return nil, err
		}
		if p.GitPlan.Changes() > 0 || gts.RunID == "" {
			gts.AppliedAt = now().UTC().Format(time.RFC3339)
			gts.RunID = runID
		}
	}
	lockBytes, err := p.Lock.Marshal()
	if err != nil {
		return fail(err)
	}
	if needLock {
		lr, err := w.WriteFileMode(p.LockPath, lockBytes, 0o644)
		if err != nil {
			return fail(err)
		}
		res.LockWritten = lr.Changed
	}
	ts.Rig = state.RigRef{Name: p.Top.M.Name, Version: p.Top.M.Version, Hash: p.MergedSHA}
	ts.LockSHA = hashing.Bytes(lockBytes)
	ts.Needs = p.Needs()
	// A run that changed nothing must not rewrite state.json just to bump a timestamp.
	if res.Applied > 0 || res.LockWritten || ts.RunID == "" {
		ts.AppliedAt = now().UTC().Format(time.RFC3339)
		ts.RunID = runID
	}
	p.State.Prefs.Sandbox = p.Sandbox
	switch {
	case p.UnsafeBase && p.State.UnsafeBase == nil:
		p.State.UnsafeBase = &state.UnsafeBase{Since: now().UTC().Format(time.RFC3339)}
	case !p.UnsafeBase:
		p.State.UnsafeBase = nil
	}
	sb, err := p.State.Marshal()
	if err != nil {
		return fail(err)
	}
	if _, err := w.WriteFileMode(filepath.Join(p.StateDir, state.FileName), sb, 0o600); err != nil {
		return fail(err)
	}
	res.RunID, err = w.Commit(x.Note)
	return res, err
}
