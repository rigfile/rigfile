package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/sigverify"
	"github.com/digitaldreamer3462/rigfile/internal/source"
)

// ---- harness ----------------------------------------------------------------------------------

type fakeMCP struct {
	avail   bool
	servers map[string]string
	calls   []string
}

func newFakeMCP() *fakeMCP { return &fakeMCP{avail: true, servers: map[string]string{}} }

func (f *fakeMCP) Available() bool { return f.avail }
func (f *fakeMCP) Present(n string) (bool, error) {
	_, ok := f.servers[n]
	return ok, nil
}
func (f *fakeMCP) AddJSON(n, d string) error {
	f.calls = append(f.calls, "add "+n)
	f.servers[n] = d
	return nil
}
func (f *fakeMCP) Remove(n string) error {
	f.calls = append(f.calls, "remove "+n)
	delete(f.servers, n)
	return nil
}

type fakeTools struct {
	have map[string]bool
	ran  []string
	fail string
}

func (f *fakeTools) Has(c string) bool { return f.have[c] }
func (f *fakeTools) Run(_ context.Context, argv []string, out io.Writer) error {
	line := strings.Join(argv, " ")
	f.ran = append(f.ran, line)
	if f.fail != "" && line == f.fail {
		return errors.New("exit status 1")
	}
	fmt.Fprintln(out, "installed", argv[len(argv)-1])
	return nil
}

type machine struct {
	t         *testing.T
	home      string
	mcp       *fakeMCP
	tools     *fakeTools
	env       map[string]string
	src       *source.Client                                          // git sources for pull/update; nil = the real services
	tty       bool                                                    // behave as an interactive terminal (scripted key presses on stdin)
	ran       [][]string                                              // vendor login commands that were "run"
	key       string                                                  // what the hidden prompt returns
	pollEvery time.Duration                                           // how often publish checks the registry scan
	verify    func(bundle, tarball []byte) (*sigverify.Result, error) // fake Sigstore verification
}

func newMachine(t *testing.T) *machine {
	h := t.TempDir()
	return &machine{t: t, home: h, mcp: newFakeMCP(), tools: &fakeTools{have: map[string]bool{"npm": true}}, env: map[string]string{"HOME": h, "USERPROFILE": h, "PATH": os.Getenv("PATH"),
		"APPDATA": filepath.Join(h, "AppData", "Roaming"), "LOCALAPPDATA": filepath.Join(h, "AppData", "Local")}}
}

type result struct {
	code     int
	out, err string
}

func (m *machine) run(stdin string, args ...string) result {
	var out, errb bytes.Buffer
	code := run(args, env{
		in: strings.NewReader(stdin), out: &out, err: &errb,
		getenv: func(k string) string { return m.env[k] }, mcp: m.mcp, sources: m.src, interactive: m.tty, pollEvery: m.pollEvery, verifySig: m.verify,
		runCmd: func(_ context.Context, argv []string) error { m.ran = append(m.ran, argv); return nil },
		hidden: func(string) ([]byte, error) { return []byte(m.key), nil }, keyringOff: true, tools: m.tools,
		lookPath: func(n string) (string, error) {
			if n == "rigfile" || n == "claude" || n == "gh" {
				return "/usr/local/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
	})
	// Plan screens print paths with the OS separator; the assertions are written with "/", so compare like with like.
	return result{code, portable(out.String()), portable(errb.String())}
}

// portable turns Windows path separators in program output into "/" (a no-op elsewhere).
func portable(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(s, `\`, "/")
	}
	return s
}

// stateDir is where Rigfile keeps state.json and backups on this OS for this machine's environment
// (~/.rigfile on macOS/Linux, %LOCALAPPDATA%\rigfile on Windows).
func (m *machine) stateDir() string {
	pi, err := platform.New(platform.Options{Getenv: func(k string) string { return m.env[k] }})
	if err != nil {
		m.t.Fatal(err)
	}
	d, err := pi.StateDir()
	if err != nil {
		m.t.Fatal(err)
	}
	return d
}

func put(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

const rigYAML = `apiVersion: rigfile.dev/v1
name: jiaxu/demo
version: 1.0.0
instructions:
  - {id: coding-style, file: instructions/style.md}
skills:
  - {path: skills/pdf}
agents:
  - {path: agents/reviewer.md}
hooks:
  - {id: guard, event: pre_tool_use, match: {tool: bash}, run: 'builtin:guard'}
mcp_servers:
  alpaca:
    command: npx
    args: ['-y', 'alpaca-mcp@1.4.2']
    env: {ALPACA_API_KEY: 'secret://alpaca/api_key'}
permissions:
  deny: [{read: '~/.ssh/**'}]
  ask: [{bash: 'git push*'}]
secrets:
  alpaca/api_key: {description: Alpaca key, obtain_url: 'https://example.test/keys'}
logins:
  - {provider: claude-code, method: vendor-cli}
`

func newRig(t *testing.T) string {
	dir := t.TempDir()
	put(t, dir, "rigfile.yaml", rigYAML, 0o644)
	put(t, dir, "instructions/style.md", "# Style\n- be terse\n", 0o644)
	put(t, dir, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: PDFs\n---\nbody\n", 0o644)
	put(t, dir, "agents/reviewer.md", "---\nname: reviewer\ndescription: r\n---\nx\n", 0o644)
	return dir
}

// ---- tests ------------------------------------------------------------------------------------

func TestValidate(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	if r := m.run("", "validate", rig); r.code != 0 || !strings.Contains(r.out, "valid") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "validate", filepath.Join(rig, "rigfile.yaml")); r.code != 0 || !strings.Contains(r.out, "schema only") {
		t.Fatalf("%+v", r)
	}
	put(t, rig, "rigfile.yaml", strings.Replace(rigYAML, "skills/pdf", "skills/missing", 1), 0o644)
	if r := m.run("", "validate", rig); r.code != 1 || !strings.Contains(r.err, "skills/missing does not exist") {
		t.Fatalf("%+v", r)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("apiVersion: rigfile.dev/v1\nname: NOPE\nversion: 1.0.0\n"), 0o644)
	if r := m.run("", "validate", bad); r.code != 1 || !strings.Contains(r.err, "invalid") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "validate"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestPlanShowsTheReviewScreenAndWritesNothing(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	r := m.run("", "plan", rig)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"Rig: jiaxu/demo@1.0.0", "Target: claude-code", "INSTRUCTIONS", "SKILLS", "AGENTS", "MCP SERVERS", "HOOKS", "PERMISSIONS",
		"⚠ executes code", "SECRET NEEDED  alpaca/api_key", "get it: https://example.test/keys", "LOGIN NEEDED   claude-code", "No rigfile.lock yet", "change(s)"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("plan output missing %q:\n%s", want, r.out)
		}
	}
	for _, p := range []string{filepath.Join(m.home, ".claude"), m.stateDir()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("plan created %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(rig, "rigfile.lock")); !os.IsNotExist(err) {
		t.Fatal("plan must not write the lockfile")
	}
}

func TestApplyThenIdempotentThenDriftThenRollback(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)

	r := m.run("", "apply", rig, "--yes")
	if r.code != 0 || !strings.Contains(r.out, "applied") || !strings.Contains(r.out, "rigfile rollback") {
		t.Fatalf("%+v", r)
	}
	cd := filepath.Join(m.home, ".claude")
	for _, f := range []string{"CLAUDE.md", "settings.json", "skills/pdf/SKILL.md", "agents/reviewer.md"} {
		if _, err := os.Stat(filepath.Join(cd, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if !json.Valid(mustRead(t, filepath.Join(cd, "settings.json"))) {
		t.Fatal("settings.json invalid")
	}
	if _, err := os.Stat(filepath.Join(rig, "rigfile.lock")); err != nil {
		t.Fatal("lockfile not written next to the rig")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(m.stateDir(), "state.json")); st.Mode().Perm() != 0o600 {
			t.Fatalf("state.json mode %v", st.Mode().Perm())
		}
	}
	if !strings.Contains(m.mcp.servers["alpaca"], `"--secret","ALPACA_API_KEY=alpaca/api_key"`) {
		t.Fatalf("mcp entry: %s", m.mcp.servers["alpaca"])
	}

	// second apply changes nothing and creates no new run
	runsBefore, _ := apply.ListRuns(filepath.Join(m.stateDir(), "backups"))
	r = m.run("", "apply", rig, "--yes")
	if r.code != 0 || !strings.Contains(r.out, "nothing to change") || !strings.Contains(r.out, "no changes") {
		t.Fatalf("%+v", r)
	}
	if runsAfter, _ := apply.ListRuns(filepath.Join(m.stateDir(), "backups")); len(runsAfter) != len(runsBefore) {
		t.Fatalf("a no-op apply must not create a run: %d -> %d", len(runsBefore), len(runsAfter))
	}

	// diff is clean, then shows a hand edit
	if r = m.run("", "diff"); r.code != 0 || !strings.Contains(r.out, "no drift") {
		t.Fatalf("%+v", r)
	}
	_ = os.WriteFile(filepath.Join(cd, "agents/reviewer.md"), []byte("hand edit"), 0o644)
	if r = m.run("", "diff"); r.code != 1 || !strings.Contains(r.out, "✘ agent") || !strings.Contains(r.out, "differ") {
		t.Fatalf("%+v", r)
	}
	// ...and removing the MCP server shows up too
	delete(m.mcp.servers, "alpaca")
	if r = m.run("", "diff"); !strings.Contains(r.out, "not registered") {
		t.Fatalf("%+v", r)
	}

	// rollback: the hand-edited agent is skipped (exit 3), everything else is undone
	r = m.run("", "rollback")
	if r.code != 3 || !strings.Contains(r.out, "skipped") || !strings.Contains(r.out, "--force") {
		t.Fatalf("%+v", r)
	}
	if b := mustRead(t, filepath.Join(cd, "agents/reviewer.md")); string(b) != "hand edit" {
		t.Fatal("rollback overwrote a hand edit")
	}
	if r = m.run("", "rollback", "--force"); r.code != 0 || !strings.Contains(r.out, "rolled back") {
		t.Fatalf("%+v", r)
	}
	if r = m.run("", "diff"); !strings.Contains(r.out, "nothing has been applied yet") {
		t.Fatalf("state must be rolled back too: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(cd, "skills")); !os.IsNotExist(err) {
		t.Fatal("skills dir should be gone after rollback")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLockfileProtectsAgainstChangedRig(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	if r := m.run("", "lock", rig); r.code != 0 || !strings.Contains(r.out, "wrote") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "lock", rig); !strings.Contains(r.out, "already up to date") {
		t.Fatalf("%+v", r)
	}
	// the rig changes after it was locked
	put(t, rig, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: CHANGED\n---\n", 0o644)
	r := m.run("", "apply", rig, "--yes")
	if r.code != 1 || !strings.Contains(r.err, "lockfile") || !strings.Contains(r.out, `skill "pdf"`) {
		t.Fatalf("apply must refuse a rig that no longer matches its lock: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written when the lock check fails")
	}
	r = m.run("", "apply", rig, "--yes", "--update-lock")
	if r.code != 0 || !strings.Contains(r.out, "wrote") {
		t.Fatalf("%+v", r)
	}
	if r = m.run("", "plan", rig); strings.Contains(r.out, "does not match") {
		t.Fatalf("lock should match after --update-lock: %s", r.out)
	}
}

func TestConfirmationAndAbort(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	r := m.run("n\n", "apply", rig)
	if r.code != 1 || !strings.Contains(r.out, "aborted") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("declined apply must not write")
	}
	if r = m.run("a\n", "apply", rig); r.code != 0 || !strings.Contains(r.out, "applied") {
		t.Fatalf("%+v", r)
	}
}

func TestConflictsExitThreeAndOverwriteResolves(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	put(t, filepath.Join(m.home, ".claude"), "agents/reviewer.md", "the user's own reviewer", 0o644)
	r := m.run("", "apply", rig, "--yes")
	if r.code != 3 || !strings.Contains(r.out, "refused") {
		t.Fatalf("%+v", r)
	}
	if string(mustRead(t, filepath.Join(m.home, ".claude", "agents/reviewer.md"))) != "the user's own reviewer" {
		t.Fatal("conflicting file overwritten")
	}
	if r = m.run("", "apply", rig, "--yes", "--overwrite"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestProblemsBlockPlanAndApply(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	put(t, rig, "rigfile.yaml", strings.Replace(rigYAML, "agents/reviewer.md", "agents/nope.md", 1), 0o644)
	for _, verb := range []string{"plan", "apply"} {
		args := []string{verb, rig}
		if verb == "apply" {
			args = append(args, "--yes")
		}
		r := m.run("", args...)
		if r.code != 1 || !strings.Contains(r.out, "agents/nope.md does not exist") || !strings.Contains(r.err, "has errors") {
			t.Fatalf("%s: %+v", verb, r)
		}
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written for an invalid rig")
	}
}

func TestLayersAndFlagOrder(t *testing.T) {
	m := newMachine(t)
	layers := t.TempDir()
	put(t, layers, "x/base/rigfile.yaml", "apiVersion: rigfile.dev/v1\nname: x/base\nversion: 1.0.0\ncommands:\n  - {path: commands/hi.md}\n", 0o644)
	put(t, layers, "x/base/commands/hi.md", "hi", 0o644)
	rig := t.TempDir()
	put(t, rig, "rigfile.yaml", "apiVersion: rigfile.dev/v1\nname: x/top\nversion: 1.0.0\nfrom: [x/base]\n", 0o644)
	// flags before AND after the positional argument both work
	for _, args := range [][]string{{"plan", "--layers", layers, rig}, {"plan", rig, "--layers", layers}} {
		r := m.run("", args...)
		if r.code != 0 || !strings.Contains(r.out, "x/base → x/top") || !strings.Contains(r.out, "hi → ~/.claude/commands/hi.md") {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	// without --layers the inherited layer is not available: a clear error
	if r := m.run("", "plan", rig); r.code != 1 || !strings.Contains(r.err, "x/base is not available") {
		t.Fatalf("%+v", r)
	}
}

func TestSecretsAndDoctor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file backend is stubbed on Windows until Stage 3")
	}
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	_ = os.WriteFile(pass, []byte("correct horse battery staple\n"), 0o600)
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	rig := newRig(t)

	// before anything is applied
	if r := m.run("", "doctor"); r.code != 0 || !strings.Contains(r.out, "nothing applied yet") || !strings.Contains(r.out, "encrypted-file backend") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "apply", rig, "--yes"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	// secrets list reads the needs recorded at apply time; the file backend is "unknown" without a prompt
	r := m.run("", "secrets", "list")
	if !strings.Contains(r.out, "alpaca/api_key") || !strings.Contains(r.out, "unknown") || !strings.Contains(r.out, "login   claude-code") {
		t.Fatalf("%+v", r)
	}
	r = m.run("", "doctor")
	if !strings.Contains(r.out, "✔ drift") || !strings.Contains(r.out, "status not checked") || !strings.Contains(r.out, "⚠ login") {
		t.Fatalf("%+v", r)
	}
	// set the secret and check status through the explicit command
	if r := m.run("FAKE-VALUE-123\n", "secrets", "set", "alpaca/api_key"); r.code != 0 || strings.Contains(r.out+r.err, "FAKE-VALUE-123") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "secrets", "status", "alpaca/api_key"); !strings.Contains(r.out, "alpaca/api_key: set") {
		t.Fatalf("%+v", r)
	}
	// drift makes doctor red
	_ = os.Remove(filepath.Join(m.home, ".claude", "agents", "reviewer.md"))
	if r := m.run("", "doctor"); r.code != 1 || !strings.Contains(r.out, "✘ drift") || !strings.Contains(r.out, "agent reviewer (missing)") {
		t.Fatalf("%+v", r)
	}
	// secrets validation and usage
	if r := m.run("v\n", "secrets", "set", "Bad Ref"); r.code == 0 {
		t.Fatal("invalid ref accepted")
	}
	if r := m.run("\n", "secrets", "set", "a/b"); r.code == 0 || !strings.Contains(r.err, "empty") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "secrets", "nonsense"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorFlagsMissingRigfileOnPath(t *testing.T) {
	m := newMachine(t)
	var out, errb bytes.Buffer
	code := run([]string{"doctor"}, env{in: strings.NewReader(""), out: &out, err: &errb, getenv: func(k string) string { return m.env[k] }, mcp: m.mcp, keyringOff: true, tools: m.tools,
		lookPath: func(string) (string, error) { return "", errors.New("nope") }})
	if code != 1 || !strings.Contains(out.String(), "✘ rigfile on PATH") {
		t.Fatalf("code=%d\n%s", code, out.String())
	}
}

func TestHookCommand(t *testing.T) {
	m := newMachine(t)
	in := func(cmd string) string {
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]string{"command": cmd}})
		return string(b)
	}
	for _, args := range [][]string{{"hook", "run", "guard"}, {"hook", "pre-tool-use"}} {
		r := m.run(in("git commit --no-verify -m x"), args...)
		if r.code != 0 || !strings.Contains(r.out, `"permissionDecision":"deny"`) {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	if r := m.run(in("git status"), "hook", "run", "guard"); r.code != 0 || r.out != "" {
		t.Fatalf("%+v", r)
	}
	if r := m.run(`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`, "hook", "run", "guard"); r.code != 0 || r.out != "" {
		t.Fatalf("other events: no opinion: %+v", r)
	}
	if r := m.run("this is not json", "hook", "run", "guard"); r.code != 2 || !strings.Contains(r.err, "blocking") {
		t.Fatalf("malformed input must fail closed: %+v", r)
	}
	if r := m.run("{}", "hook", "run", "nope"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("{}", "hook"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestRollbackListAndUsage(t *testing.T) {
	m := newMachine(t)
	if r := m.run("", "rollback", "--list"); r.code != 0 || !strings.Contains(r.out, "no runs") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "rollback"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
	rig := newRig(t)
	m.run("", "apply", rig, "--yes")
	if r := m.run("", "rollback", "--list"); r.code != 0 || !strings.Contains(r.out, "apply jiaxu/demo@1.0.0") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "rollback", "../etc"); r.code != 1 {
		t.Fatalf("a path-like run id must be rejected: %+v", r)
	}
}

func TestUnknownCommandAndUsage(t *testing.T) {
	m := newMachine(t)
	if r := m.run(""); r.code != 2 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "frobnicate"); r.code != 2 || !strings.Contains(r.err, "unknown command") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "version"); r.code != 0 || !strings.Contains(r.out, "rigfile") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "help"); r.code != 0 || !strings.Contains(r.out, "usage:") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "plan", "a", "b"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

// TestHelperProcess is the child launched by the exec test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("RIGFILE_TEST_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("child sees ALPACA_API_KEY=" + os.Getenv("ALPACA_API_KEY") + " LEAK=" + os.Getenv("SPIKE_PARENT_ONLY") + "\n")
	os.Exit(0)
}

func TestExecInjectsSecretsIntoChildOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file backend is stubbed on Windows until Stage 3")
	}
	t.Setenv("SPIKE_PARENT_ONLY", "should-not-leak")
	m := newMachine(t)
	m.env["SPIKE_PARENT_ONLY"] = "should-not-leak"
	pass := filepath.Join(t.TempDir(), "pass")
	_ = os.WriteFile(pass, []byte("correct horse battery staple\n"), 0o600)
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	if r := m.run("FAKE-EXEC-VALUE\n", "secrets", "set", "alpaca/api_key"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := m.run("", "exec", "--secret", "ALPACA_API_KEY=alpaca/api_key", "--env", "RIGFILE_TEST_HELPER=1", "--", os.Args[0], "-test.run=^TestHelperProcess$")
	if r.code != 0 || !strings.Contains(r.out, "ALPACA_API_KEY=FAKE-EXEC-VALUE") || !strings.Contains(r.out, "LEAK=\n") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "exec", "--secret", "X=no/such", "--", os.Args[0]); r.code == 0 || !strings.Contains(r.err, "rigfile secrets set no/such") {
		t.Fatalf("%+v", r)
	}
	for _, args := range [][]string{{"exec"}, {"exec", "--secret", "bad", "--", "x"}, {"exec", "--env", "=v", "--", "x"}} {
		if r := m.run("", args...); r.code != 2 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}

func TestInitCapturesAndTheResultAppliesCleanly(t *testing.T) {
	src := newMachine(t)
	cd := filepath.Join(src.home, ".claude")
	put(t, cd, "CLAUDE.md", "# Mine\nbe terse\n", 0o644)
	put(t, cd, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: PDFs\n---\nbody\n", 0o644)
	put(t, cd, "agents/reviewer.md", "---\nname: reviewer\ndescription: r\n---\nx\n", 0o644)
	put(t, cd, "settings.json", `{"permissions":{"deny":["Read(~/.ssh/**)"],"ask":["Bash(git push*)"]}}`, 0o644)
	put(t, src.home, ".claude.json", `{"mcpServers":{"alpaca":{"type":"stdio","command":"npx","args":["-y","alpaca-mcp@1.4.2"],"env":{"ALPACA_API_KEY":"sk-ant-TESTTESTTESTTESTTEST"}}}}`, 0o600)
	before := mustRead(t, filepath.Join(cd, "settings.json"))

	out := filepath.Join(t.TempDir(), "rig")
	r := src.run("", "init", "--out", out, "--name", "jia/captured")
	if r.code != 0 || !strings.Contains(r.out, "Captured (") || !strings.Contains(r.out, "Secrets: values NOT captured") || !strings.Contains(r.out, "Next:") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.out+r.err, "TESTTEST") {
		t.Fatal("init printed a secret value")
	}
	if string(mustRead(t, filepath.Join(cd, "settings.json"))) != string(before) {
		t.Fatal("init modified the source config")
	}
	for _, f := range []string{"rigfile.yaml", "instructions/claude-md.md", "skills/pdf/SKILL.md", "agents/reviewer.md"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if strings.Contains(string(mustRead(t, filepath.Join(out, "rigfile.yaml"))), "TESTTEST") {
		t.Fatal("manifest holds a secret")
	}
	// refuses a non-empty target
	if r := src.run("", "init", "--out", out); r.code != 1 || !strings.Contains(r.err, "already has files") {
		t.Fatalf("%+v", r)
	}
	// the captured rig validates and applies on a fresh machine
	dst := newMachine(t)
	if r := dst.run("", "validate", out); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := dst.run("", "apply", out, "--yes"); r.code != 0 || !strings.Contains(r.out, "SECRET NEEDED  alpaca/alpaca_api_key") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(dst.mcp.servers["alpaca"], "ALPACA_API_KEY=alpaca/alpaca_api_key") {
		t.Fatalf("%v", dst.mcp.servers)
	}
	// once applied, re-capturing on that machine leaves what Rigfile manages out
	out2 := filepath.Join(t.TempDir(), "rig2")
	r = dst.run("", "init", "--out", out2)
	y := string(mustRead(t, filepath.Join(out2, "rigfile.yaml")))
	if r.code != 0 || !strings.Contains(r.out, "already managed by Rigfile") || strings.Contains(y, "skills:") || strings.Contains(y, "agents:") {
		t.Fatalf("%+v\n%s", r, y)
	}
}

const toolsRigYAML = `apiVersion: rigfile.dev/v1
name: jiaxu/tools
version: 1.0.0
tools:
  common: [nonsense-tool]
  npm: ['good-mcp@1.4.2', 'loose-mcp']
`

func TestToolsAreShownThenInstalledOnlyAfterApproval(t *testing.T) {
	m := newMachine(t)
	rig := t.TempDir()
	put(t, rig, "rigfile.yaml", toolsRigYAML, 0o644)

	r := m.run("", "plan", rig)
	for _, want := range []string{"TOOLS", "+  npm install -g good-mcp@1.4.2", "unpinned", "?  nonsense-tool: not in the tool catalog"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	if len(m.tools.ran) != 0 {
		t.Fatalf("plan must not install anything: %v", m.tools.ran)
	}
	// declined: nothing runs
	if r := m.run("q\n", "apply", rig); r.code != 1 || len(m.tools.ran) != 0 {
		t.Fatalf("%+v %v", r, m.tools.ran)
	}
	// --no-tools: config only
	if r := m.run("", "apply", rig, "--yes", "--no-tools"); r.code != 0 || len(m.tools.ran) != 0 {
		t.Fatalf("%+v %v", r, m.tools.ran)
	}
	// approved
	r = m.run("a\n", "apply", rig)
	if r.code != 0 || len(m.tools.ran) != 2 || m.tools.ran[0] != "npm install -g good-mcp@1.4.2" || !strings.Contains(r.out, "does not uninstall tools") {
		t.Fatalf("%+v %v", r, m.tools.ran)
	}
}

func TestToolInstallFailureIsReportedAndConfigStillApplies(t *testing.T) {
	m := newMachine(t)
	m.tools.fail = "npm install -g good-mcp@1.4.2"
	rig := newRig(t)
	put(t, rig, "rigfile.yaml", rigYAML+"tools:\n  npm: ['good-mcp@1.4.2']\n", 0o644)
	r := m.run("", "apply", rig, "--yes")
	if r.code != 1 || !strings.Contains(r.err, "tool install failed") || !strings.Contains(r.out, "continuing with the configuration") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "CLAUDE.md")); err != nil {
		t.Fatal("config should still have been applied")
	}
}

func TestApplyEndsWithABatchedSecretsPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file backend is stubbed on Windows until Stage 3")
	}
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	_ = os.WriteFile(pass, []byte("correct horse battery staple\n"), 0o600)
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	rig := newRig(t)

	var asked []string
	var out, errb bytes.Buffer
	call := func(stdin string, args ...string) int {
		out.Reset()
		errb.Reset()
		return run(args, env{in: strings.NewReader(stdin), out: &out, err: &errb, getenv: func(k string) string { return m.env[k] },
			mcp: m.mcp, keyringOff: true, tools: m.tools, interactive: true,
			hidden: func(p string) ([]byte, error) { asked = append(asked, p); return []byte("FAKE-BATCH-VALUE"), nil }})
	}
	// "a" approves the apply, "y" agrees to set the missing secrets
	if code := call("a\ny\n", "apply", rig); code != 0 {
		t.Fatalf("code %d\n%s%s", code, out.String(), errb.String())
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "alpaca/api_key") || !strings.Contains(out.String(), "get it at https://example.test/keys") || !strings.Contains(out.String(), "stored alpaca/api_key") {
		t.Fatalf("asked=%v\n%s", asked, out.String())
	}
	if strings.Contains(out.String()+errb.String(), "FAKE-BATCH-VALUE") {
		t.Fatal("a secret value was printed")
	}
	if r := m.run("", "secrets", "status", "alpaca/api_key"); !strings.Contains(r.out, "alpaca/api_key: set") {
		t.Fatalf("%+v", r)
	}
	// nothing missing any more: a second apply asks nothing
	asked = nil
	if code := call("", "apply", rig); code != 0 || len(asked) != 0 || strings.Contains(out.String(), "not set yet") {
		t.Fatalf("code %d asked %v\n%s", code, asked, out.String())
	}
	// declining is fine and points at the command
	_ = m.run("", "secrets", "rm", "alpaca/api_key")
	if code := call("n\n", "apply", rig); code != 0 || !strings.Contains(out.String(), "skipped; set them later") || len(asked) != 0 {
		t.Fatalf("code %d\n%s", code, out.String())
	}
	// non-interactive runs never prompt
	if r := m.run("", "apply", rig, "--yes"); strings.Contains(r.out, "Set them now") {
		t.Fatalf("%+v", r)
	}
	// the login hint is shown
	if r := m.run("", "plan", rig); !strings.Contains(r.out, "use /login") {
		t.Fatalf("%+v", r)
	}
}

func TestUnwritableRigDirFailsBeforeAnythingIsWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	m := newMachine(t)
	rig := newRig(t)
	if err := os.Chmod(rig, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(rig, 0o755)
	r := m.run("", "apply", rig, "--yes")
	if r.code != 1 || !strings.Contains(r.err, "rig directory must be writable") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("machine was modified even though the lockfile could not be written")
	}
}

func TestJiaRigFixture(t *testing.T) {
	const fx = "../../testdata/fixtures/jia-rig"
	// hygiene: no machine paths, no secret-shaped text, no credential file names
	_ = filepath.WalkDir(fx, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b := mustRead(t, p)
		for _, bad := range []string{"/Users/", "/home/", "snowflake:", "TESTTEST"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s contains %q", p, bad)
			}
		}
		if scan.LooksLikeSecret(string(b)) || scan.IsSensitiveFilename(p) {
			t.Errorf("%s looks sensitive", p)
		}
		return nil
	})

	m := newMachine(t)
	r := m.run("", "validate", fx)
	if r.code != 0 || !strings.Contains(r.err+r.out, "alpaca") || !strings.Contains(r.err+r.out, "not pinned") {
		t.Fatalf("want valid with the unpinned-alpaca warning: %+v", r)
	}
	r = m.run("", "plan", fx)
	for _, want := range []string{"ios-app-store-launch", "commit-push-pr", "alpaca", "Bash(git push*)", "SECRET NEEDED  alpaca/alpaca_api_key"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("plan wrote to the machine")
	}
}

func TestGitProtectionsArePartOfApplyDiffDoctorAndRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git module targets macOS and Linux in Stage 2")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	m := newMachine(t)
	rig := newRig(t)

	r := m.run("", "plan", rig)
	for _, want := range []string{"Git protections (base-secure)", "GIT", "core.hooksPath", "⚠ executes code", "reference-transaction"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	if _, err := os.Stat(filepath.Join(m.home, ".gitconfig")); !os.IsNotExist(err) {
		t.Fatal("plan must not write the git config")
	}

	if r := m.run("", "apply", rig, "--yes"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	conf := string(mustRead(t, filepath.Join(m.home, ".gitconfig")))
	hooks := filepath.Join(m.home, ".config", "rigfile", "git-hooks")
	if !strings.Contains(conf, "hooksPath") || !strings.Contains(conf, hooks) {
		t.Fatalf("global config lacks the hooks path:\n%s", conf)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(m.home, ".config", "git", "ignore"))), "!.env.example") {
		t.Fatal("global gitignore block missing")
	}
	if r := m.run("", "diff"); r.code != 0 || !strings.Contains(r.out, "Git protections") || !strings.Contains(r.out, "no drift") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "doctor"); !strings.Contains(r.out, "git protections") || strings.Contains(r.out, "✘ git protections") {
		t.Fatalf("%+v", r)
	}
	// a hand-edit of a hook shows as drift
	_ = os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if r := m.run("", "diff"); r.code != 1 || !strings.Contains(r.out, "hooks") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "doctor"); r.code != 1 || !strings.Contains(r.out, "✘ git protections") {
		t.Fatalf("%+v", r)
	}
	// rollback removes the git changes together with everything else from that run
	if r := m.run("", "rollback", "--force"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, p := range []string{filepath.Join(m.home, ".gitconfig"), hooks} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone after rollback", p)
		}
	}
}

func TestNoGitFlagSkipsTheGitModule(t *testing.T) {
	m := newMachine(t)
	rig := newRig(t)
	r := m.run("", "plan", rig, "--no-git")
	if strings.Contains(r.out, "Git protections") {
		t.Fatalf("%s", r.out)
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, p := range []string{".gitconfig", ".config/git", ".config/rigfile"} {
		if _, err := os.Stat(filepath.Join(m.home, p)); !os.IsNotExist(err) {
			t.Fatalf("--no-git must not create %s", p)
		}
	}
}

func TestHookWriteGuardAndRedactCommands(t *testing.T) {
	m := newMachine(t)
	tok := "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"
	pre, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Write", "tool_input": map[string]string{"file_path": "a.py", "content": "token = \"" + tok + "\"\n"}})
	r := m.run(string(pre), "hook", "run", "write-guard")
	if r.code != 0 || !strings.Contains(r.out, `"permissionDecision":"deny"`) || strings.Contains(r.out, tok) {
		t.Fatalf("%+v", r)
	}
	clean, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Write", "tool_input": map[string]string{"file_path": "a.py", "content": "print(1)\n"}})
	if r := m.run(string(clean), "hook", "run", "write-guard"); r.code != 0 || r.out != "" {
		t.Fatalf("%+v", r)
	}
	post, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "tool_response": map[string]string{"stdout": "K=" + tok + "\n"}})
	r = m.run(string(post), "hook", "run", "redact")
	if r.code != 0 || !strings.Contains(r.out, "updatedToolOutput") || !strings.Contains(r.out, "[REDACTED:github-pat]") || strings.Contains(r.out, tok) {
		t.Fatalf("%+v", r)
	}
	// wrong event: no opinion
	if r := m.run(string(post), "hook", "run", "write-guard"); r.code != 0 || r.out != "" {
		t.Fatalf("%+v", r)
	}
	if r := m.run(string(pre), "hook", "run", "redact"); r.code != 0 || r.out != "" {
		t.Fatalf("%+v", r)
	}
}

const plainRigYAML = `apiVersion: rigfile.dev/v1
name: jiaxu/plain
version: 1.0.0
commands:
  - {path: commands/hi.md}
`

func plainRig(t *testing.T, extra string) string {
	dir := t.TempDir()
	put(t, dir, "rigfile.yaml", plainRigYAML+extra, 0o644)
	put(t, dir, "commands/hi.md", "hi", 0o644)
	return dir
}

func TestBaseSecureIsAppliedToEveryRig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // the embedded layer is extracted here for the run and must be cleaned up
	m := newMachine(t)
	rig := plainRig(t, "")

	r := m.run("", "plan", rig, "--no-git")
	for _, want := range []string{"rigfile/base-secure → jiaxu/plain", "security-baseline", "Read(~/.ssh/**)", "Bash(git*--no-verify*)", "Bash(git push*)",
		"base-secure-guard", "base-secure-write-guard", "base-secure-redact", "disableBypassPermissionsMode", "⚠ executes code"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "rigfile-base-secure-*")); len(left) != 0 {
		t.Fatalf("the extracted base layer was not cleaned up: %v", left)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("plan wrote to the machine")
	}

	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	settings := string(mustRead(t, filepath.Join(m.home, ".claude", "settings.json")))
	for _, want := range []string{`"Read(~/.ssh/**)"`, `"Bash(git*--no-verify*)"`, `"Bash(git push*)"`, `"Edit(~/.config/rigfile/**)"`,
		`"disableBypassPermissionsMode": "disable"`, `"matcher": "Edit|MultiEdit|NotebookEdit"`, `"PostToolUse"`, `"write-guard"`, `"redact"`, `"guard"`} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings.json missing %s:\n%s", want, settings)
		}
	}
	if md := string(mustRead(t, filepath.Join(m.home, ".claude", "CLAUDE.md"))); !strings.Contains(md, "Security baseline (managed by rigfile/base-secure") {
		t.Fatalf("instructions snippet missing:\n%s", md)
	}
	if r := m.run("", "diff"); r.code != 0 || !strings.Contains(r.out, "no drift") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "doctor"); strings.Contains(r.out, "✘ base-secure") {
		t.Fatalf("%+v", r)
	}
}

func TestBaseSecureCannotBeReplacedOrImpersonatedByARig(t *testing.T) {
	m := newMachine(t)
	// a rig cannot replace a locked base hook
	rig := plainRig(t, "hooks:\n  - {id: base-secure-guard, event: pre_tool_use, match: {tool: bash}, run: 'builtin:guard'}\n")
	if r := m.run("", "plan", rig, "--no-git"); r.code != 1 || !strings.Contains(r.err, "base-secure") {
		t.Fatalf("replacing a locked item must be refused: %+v", r)
	}
	// a rig cannot take the reserved name, and a same-named layer on disk cannot stand in for the embedded one
	bad := t.TempDir()
	put(t, bad, "rigfile.yaml", "apiVersion: rigfile.dev/v1\nname: rigfile/base-secure\nversion: 9.9.9\n", 0o644)
	if r := m.run("", "plan", bad, "--no-git"); r.code != 1 || !strings.Contains(r.err, "reserved") {
		t.Fatalf("%+v", r)
	}
	layers := t.TempDir()
	put(t, layers, "rigfile/base-secure/rigfile.yaml", "apiVersion: rigfile.dev/v1\nname: rigfile/base-secure\nversion: 9.9.9\n", 0o644)
	good := plainRig(t, "")
	r := m.run("", "plan", good, "--layers", layers, "--no-git")
	if r.code != 0 || !strings.Contains(r.out, "Read(~/.ssh/**)") || strings.Contains(r.out, "9.9.9") {
		t.Fatalf("the embedded base must win over a directory layer:\n%+v", r)
	}
	// an allow rule that base-secure denies has no effect and says so (deny always wins)
	rig = plainRig(t, "permissions:\n  allow:\n    - {read: '~/.ssh/**'}\n")
	if r := m.run("", "plan", rig, "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	settings := string(mustRead(t, filepath.Join(m.home, ".claude", "settings.json")))
	var doc struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	_ = json.Unmarshal([]byte(settings), &doc)
	for _, a := range doc.Permissions.Allow {
		if strings.Contains(a, ".ssh") {
			t.Fatalf("an allow shadowed by a base deny must not be written: %v", doc.Permissions.Allow)
		}
	}
}

func TestUnsafeBaseFlagIsLoudRecordedAndReversible(t *testing.T) {
	m := newMachine(t)
	rig := plainRig(t, "")
	r := m.run("", "plan", rig, "--no-git", "--i-understand-unsafe-base")
	if r.code != 0 || !strings.Contains(r.out, "--i-understand-unsafe-base") || !strings.Contains(r.out, "SKIPPED") || strings.Contains(r.out, "Read(~/.ssh/**)") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git", "--i-understand-unsafe-base"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	sd := filepath.Join(m.stateDir(), "state.json")
	if !strings.Contains(string(mustRead(t, sd)), `"unsafeBase"`) {
		t.Fatal("the unsafe apply must be recorded in state.json")
	}
	if r := m.run("", "doctor"); r.code != 1 || !strings.Contains(r.out, "✘ base-secure") || !strings.Contains(r.out, "DISABLED") {
		t.Fatalf("doctor must show it red: %+v", r)
	}
	// a normal apply restores the layer and clears the record (the lockfile changed: the base layer is back)
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 1 || !strings.Contains(r.err, "lockfile") {
		t.Fatalf("restoring the base layer changes the lock, which must be accepted explicitly: %+v", r)
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git", "--update-lock"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(string(mustRead(t, sd)), `"unsafeBase"`) {
		t.Fatal("a normal apply must clear the unsafe record")
	}
	if r := m.run("", "doctor"); strings.Contains(r.out, "✘ base-secure") {
		t.Fatalf("%+v", r)
	}
}

func TestExistingBypassModeSettingIsNeverOverwritten(t *testing.T) {
	m := newMachine(t)
	put(t, filepath.Join(m.home, ".claude"), "settings.json", "{\n  \"permissions\": {\n    \"disableBypassPermissionsMode\": \"other\"\n  }\n}\n", 0o644)
	rig := plainRig(t, "")
	r := m.run("", "apply", rig, "--yes", "--no-git")
	if r.code != 3 || !strings.Contains(r.out, "your settings already say other") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(m.home, ".claude", "settings.json"))), `"other"`) {
		t.Fatal("the user's own value was overwritten")
	}
}

func TestSandboxProfileIsOptInStickyAndNeverOverwritesUserSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sandbox profile is macOS/Linux")
	}
	m := newMachine(t)
	rig := plainRig(t, "")
	settingsPath := filepath.Join(m.home, ".claude", "settings.json")

	// off by default
	if r := m.run("", "plan", rig, "--no-git"); strings.Contains(r.out, "sandbox.") {
		t.Fatalf("the sandbox must be opt-in:\n%s", r.out)
	}
	// opt in
	r := m.run("", "plan", rig, "--no-git", "--sandbox")
	for _, want := range []string{"sandbox.enabled = true", "sandbox.allowUnsandboxedCommands = false", "sandbox.credentials.files", "sandbox.credentials.envVars"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("plan missing %q:\n%s", want, r.out)
		}
	}
	if r := m.run("", "apply", rig, "--yes", "--no-git", "--sandbox"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var doc struct {
		Sandbox struct {
			Enabled                  bool `json:"enabled"`
			AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
			Credentials              struct {
				Files   []struct{ Path, Mode string } `json:"files"`
				EnvVars []struct{ Name, Mode string } `json:"envVars"`
			} `json:"credentials"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(mustRead(t, settingsPath), &doc); err != nil || !doc.Sandbox.Enabled || doc.Sandbox.AllowUnsandboxedCommands || len(doc.Sandbox.Credentials.Files) < 10 || len(doc.Sandbox.Credentials.EnvVars) < 5 {
		t.Fatalf("%v %+v", err, doc)
	}
	for _, f := range doc.Sandbox.Credentials.Files {
		if f.Mode != "deny" {
			t.Fatalf("only deny entries: %+v", f)
		}
	}
	// sticky: a later plain apply keeps it and changes nothing
	if r := m.run("", "plan", rig, "--no-git"); r.code != 0 || !strings.Contains(r.out, "sandbox.enabled") || strings.Contains(r.out, "+ ") && strings.Contains(r.out, "SETTINGS   +") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "diff"); r.code != 0 || !strings.Contains(r.out, "no drift") {
		t.Fatalf("%+v", r)
	}
	// turning it off removes the entry lists and says the scalars stay
	r = m.run("", "apply", rig, "--yes", "--no-git", "--no-sandbox")
	if r.code != 0 || !strings.Contains(r.out, "still in") {
		t.Fatalf("%+v", r)
	}
	after := string(mustRead(t, settingsPath))
	if strings.Contains(after, `"~/.ssh"`) || !strings.Contains(after, `"enabled": true`) {
		t.Fatalf("lists should be gone, scalars should stay:\n%s", after)
	}
}

func TestSandboxDoesNotOverwriteAUserChoice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	m := newMachine(t)
	put(t, filepath.Join(m.home, ".claude"), "settings.json", "{\n  \"sandbox\": {\n    \"enabled\": false\n  }\n}\n", 0o644)
	rig := plainRig(t, "")
	r := m.run("", "apply", rig, "--yes", "--no-git", "--sandbox")
	if r.code != 3 || !strings.Contains(r.out, "your settings already say false") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(m.home, ".claude", "settings.json"))), `"enabled": false`) {
		t.Fatal("the user's own sandbox choice was overwritten")
	}
}

func TestDoctorVerifiesBaseSecureAndFixReappliesTheRig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS/Linux")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	m := newMachine(t)
	rig := plainRig(t, "")
	if r := m.run("", "doctor"); !strings.Contains(r.out, "⚠ base-secure") || !strings.Contains(r.out, "not applied") {
		t.Fatalf("before apply: %+v", r)
	}
	if r := m.run("", "apply", rig, "--yes"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := m.run("", "doctor")
	for _, want := range []string{"✔ base-secure ", "✔ base-secure git", "✔ scanner", "✔ git protections"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("doctor missing %q:\n%s", want, r.out)
		}
	}
	if strings.Contains(r.out, "✘") {
		t.Fatalf("a fresh apply must be all green:\n%s", r.out)
	}

	// someone removes a deny rule from settings.json
	sp := filepath.Join(m.home, ".claude", "settings.json")
	b := mustRead(t, sp)
	if !strings.Contains(string(b), `"Read(~/.ssh/**)",`) {
		t.Fatalf("test assumption: %s", b)
	}
	_ = os.WriteFile(sp, []byte(strings.Replace(string(b), `"Read(~/.ssh/**)",`, "", 1)), 0o644)
	r = m.run("", "doctor")
	if r.code != 1 || !strings.Contains(r.out, "✘ base-secure") || !strings.Contains(r.out, "Read(~/.ssh/**)") || !strings.Contains(r.out, "doctor --fix") {
		t.Fatalf("a missing deny must be named:\n%+v", r)
	}
	// --fix re-applies the recorded rig (the plan is shown; "a" approves)
	r = m.run("a\n", "doctor", "--fix")
	if !strings.Contains(r.out, "re-applying jiaxu/plain") || !strings.Contains(r.out, "applied") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(string(mustRead(t, sp)), `"Read(~/.ssh/**)"`) {
		t.Fatal("--fix did not restore the deny rule")
	}
	if r := m.run("", "doctor"); r.code != 0 || strings.Contains(r.out, "✘") {
		t.Fatalf("%+v", r)
	}

	// hooks switched off, and the git hooks binary gone
	b = mustRead(t, sp)
	_ = os.WriteFile(sp, []byte(strings.Replace(string(b), "{\n", "{\n  \"disableAllHooks\": true,\n", 1)), 0o644)
	if r := m.run("", "doctor"); r.code != 1 || !strings.Contains(r.out, "✘ hooks enabled") || !strings.Contains(r.out, "disableAllHooks") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "doctor", "--fix", "extra"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorGitScansHistoryAndWalksThroughRotation(t *testing.T) {
	g := newGitEnv(t)
	g.write("README.md", "hello\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "--no-verify", "-m", "init")
	m := newMachine(t)

	if r := m.run("", "doctor", "--git", g.dir); r.code != 0 || !strings.Contains(r.out, "no secrets found") {
		t.Fatalf("clean repo: %+v", r)
	}
	g.write("old.py", "token = \""+e2eSecret+"\"\n")
	g.write("still.py", "key = \""+e2eSecret+"\"\n")
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "--no-verify", "-m", "oops")
	os.Remove(filepath.Join(g.dir, "old.py"))
	g.mustGit("add", "-A")
	g.mustGit("commit", "-q", "--no-verify", "-m", "remove old")

	r := m.run("", "doctor", "--git", g.dir)
	if r.code != 1 || !strings.Contains(r.out, "old.py:1") || !strings.Contains(r.out, "removed since") || !strings.Contains(r.out, "still.py:1") || !strings.Contains(r.out, "STILL IN HEAD") {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"ROTATE every credential", "github-pat", "Personal access tokens", "filter-repo", "never rewrites history"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("walkthrough missing %q:\n%s", want, r.out)
		}
	}
	if strings.Contains(r.out, e2eSecret) || strings.Contains(r.out, "wJ4kP9xQm2Rt7V") {
		t.Fatal("doctor --git printed the secret")
	}
	if r := m.run("", "doctor", "--git", g.dir, "--max-commits", "1"); !strings.Contains(r.out, "only the newest 1 commits") {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorBaseCheckIgnoresTheUsersOwnRigContent(t *testing.T) {
	// regression (found by the container E2E): a rig with its own CLAUDE.md sections, skills and MCP servers
	// must not make the base-only integrity check report drift
	if runtime.GOOS == "windows" {
		t.Skip("macOS/Linux")
	}
	m := newMachine(t)
	rig := newRig(t)
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := m.run("", "doctor")
	if !strings.Contains(r.out, "✔ base-secure ") || strings.Contains(r.out, "✘ base-secure") {
		t.Fatalf("%s", r.out)
	}
}
