// Package adaptertest is the shared fixture for every adapter's tests: one canonical rig (all categories), a
// temp home, projection for a target on a chosen OS, applying a plan through the journaled writer, and golden
// files compared per OS (RIGFILE_PLAN.md §9.3).
package adaptertest

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rigfile/rigfile/internal/apply"
	"github.com/rigfile/rigfile/internal/engine"
	"github.com/rigfile/rigfile/internal/layers"
	"github.com/rigfile/rigfile/internal/manifest"
	"github.com/rigfile/rigfile/internal/merge"
	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/state"
)

// UpdateGolden regenerates golden files (`go test ./internal/adapters/... -update-golden`).
var UpdateGolden = flag.Bool("update-golden", false, "rewrite golden files")

// RigYAML is the canonical fixture rig: every category, with a secret-bearing MCP server and a remote one.
const RigYAML = `apiVersion: rigfile.dev/v1
name: jiaxu/demo
version: 1.0.0
instructions:
  - {id: coding-style, file: instructions/style.md}
skills:
  - {path: skills/pdf}
agents:
  - {path: agents/reviewer.md}
commands:
  - {path: commands/ship.md}
hooks:
  - {id: guard, event: pre_tool_use, match: {tool: bash}, run: 'builtin:guard'}
mcp_servers:
  alpaca:
    command: npx
    args: ['-y', 'alpaca-mcp@1.4.2']
    env: {ALPACA_API_KEY: 'secret://alpaca/api_key', ALPACA_PAPER: 'true'}
  docs:
    transport: http
    url: https://mcp.example.test/mcp
    auth: oauth
permissions:
  deny: [{read: '~/.ssh/**'}, {read: '**/.env'}]
  ask: [{bash: 'git push*'}]
secrets:
  alpaca/api_key: {description: key}
`

// Files is the canonical fixture's files, by rig-relative path.
var Files = map[string]string{
	"rigfile.yaml":              RigYAML,
	"instructions/style.md":     "# Style\n- be terse\n",
	"skills/pdf/SKILL.md":       "---\nname: pdf\ndescription: Work with PDFs\n---\nUse pdftotext.\n",
	"skills/pdf/scripts/run.sh": "#!/bin/sh\necho pdf\n",
	"agents/reviewer.md":        "---\nname: reviewer\ndescription: Reviews diffs\ntools: Read, Grep\n---\nBe strict.\n",
	"commands/ship.md":          "---\ndescription: Ship it\n---\nRun the tests, then push.\n",
}

// Rig is a fixture rig on disk plus a temp home.
type Rig struct {
	T    *testing.T
	Dir  string
	Home string
}

// New writes the canonical rig (with overrides: a value "\x00delete" removes a file) and a temp home.
func New(t *testing.T, override map[string]string) *Rig {
	t.Helper()
	r := &Rig{T: t, Dir: t.TempDir(), Home: t.TempDir()}
	files := map[string]string{}
	for k, v := range Files {
		files[k] = v
	}
	for k, v := range override {
		if v == "\x00delete" {
			delete(files, k)
		} else {
			files[k] = v
		}
	}
	for rel, c := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		r.Put(r.Dir, rel, c, mode)
	}
	return r
}

// Put writes a file (creating directories).
func (r *Rig) Put(root, rel, content string, mode os.FileMode) {
	r.T.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		r.T.Fatal(err)
	}
}

// Plat builds platform info for a GOOS ("macos"/"linux"/"windows" or a Go name) with the temp home injected.
func (r *Rig) Plat(goos string) *platform.Info {
	if goos == "macos" {
		goos = "darwin"
	}
	pi, err := platform.New(platform.Options{GOOS: goos, GOARCH: "arm64", Getenv: func(k string) string {
		switch k {
		case "HOME", "USERPROFILE":
			return r.Home
		case "APPDATA":
			return filepath.Join(r.Home, "AppData", "Roaming")
		case "LOCALAPPDATA":
			return filepath.Join(r.Home, "AppData", "Local")
		}
		return ""
	}})
	if err != nil {
		r.T.Fatal(err)
	}
	return pi
}

// Projection resolves, merges and projects the rig for target on pi's OS (no base-secure layer).
func (r *Rig) Projection(target string, pi *platform.Info) *merge.Projection {
	r.T.Helper()
	top, err := manifest.Load(r.Dir)
	if err != nil {
		r.T.Fatal(err)
	}
	if ps := manifest.Check(top); manifest.HasErrors(ps) {
		r.T.Fatalf("rig has errors: %v", ps)
	}
	res, err := layers.Resolve(top, layers.DirSource{})
	if err != nil {
		r.T.Fatal(err)
	}
	m, err := merge.Merge(res.Layers, target)
	if err != nil {
		r.T.Fatal(err)
	}
	return m.Project(string(pi.OS), target)
}

// Apply executes a plan through a fresh journaled writer into st and returns the run id.
func (r *Rig) Apply(p *engine.Plan, st *state.State, target string) string {
	r.T.Helper()
	w := &apply.Writer{BackupRoot: filepath.Join(r.Home, ".rigfile", "backups"), Now: func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }}
	if err := p.Apply(&engine.Exec{W: w}, st.Target(target)); err != nil {
		r.T.Fatalf("apply: %v", err)
	}
	id, err := w.Commit("test")
	if err != nil {
		r.T.Fatal(err)
	}
	return id
}

// Read returns a file under the home ("" if missing).
func (r *Rig) Read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(r.Home, filepath.FromSlash(rel)))
	return string(b)
}

// Tree lists every regular file under dir (relative to the home) with its content, for golden comparison.
func (r *Rig) Tree(rels ...string) map[string]string {
	out := map[string]string{}
	for _, rel := range rels {
		root := filepath.Join(r.Home, filepath.FromSlash(rel))
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			b, _ := os.ReadFile(p)
			rp, _ := filepath.Rel(r.Home, p)
			out[filepath.ToSlash(rp)] = string(b)
			return nil
		})
	}
	return out
}

// Golden compares files (path → content) with testdata/golden/<name>/<path>; with -update-golden it rewrites them.
// A file missing on either side is a failure.
func Golden(t *testing.T, name string, got map[string]string) {
	t.Helper()
	root := filepath.Join("testdata", "golden", name)
	if *UpdateGolden {
		if !wiped[name] { // several per-OS subtests share one golden directory: clear it once
			wiped[name] = true
			_ = os.RemoveAll(root)
		}
		for rel, c := range got {
			p := filepath.Join(root, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	want := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		rp, _ := filepath.Rel(root, p)
		want[filepath.ToSlash(rp)] = string(b)
		return nil
	})
	var names []string
	for k := range got {
		names = append(names, k)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		g, gok := got[k]
		w, wok := want[k]
		switch {
		case !gok:
			t.Errorf("golden %s: %s is expected but was not written", name, k)
		case !wok:
			t.Errorf("golden %s: %s was written but has no golden file (run with -update-golden and review)", name, k)
		case g != w:
			t.Errorf("golden %s: %s differs\n--- got ---\n%s\n--- want ---\n%s", name, k, g, w)
		}
	}
}

var wiped = map[string]bool{}

// OSes are the three operating systems every adapter is tested on.
var OSes = []string{"macos", "linux", "windows"}

// HostIsWindows reports whether the tests run on native Windows (a few path assertions differ there).
func HostIsWindows() bool { return runtime.GOOS == "windows" }

// AssertCanonicalMCP checks that a captured manifest describes the fixture's servers: `alpaca` (stdio, npx,
// secret ref, literal env) and, when remote is true, `docs` (https). It is the "capture(apply(x)) == x" check for
// the MCP category, shared by every adapter.
func AssertCanonicalMCP(t *testing.T, m *manifest.Manifest, remote bool) {
	t.Helper()
	a, ok := m.MCPServers["alpaca"]
	if !ok || a.Command != "npx" || strings.Join(a.Args, " ") != "-y alpaca-mcp@1.4.2" || a.Env["ALPACA_API_KEY"] != "secret://alpaca/api_key" || a.Env["ALPACA_PAPER"] != "true" {
		t.Fatalf("alpaca did not round-trip: %+v", a)
	}
	if _, ok := m.Secrets["alpaca/api_key"]; !ok {
		t.Fatalf("the secret declaration was lost: %v", m.Secrets)
	}
	if remote {
		if d, ok := m.MCPServers["docs"]; !ok || d.URL != "https://mcp.example.test/mcp" || d.Transport != "http" {
			t.Fatalf("docs did not round-trip: %+v", d)
		}
	}
}

// ReadRig returns a captured rig's file (rig-relative path) as text.
func ReadRig(files map[string][]byte, rel string) string { return string(files[rel]) }
