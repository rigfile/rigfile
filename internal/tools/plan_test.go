package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type fakeHost struct {
	have map[string]bool
	ran  [][]string
	fail string // argv[0]+" "+argv[1] that fails
	// installing a command makes it appear (so Verify passes)
	installs map[string]string
}

func (f *fakeHost) Has(c string) bool { return f.have[c] }
func (f *fakeHost) Run(_ context.Context, argv []string, out io.Writer) error {
	f.ran = append(f.ran, argv)
	if f.fail != "" && strings.HasPrefix(strings.Join(argv, " "), f.fail) {
		return errors.New("exit status 1")
	}
	if c, ok := f.installs[argv[len(argv)-1]]; ok {
		f.have[c] = true
	}
	io.WriteString(out, "ok\n")
	return nil
}

func host(cmds ...string) *fakeHost {
	h := &fakeHost{have: map[string]bool{}, installs: map[string]string{}}
	for _, c := range cmds {
		h.have[c] = true
	}
	return h
}

func cat(t *testing.T) *Catalog {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogLoads(t *testing.T) {
	c := cat(t)
	for _, n := range []string{"gh", "uv", "jq", "git", "node", "gitleaks"} {
		e, ok := c.Tools[n]
		if !ok || e.Detect.Command == "" || len(e.Install["macos"]) == 0 {
			t.Errorf("catalog entry %s incomplete: %+v", n, e)
		}
	}
}

func TestMacOSBrewToolsAreRunnableAfterApproval(t *testing.T) {
	p := Build(cat(t), Input{"common": {"jq", "gitleaks", "git"}}, "macos", host("brew", "git"))
	if len(p.Present) != 1 || p.Present[0] != "git" {
		t.Fatalf("present: %v", p.Present)
	}
	if len(p.Steps) != 2 || len(p.Runnable()) != 2 || p.Steps[0].Command() != "brew install jq" {
		t.Fatalf("%+v", p.Steps)
	}
	if p.Steps[0].Manual {
		t.Fatal()
	}
}

func TestBuildDoesNotRunAnything(t *testing.T) {
	h := host("brew")
	Build(cat(t), Input{"common": {"jq"}, "npm": {"x@1.0.0"}}, "macos", h)
	if len(h.ran) != 0 {
		t.Fatalf("planning must not execute: %v", h.ran)
	}
}

func TestLinuxRootManagersArePrintedNeverRun(t *testing.T) {
	h := host("apt-get")
	p := Build(cat(t), Input{"common": {"jq"}}, "linux", h)
	if len(p.Steps) != 1 || !p.Steps[0].Manual || p.Steps[0].Command() != "sudo apt-get install -y jq" || !strings.Contains(p.Steps[0].Why, "root") {
		t.Fatalf("%+v", p.Steps)
	}
	res := Execute(context.Background(), p, h, io.Discard)
	if len(h.ran) != 0 || len(res.Ran) != 0 {
		t.Fatalf("manual steps must never run: %v", h.ran)
	}
}

func TestLinuxPicksTheManagerThatExists(t *testing.T) {
	p := Build(cat(t), Input{"common": {"jq"}}, "linux", host("dnf"))
	if len(p.Steps) != 1 || p.Steps[0].Manager != "dnf" {
		t.Fatalf("%+v", p)
	}
	p = Build(cat(t), Input{"common": {"jq"}}, "linux", host())
	if len(p.Steps) != 0 || len(p.Unresolved) != 1 || !strings.Contains(p.Unresolved[0], "no supported package manager") {
		t.Fatalf("%+v", p)
	}
}

func TestRepoSetupEntriesAreNeverAutoSelected(t *testing.T) {
	// gh on apt has a plain (lagging) entry and a repo_setup one; only the plain one may be chosen
	p := Build(cat(t), Input{"common": {"gh"}}, "linux", host("apt-get"))
	if len(p.Steps) != 1 || p.Steps[0].Package != "gh" || p.Steps[0].Manager != "apt" {
		t.Fatalf("%+v", p)
	}
}

func TestUnknownToolAndMissingBrew(t *testing.T) {
	p := Build(cat(t), Input{"common": {"nonsense", "jq"}}, "macos", host())
	if len(p.Unresolved) != 2 || !strings.Contains(p.Unresolved[0], "not in the tool catalog") || !strings.Contains(p.Unresolved[1], "brew") {
		t.Fatalf("%+v", p.Unresolved)
	}
}

func TestLanguageManagersFlagUnpinned(t *testing.T) {
	p := Build(cat(t), Input{"npm": {"good-mcp@1.4.2", "loose-mcp"}, "pipx": {"tool==2.0"}, "uv": {"other"}}, "macos", host("npm", "pipx", "uv"))
	byPkg := map[string]Step{}
	for _, s := range p.Steps {
		byPkg[s.Package] = s
	}
	if byPkg["good-mcp@1.4.2"].Warn != "" || byPkg["tool==2.0"].Warn != "" {
		t.Fatalf("pinned entries must not be flagged: %+v", byPkg)
	}
	if !strings.Contains(byPkg["loose-mcp"].Warn, "unpinned") || !strings.Contains(byPkg["other"].Warn, "unpinned") {
		t.Fatalf("%+v", byPkg)
	}
	if byPkg["good-mcp@1.4.2"].Command() != "npm install -g good-mcp@1.4.2" || byPkg["other"].Command() != "uv tool install other" {
		t.Fatalf("%+v", byPkg)
	}
	// a language manager that is not installed becomes a manual step
	p = Build(cat(t), Input{"pipx": {"x==1"}}, "macos", host())
	if !p.Steps[0].Manual || !strings.Contains(p.Steps[0].Why, "not installed") {
		t.Fatalf("%+v", p.Steps)
	}
}

func TestEscapeHatchesAreOSScoped(t *testing.T) {
	in := Input{"macos.brew": {"wget"}, "macos.cask": {"iterm2"}, "linux.apt": {"curl"}, "windows.winget": {"Foo.Bar"}}
	mac := Build(cat(t), in, "macos", host("brew"))
	if len(mac.Steps) != 2 || mac.Steps[1].Command() != "brew install --cask iterm2" {
		t.Fatalf("%+v", mac.Steps)
	}
	lin := Build(cat(t), in, "linux", host())
	if len(lin.Steps) != 1 || lin.Steps[0].Manager != "apt" || !lin.Steps[0].Manual {
		t.Fatalf("%+v", lin.Steps)
	}
	win := Build(cat(t), in, "windows", host())
	if len(win.Steps) != 1 || !win.Steps[0].Manual || !strings.Contains(win.Steps[0].Why, "Stage 3") {
		t.Fatalf("%+v", win.Steps)
	}
}

func TestExecuteRunsInOrderStopsOnFailureAndVerifies(t *testing.T) {
	c := cat(t)
	h := host("brew")
	h.installs["jq"] = "jq"
	p := Build(c, Input{"common": {"jq"}}, "macos", h)
	var out bytes.Buffer
	if r := Execute(context.Background(), p, h, &out); len(r.Failed) != 0 || len(r.Ran) != 1 || !strings.Contains(out.String(), "$ brew install jq") {
		t.Fatalf("%+v\n%s", r, out.String())
	}

	// failure stops the run
	h = host("brew")
	h.fail = "brew install jq"
	p = Build(c, Input{"common": {"jq", "gitleaks"}}, "macos", h)
	r := Execute(context.Background(), p, h, io.Discard)
	if len(r.Failed) != 1 || len(h.ran) != 1 {
		t.Fatalf("%+v %v", r, h.ran)
	}

	// installed but not on PATH afterwards
	h = host("brew")
	p = Build(c, Input{"common": {"jq"}}, "macos", h)
	r = Execute(context.Background(), p, h, io.Discard)
	if len(r.Failed) != 1 || !strings.Contains(r.Failed[0], "still not on PATH") {
		t.Fatalf("%+v", r)
	}
}

func TestDuplicatesCollapse(t *testing.T) {
	p := Build(cat(t), Input{"npm": {"a@1.0.0", "a@1.0.0"}}, "macos", host("npm"))
	if len(p.Steps) != 1 {
		t.Fatalf("%+v", p.Steps)
	}
}
