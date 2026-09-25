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

	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/layers"
	"github.com/digitaldreamer3462/rigfile/internal/lock"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
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
}

// Prepared is everything computed before anything is written.
type Prepared struct {
	Opts     Options
	Plat     *platform.Info
	StateDir string
	Top      *manifest.Loaded
	Layers   *layers.Result
	Merged   *merge.Merged
	Proj     *merge.Projection
	Problems []manifest.Problem
	State    *state.State
	Plan     *engine.Plan // nil if the rig has errors

	Lock      *lock.Lock
	LockPath  string
	LockDiffs []string // differences against an existing lockfile (empty if none or identical)
	HasLock   bool
	MergedSHA string
}

// ErrProblems is returned by Execute when the rig has errors.
var ErrProblems = errors.New("the rig has errors")

// Prepare computes the plan. It reads the machine and the rig; it writes nothing.
func Prepare(o Options) (*Prepared, error) {
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
	p := &Prepared{Opts: o, Plat: pi, StateDir: sd}

	top, err := manifest.Load(o.RigDir)
	if err != nil {
		return nil, err
	}
	p.Top = top
	res, err := layers.Resolve(top, layers.DirSource{Root: o.LayersDir})
	if err != nil {
		return nil, err
	}
	p.Layers = res
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
		MCP: o.MCP, Overwrite: o.Overwrite,
	}
	if p.Plan, err = claudecode.Build(env, p.Proj); err != nil {
		return nil, err
	}
	p.Plan.Replaced = p.Merged.Replaced
	for _, w := range append(append([]string(nil), res.Warnings...), m.Warnings...) {
		p.Plan.Notes = append(p.Plan.Notes, w)
	}
	return p, nil
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
	res := &Result{Applied: p.Plan.Changes(), Refused: len(p.Plan.Conflicts())}

	ts := p.State.Target(Target)
	if err := p.Plan.Apply(&engine.Exec{W: w}, ts); err != nil {
		// whatever already happened is journaled: commit it so `rollback` can undo the partial run
		_, _ = w.Commit("FAILED: " + x.Note)
		return nil, err
	}
	lockBytes, err := p.Lock.Marshal()
	if err != nil {
		return nil, err
	}
	if !p.HasLock || len(p.LockDiffs) > 0 {
		lr, err := w.WriteFileMode(p.LockPath, lockBytes, 0o644)
		if err != nil {
			return nil, err
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
	sb, err := p.State.Marshal()
	if err != nil {
		return nil, err
	}
	if _, err := w.WriteFileMode(filepath.Join(p.StateDir, state.FileName), sb, 0o600); err != nil {
		return nil, err
	}
	res.RunID, err = w.Commit(x.Note)
	return res, err
}
