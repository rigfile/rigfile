// Package basecheck verifies that rigfile/base-secure is intact on this machine (RIGFILE_PLAN.md §8.4): it
// plans the base layer ALONE against the real files and reports every change it would still make. Nothing is
// written; the same planners that apply the layer are the ones that judge it, so "intact" means exactly "an
// apply would change nothing".
package basecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	basesecure "github.com/digitaldreamer3462/rigfile/base-secure"
	"github.com/digitaldreamer3462/rigfile/internal/adapters/claudecode"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/githook"
	"github.com/digitaldreamer3462/rigfile/internal/gitmod"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/merge"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// Options describe the machine to check.
type Options struct {
	Plat      *platform.Info
	Getenv    func(string) string
	ClaudeDir string
	State     *state.State
	Rigfile   string // fallback rigfile path for the git hooks when none is recorded
	Sandbox   bool
	Have      func(string) bool
}

// Result lists what differs. Empty slices mean intact; Applied* say whether that side was ever applied.
type Result struct {
	ClaudeApplied, GitApplied bool
	Claude, Git               []string // one line per outstanding change or conflict
	Notes                     []string
}

// Check plans base-secure alone. Removal ops are ignored: they are the user's own rig items, which a
// base-only plan does not know about.
func Check(o Options) (*Result, error) {
	res := &Result{}
	dir, cleanup, err := basesecure.Extract()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	base, err := manifest.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("the embedded base-secure layer failed to load: %w", err)
	}
	m, err := merge.Merge([]merge.Layer{{Name: base.M.Name, Version: base.M.Version, Locked: true, M: base.M, Dir: base.Dir}}, claudecode.Target)
	if err != nil {
		return nil, err
	}
	proj := m.Project(string(o.Plat.OS), claudecode.Target)

	cts := o.State.Targets[claudecode.StateTarget]
	res.ClaudeApplied = cts != nil && len(cts.Items) > 0
	cplan, err := claudecode.Build(claudecode.Env{Plat: o.Plat, ClaudeDir: o.ClaudeDir, State: cts, BaseSecure: true, Sandbox: o.Sandbox, Have: o.Have, CheckOnly: true}, proj)
	if err != nil {
		return nil, err
	}
	res.Claude = drift(cplan)
	res.Notes = append(res.Notes, cplan.Notes...)

	gts := o.State.Targets[gitmod.Target]
	res.GitApplied = gts != nil && len(gts.Items) > 0
	if res.GitApplied {
		bin, backstop := recordedDispatcher(o)
		gplan, err := gitmod.Build(gitmod.Options{Plat: o.Plat, Getenv: o.Getenv, Rigfile: bin, Backstop: backstop, State: gts})
		if err != nil {
			return nil, err
		}
		res.Git = drift(gplan)
	}
	return res, nil
}

func drift(p *engine.Plan) []string {
	var out []string
	for _, op := range p.Ops {
		switch op.Symbol {
		case engine.New, engine.Update:
			out = append(out, fmt.Sprintf("%s %s is out of date or missing", op.Category, op.Key))
			for _, d := range op.Detail {
				if strings.HasPrefix(d, "+") || strings.HasPrefix(d, "~") {
					out = append(out, "    "+d)
				}
			}
		case engine.Conflict:
			out = append(out, fmt.Sprintf("%s %s: %s", op.Category, op.Key, op.Summary))
		}
	}
	return out
}

var (
	reBin      = regexp.MustCompile(`(?m)^RIGFILE='(.*)'$`)
	reBackstop = regexp.MustCompile(`(?m)^BACKSTOP=(\d)$`)
)

// RecordedRigfile reads the rigfile path and backstop switch from the installed dispatcher.
func RecordedRigfile(hooksDir string) (bin string, backstop, ok bool) {
	b, err := os.ReadFile(filepath.Join(hooksDir, githook.DispatcherName))
	if err != nil {
		return "", false, false
	}
	m := reBin.FindSubmatch(b)
	if m == nil {
		return "", false, false
	}
	bin = strings.ReplaceAll(string(m[1]), `'\''`, "'")
	if bm := reBackstop.FindSubmatch(b); bm != nil {
		backstop = string(bm[1]) == "1"
	}
	return bin, backstop, true
}

// recordedDispatcher: what the installed hooks call, so a doctor run from another binary path is not
// mistaken for drift.
func recordedDispatcher(o Options) (string, bool) {
	paths, err := gitmod.Locate(gitmod.Options{Plat: o.Plat, Getenv: o.Getenv})
	if err == nil {
		if bin, backstop, ok := RecordedRigfile(paths.HooksDir); ok {
			return bin, backstop
		}
	}
	return o.Rigfile, true
}
