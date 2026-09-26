package gitmod

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/githook"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

type machine struct {
	t       *testing.T
	home    string
	backups string
	ts      *state.TargetState
	n       int
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("git module targets macOS and Linux in Stage 2")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	h := t.TempDir()
	// never touch (or be influenced by) the developer's real git configuration
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", h)
	return &machine{t: t, home: h, backups: t.TempDir(), ts: &state.TargetState{}}
}

func (m *machine) opts() Options {
	pi, err := platform.New(platform.Options{GOOS: runtime.GOOS, GOARCH: "arm64", Getenv: func(k string) string {
		if k == "HOME" {
			return m.home
		}
		return ""
	}})
	if err != nil {
		m.t.Fatal(err)
	}
	return Options{Plat: pi, Getenv: func(k string) string {
		if k == "HOME" {
			return m.home
		}
		return ""
	}, Rigfile: "/usr/local/bin/rigfile", Backstop: true, State: m.ts}
}

func (m *machine) plan(mod func(*Options)) *engine.Plan {
	m.t.Helper()
	o := m.opts()
	if mod != nil {
		mod(&o)
	}
	p, err := Build(o)
	if err != nil {
		m.t.Fatal(err)
	}
	return p
}

// apply runs a plan through the journaled writer like session.Execute does and returns the run id.
func (m *machine) apply(p *engine.Plan) string {
	m.t.Helper()
	m.n++
	w := &apply.Writer{BackupRoot: m.backups, Now: func() time.Time { return time.Date(2026, 9, 25, 12, 0, m.n, 0, time.UTC) }}
	if err := p.Apply(&engine.Exec{W: w}, m.ts); err != nil {
		m.t.Fatal(err)
	}
	id, err := w.Commit("test")
	if err != nil {
		m.t.Fatal(err)
	}
	return id
}

func (m *machine) write(rel, content string) string {
	p := filepath.Join(m.home, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		m.t.Fatal(err)
	}
	return p
}

func (m *machine) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(m.home, rel))
	return string(b)
}

func symbols(p *engine.Plan) string {
	var s []string
	for _, o := range p.Ops {
		s = append(s, o.Key+o.Symbol)
	}
	return strings.Join(s, " ")
}

func gitGet(t *testing.T, file, key string) string {
	t.Helper()
	out, _ := exec.Command("git", "config", "--file", file, "--type=path", "--get", key).Output()
	return strings.TrimSpace(string(out))
}

func TestFreshMachineInstallsHooksConfigAndIgnore(t *testing.T) {
	m := newMachine(t)
	p := m.plan(nil)
	if symbols(p) != "hooks+ config+ gitignore+" {
		t.Fatalf("%s", symbols(p))
	}
	if !p.Ops[0].Runs {
		t.Fatal("hooks execute code and must be flagged")
	}
	m.apply(p)

	hooks := filepath.Join(m.home, ".config", "rigfile", "git-hooks")
	for _, f := range append([]string{githook.DispatcherName}, githook.HookNames...) {
		st, err := os.Stat(filepath.Join(hooks, f))
		if err != nil || st.Mode().Perm() != 0o755 {
			t.Fatalf("%s: %v %v", f, err, st)
		}
	}
	if _, err := os.Stat(filepath.Join(hooks, githook.PreviousFile)); err == nil {
		t.Fatal("no previous hooks dir, so nothing to chain")
	}
	if got := gitGet(t, filepath.Join(m.home, ".gitconfig"), "core.hooksPath"); got != hooks {
		t.Fatalf("core.hooksPath = %q, want %q", got, hooks)
	}
	ign := m.read(".config/git/ignore")
	if !strings.Contains(ign, "\n.env\n") || !strings.Contains(ign, "!.env.example") || !strings.Contains(ign, "rigfile:begin "+IgnoreRegion) {
		t.Fatalf("ignore block missing:\n%s", ign)
	}

	// idempotent, and everything Rigfile owns verifies clean
	p2 := m.plan(nil)
	if symbols(p2) != "hooks= config= gitignore=" {
		t.Fatalf("second plan must change nothing: %s", symbols(p2))
	}
	if ds := state.Check(m.ts.Items, nil); !state.Clean(ds) {
		t.Fatalf("drift right after apply: %+v", ds)
	}
	if len(m.ts.Items) != 3 {
		t.Fatalf("%+v", m.ts.Items)
	}
}

func TestExistingUserSetupIsChainedNotClobberedAndRollbackRestoresIt(t *testing.T) {
	m := newMachine(t)
	prevHooks := filepath.Join(m.home, ".config", "git", "hooks")
	userConf := "[user]\n\tname = Jia\n[core]\n\thooksPath = " + prevHooks + "\n\teditor = vim\n"
	m.write(".gitconfig", userConf)
	m.write(".config/git/hooks/pre-commit", "#!/bin/sh\nexit 0\n")
	userIgnore := "# mine\n*.log\n"
	m.write(".config/git/ignore", userIgnore)

	before := map[string]string{".gitconfig": m.read(".gitconfig"), ".config/git/ignore": m.read(".config/git/ignore")}
	p := m.plan(nil)
	if !strings.Contains(strings.Join(p.Ops[0].Detail, "\n"), "chains to your existing hooks") {
		t.Fatalf("the plan screen must say the previous hooks are chained: %v", p.Ops[0].Detail)
	}
	run := m.apply(p)

	conf := m.read(".gitconfig")
	if !strings.HasPrefix(conf, userConf) {
		t.Fatalf("the user's own lines must be untouched and first:\n%s", conf)
	}
	ours := filepath.Join(m.home, ".config", "rigfile", "git-hooks")
	if got := gitGet(t, filepath.Join(m.home, ".gitconfig"), "core.hooksPath"); got != ours {
		t.Fatalf("git must now see our hooks dir (last value wins), got %q", got)
	}
	if got := strings.TrimSpace(m.read(".config/rigfile/git-hooks/" + githook.PreviousFile)); got != prevHooks {
		t.Fatalf("previous hooks dir not recorded for chaining: %q", got)
	}
	if !strings.HasPrefix(m.read(".config/git/ignore"), userIgnore) {
		t.Fatal("the user's own ignore patterns must stay first and untouched")
	}
	// re-applying keeps chaining to the ORIGINAL previous dir (it must not record our own dir as "previous")
	p2 := m.plan(nil)
	if symbols(p2) != "hooks= config= gitignore=" {
		t.Fatalf("%s", symbols(p2))
	}

	// rollback: byte-identical, hooks dir gone
	out, err := apply.Rollback(m.backups, run, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if o.Action == "skipped" {
			t.Fatalf("rollback skipped %s: %s", o.Path, o.Reason)
		}
	}
	for rel, want := range before {
		if got := m.read(rel); got != want {
			t.Fatalf("%s not restored:\n%q\nwant\n%q", rel, got, want)
		}
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Fatal("the hooks dir should be gone after rollback")
	}
	if got := gitGet(t, filepath.Join(m.home, ".gitconfig"), "core.hooksPath"); got != prevHooks {
		t.Fatalf("the user's own hooksPath is effective again, got %q", got)
	}
}

func TestConfiguredExcludesFileIsUsedAndNoConfigLineIsAddedForIt(t *testing.T) {
	m := newMachine(t)
	custom := filepath.Join(m.home, ".gitignore_global")
	m.write(".gitconfig", "[core]\n\texcludesFile = "+custom+"\n")
	m.write(".gitignore_global", "*.swp\n")
	m.apply(m.plan(nil))
	if got := m.read(".gitignore_global"); !strings.HasPrefix(got, "*.swp\n") || !strings.Contains(got, "!.env.example") {
		t.Fatalf("block must be spliced into the user's excludes file:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".config", "git", "ignore")); err == nil {
		t.Fatal("the default excludes file must not be created when the user configured their own")
	}
	if strings.Count(m.read(".gitconfig"), "excludesFile") != 1 {
		t.Fatal("core.excludesFile must not be rewritten")
	}
}

func TestHandEditsAreConflictsUntilOverwrite(t *testing.T) {
	m := newMachine(t)
	m.apply(m.plan(nil))
	hooks := filepath.Join(m.home, ".config", "rigfile", "git-hooks")

	// someone edits a hook file
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// and the ignore block
	ig := filepath.Join(m.home, ".config", "git", "ignore")
	b, _ := os.ReadFile(ig)
	_ = os.WriteFile(ig, bytes.Replace(b, []byte(".DS_Store"), []byte(".DS_Store\n# sneaky"), 1), 0o644)

	p := m.plan(nil)
	if symbols(p) != "hooks! config= gitignore!" {
		t.Fatalf("%s", symbols(p))
	}
	for _, o := range p.Conflicts() {
		if len(o.Keep) == 0 {
			t.Fatalf("a refused item must keep its ownership record: %+v", o)
		}
	}
	p = m.plan(func(o *Options) { o.Overwrite = true })
	if symbols(p) != "hooks~ config= gitignore~" {
		t.Fatalf("%s", symbols(p))
	}
	m.apply(p)
	if ds := state.Check(m.ts.Items, nil); !state.Clean(ds) {
		t.Fatalf("%+v", ds)
	}
}

func TestBackstopSwitchAndRigfilePathAreInTheDispatcher(t *testing.T) {
	m := newMachine(t)
	m.apply(m.plan(func(o *Options) { o.Backstop = false; o.Rigfile = "/opt/my tools/rigfile" }))
	d := m.read(".config/rigfile/git-hooks/" + githook.DispatcherName)
	if !strings.Contains(d, "BACKSTOP=0") || !strings.Contains(d, "'/opt/my tools/rigfile'") {
		t.Fatalf("%s", d)
	}
	p := m.plan(func(o *Options) { o.Backstop = false; o.Rigfile = "/opt/my tools/rigfile" })
	if strings.Contains(strings.Join(p.Ops[0].Detail, "\n"), "reference-transaction") {
		t.Fatal("the plan must not advertise a backstop that is off")
	}
	// turning it on later is an update of the hooks dir only
	if symbols(m.plan(func(o *Options) { o.Rigfile = "/opt/my tools/rigfile" })) != "hooks~ config= gitignore=" {
		t.Fatal("enabling the backstop should update just the hooks")
	}
}

func TestNoGitAndRelativePreviousPath(t *testing.T) {
	m := newMachine(t)
	p := m.plan(func(o *Options) { o.GitBin = "definitely-not-git" })
	if len(p.Ops) != 0 || len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "git is not installed") {
		t.Fatalf("%+v", p)
	}
	m.write(".gitconfig", "[core]\n\thooksPath = relative/hooks\n")
	p = m.plan(nil)
	if len(p.Notes) == 0 || !strings.Contains(p.Notes[0], "relative") {
		t.Fatalf("a relative previous hooksPath cannot be chained and must be reported: %v", p.Notes)
	}
}

func TestMarkerProblemsFailLoudlyInsteadOfEditingBlindly(t *testing.T) {
	m := newMachine(t)
	m.write(".gitconfig", "# rigfile:begin "+ConfigRegion+"\n[core]\n")
	if _, err := Build(m.opts()); err == nil || !strings.Contains(err.Error(), "markers") {
		t.Fatalf("unbalanced markers must be an error: %v", err)
	}
}
