// Package gitmod is the git half of base-secure (plan §8.1a-c): a built-in "host module" that plans,
// applies, journals and rolls back machine-level git configuration through the same engine as the agent
// adapters. It installs
//
//   - a hooks directory (~/.config/rigfile/git-hooks) whose dispatcher runs Rigfile's checks, then the hooks
//     the machine used before, then the repository's own (internal/githook);
//   - a marked block in the global git config that points core.hooksPath at it. Because git honours the LAST
//     value in a file, the block is appended and the user's own lines are never edited; removing the block
//     (rollback) restores exactly what they had;
//   - a marked block in the global excludes file (core.excludesFile, or git's default when unset) with the
//     credential-file patterns.
//
// Nothing here runs `sudo`, touches system config, or edits a repository.
package gitmod

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/githook"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/splice"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// Target is this module's name in state.json.
const Target = "git"

// Region ids of the blocks Rigfile owns inside the user's files.
const (
	ConfigRegion = "base-secure-git"
	IgnoreRegion = "base-secure-gitignore"
)

// Options describe one planning run.
type Options struct {
	Plat      *platform.Info
	Getenv    func(string) string
	Rigfile   string             // absolute path of the rigfile executable the hooks call
	Backstop  bool               // install the reference-transaction backstop (decision O9: on by default)
	State     *state.TargetState // what a previous apply recorded (nil = nothing)
	Overwrite bool
	GitBin    string // "git"; tests may point elsewhere
}

func (o Options) git() string {
	if o.GitBin != "" {
		return o.GitBin
	}
	return "git"
}

type builder struct {
	o    Options
	plan *engine.Plan
	home string
}

// Paths is where the module reads and writes; exported for doctor and tests.
type Paths struct {
	HooksDir   string
	GlobalConf string
	Excludes   string
}

// Locate resolves the paths for this machine.
func Locate(o Options) (Paths, error) {
	home, err := o.Plat.Home()
	if err != nil {
		return Paths{}, err
	}
	cfgDir, err := o.Plat.RigfileConfigDir()
	if err != nil {
		return Paths{}, err
	}
	p := Paths{HooksDir: filepath.Join(cfgDir, "git-hooks"), GlobalConf: globalConfigPath(o.Getenv, home)}
	if v, ok := configGet(o.git(), p.GlobalConf, "core.excludesFile"); ok && v != "" {
		p.Excludes = v
	} else {
		xdg := o.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		p.Excludes = filepath.Join(xdg, "git", "ignore") // git's default when core.excludesFile is unset
	}
	return p, nil
}

// globalConfigPath is the file `git config --global` would write.
func globalConfigPath(getenv func(string) string, home string) string {
	if v := getenv("GIT_CONFIG_GLOBAL"); v != "" {
		return v
	}
	legacy := filepath.Join(home, ".gitconfig")
	xdg := getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	xdgConf := filepath.Join(xdg, "git", "config")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	if _, err := os.Stat(xdgConf); err == nil {
		return xdgConf
	}
	return legacy
}

// configGet reads one value from a specific config file, expanding ~ for path settings. ok is false when the
// key is unset or git cannot run.
func configGet(gitBin, file, key string) (string, bool) {
	if _, err := os.Stat(file); err != nil {
		return "", false
	}
	c := exec.Command(gitBin, "config", "--file", file, "--type=path", "--get", key)
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(out.String()), true
}

// Build plans the git module. It never writes anything.
func Build(o Options) (*engine.Plan, error) {
	b := &builder{o: o, plan: &engine.Plan{Target: "Git protections (base-secure)"}}
	if _, err := exec.LookPath(o.git()); err != nil {
		b.plan.Notes = append(b.plan.Notes, "git is not installed: the git protections (global gitignore, secret-scanning hooks) were skipped")
		return b.plan, nil
	}
	if o.Rigfile == "" {
		return nil, errors.New("gitmod: no rigfile executable path")
	}
	home, err := o.Plat.Home()
	if err != nil {
		return nil, err
	}
	b.home = home
	paths, err := Locate(o)
	if err != nil {
		return nil, err
	}

	if err := b.hooks(paths); err != nil {
		return nil, err
	}
	if err := b.region("config", paths.GlobalConf, ConfigRegion, configBlock(paths.HooksDir),
		"core.hooksPath → "+engine.Short(paths.HooksDir, home)); err != nil {
		return nil, err
	}
	if err := b.region("gitignore", paths.Excludes, IgnoreRegion, GlobalIgnore, "global excludes"); err != nil {
		return nil, err
	}
	return b.plan, nil
}

func configBlock(hooksDir string) string {
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(platform.ToShellPath(hooksDir))
	return "# managed by rigfile (base-secure): global git hooks that scan for secrets, chained to your previous hooks.\n" +
		"# Remove this block (or run `rigfile rollback`) to restore your previous setting.\n" +
		"[core]\n\thooksPath = \"" + q + "\"\n"
}

// previousHooksPath is the hooks directory that was configured before Rigfile ("" = none). The global
// config may already contain our block, in which case the recorded value is kept.
func (b *builder) previousHooksPath(p Paths) string {
	cur, _ := configGet(b.o.git(), p.GlobalConf, "core.hooksPath")
	recorded := ""
	if bts, err := os.ReadFile(filepath.Join(p.HooksDir, githook.PreviousFile)); err == nil {
		recorded = strings.TrimSpace(string(bts))
	}
	prev := cur
	if sameDir(cur, p.HooksDir) {
		prev = recorded
	}
	if prev != "" && !filepath.IsAbs(prev) {
		b.plan.Notes = append(b.plan.Notes, fmt.Sprintf("your core.hooksPath (%s) is relative, so it cannot be chained from a global directory; it was not chained", prev))
		return ""
	}
	return platform.ToShellPath(prev) // the dispatcher is a sh script (Git for Windows): forward slashes
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func (b *builder) owned(category, key, path string) (state.Item, bool) {
	if b.o.State == nil {
		return state.Item{}, false
	}
	for _, it := range b.o.State.Items {
		if it.Category == category && it.Key == key && it.Path == path {
			return it, true
		}
	}
	return state.Item{}, false
}

type fileSpec struct {
	rel  string
	data []byte
	exec bool
}

func (b *builder) hooks(p Paths) error {
	prev := b.previousHooksPath(p)
	var files []fileSpec
	files = append(files, fileSpec{githook.DispatcherName, []byte(githook.DispatchScript(b.o.Rigfile, b.o.Backstop)), true})
	for _, n := range githook.HookNames {
		files = append(files, fileSpec{n, []byte(githook.ShimScript(n)), true})
	}
	if prev != "" {
		files = append(files, fileSpec{githook.PreviousFile, []byte(prev + "\n"), false})
	}
	entries := make([]hashing.Entry, 0, len(files))
	for _, f := range files {
		// NTFS has no execute bit, so a Windows tree is hashed with none (Git for Windows runs hooks through sh anyway).
		entries = append(entries, hashing.Entry{Path: f.rel, Size: int64(len(f.data)), SHA256: hashing.Bytes(f.data), Exec: f.exec && b.o.Plat.OS != platform.Windows})
	}
	sortEntries(entries)
	want := hashing.TreeOf(entries)
	item := state.Item{Category: "git", Key: "hooks", Kind: state.KindTree, Path: p.HooksDir, Hash: want}
	label := "hooks → " + engine.Short(p.HooksDir, b.home)
	op := engine.Op{Category: "git", Key: "hooks", Items: []state.Item{item}, Runs: true}
	op.Detail = []string{
		"pre-commit: scans the staged changes for secrets and credential files (blocks)",
		"pre-push: re-scans the commits being pushed (blocks)",
	}
	if b.o.Backstop {
		op.Detail = append(op.Detail, "reference-transaction: blocks a commit made with `git commit --no-verify` that contains a secret")
	}
	if prev != "" {
		op.Detail = append(op.Detail, "chains to your existing hooks in "+engine.Short(prev, b.home)+" (they still run, after Rigfile's check)")
	}
	op.Detail = append(op.Detail, "the repository's own .git/hooks keep running too (git would otherwise ignore them)")

	do := func(x *engine.Exec) error {
		have := map[string]bool{}
		for _, f := range files {
			have[f.rel] = true
			mode := os.FileMode(0o644)
			if f.exec {
				mode = 0o755
			}
			if _, err := x.W.WriteFileMode(filepath.Join(p.HooksDir, f.rel), f.data, mode); err != nil {
				return err
			}
		}
		old, _ := hashing.TreeEntries(p.HooksDir)
		for _, e := range old {
			if !have[e.Path] {
				if _, err := x.W.Delete(filepath.Join(p.HooksDir, filepath.FromSlash(e.Path))); err != nil {
					return err
				}
			}
		}
		return nil
	}

	st, err := os.Lstat(p.HooksDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		op.Symbol, op.Summary, op.Do = engine.New, label, do
	case err != nil:
		return err
	case !st.IsDir():
		op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   a file with that name exists; not touched", nil
	default:
		cur, herr := hashing.Tree(p.HooksDir)
		prevItem, mine := b.owned("git", "hooks", p.HooksDir)
		switch {
		case herr != nil:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   cannot be inspected ("+herr.Error()+"); not touched", nil
		case cur == want:
			op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
		case mine && prevItem.Hash == cur:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (updated)", do
		case b.o.Overwrite:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (--overwrite)", do
		case mine:
			op.Symbol, op.Summary, op.Items, op.Keep = engine.Conflict, label+"   edited by hand since Rigfile wrote it; not touched (use --overwrite)", nil, []state.Item{prevItem}
		default:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   already exists and is not managed by Rigfile; not touched (use --overwrite)", nil
		}
	}
	b.plan.Ops = append(b.plan.Ops, op)
	return nil
}

func sortEntries(es []hashing.Entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j].Path < es[j-1].Path; j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}

// region plans one marked block inside a user-owned file (the global git config, the global excludes file).
func (b *builder) region(key, path, id, body, what string) error {
	orig, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	reg, found, ferr := splice.Find(orig, splice.Hash, id)
	if ferr != nil {
		return fmt.Errorf("%s: %w (fix the markers by hand; Rigfile will not edit a file it cannot parse safely)", engine.Short(path, b.home), ferr)
	}
	label := engine.Short(path, b.home)
	op := engine.Op{Category: "git", Key: key, Detail: []string{what}}
	if found && reg.Drifted && !b.o.Overwrite {
		op.Symbol, op.Summary = engine.Conflict, label+"   the Rigfile block was edited by hand; not touched (use --overwrite)"
		if prev, mine := b.owned("git", key, path); mine {
			op.Keep = []state.Item{prev}
		}
		b.plan.Ops = append(b.plan.Ops, op)
		return nil
	}
	next, changed, err := splice.Upsert(orig, splice.Hash, id, []byte(body), splice.Options{Overwrite: b.o.Overwrite})
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	r, _, _ := splice.Find(next, splice.Hash, id)
	op.Items = []state.Item{{Category: "git", Key: key, Kind: state.KindRegion, Path: path, Hash: r.Hash,
		Detail: map[string]string{"region": id, "style": "hash"}}}
	switch {
	case !changed && found:
		op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
	default:
		op.Symbol = engine.Update
		if !exists {
			op.Symbol = engine.New
		}
		op.Summary = label
		op.Do = func(x *engine.Exec) error { _, err := x.W.WriteFile(path, next); return err }
	}
	b.plan.Ops = append(b.plan.Ops, op)
	return nil
}
