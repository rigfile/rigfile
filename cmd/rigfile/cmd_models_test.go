package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/models"
	"github.com/rigfile/rigfile/internal/platform"
)

var appleHW = platform.Hardware{OS: "macos", Arch: "arm64", MemoryGB: 16, FreeDiskGB: 300, GPUs: []platform.GPU{{Vendor: "apple", Name: "Apple Silicon", VRAMGB: 16}}}

func runHW(m *machine, hw platform.Hardware, args ...string) result {
	var out, errb bytes.Buffer
	code := runWith(m, &out, &errb, func(e *env) { e.hardware = &hw }, args...)
	return result{code, portable(out.String()), portable(errb.String())}
}

func TestPlanShowsTheModelsSection(t *testing.T) {
	m := newMachine(t)
	rig := plainRig(t, "models:\n  local-coder:\n    role: local-coder\n    serve: {port: 8080, autostart: true}\ngateways:\n  bridge:\n    listen: 127.0.0.1:4000\n    routes: {local-coder: \"http://127.0.0.1:8080/v1\"}\nrouting:\n  default: cloud\n  local_for: [summaries]\n")
	r := runHW(m, appleHW, "plan", rig, "--no-git")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"MODELS", "mlx-community/Qwen3-8B-4bit via mlx-lm 0.31.3", "download 4.6 GB", "pinned to revision 545dc4251c05", "served on 127.0.0.1:8080 (per-user service",
		"needs-bridge codex:", "gateways: are NOT applied", "routing: is NOT translated", "nothing is downloaded until you approve"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q:\n%s", want, r.out)
		}
	}
	// the same rig on a Linux box without a GPU falls back to Ollama, the engine with a documented route
	linux := platform.Hardware{OS: "linux", Arch: "amd64", MemoryGB: 16, FreeDiskGB: 100}
	r = runHW(m, linux, "plan", rig, "--no-git")
	for _, want := range []string{"qwen3:8b via ollama 0.34.4", "supported    codex:", "experimental claude-code:", "not chosen: apple-silicon-16gb-mlx-qwen3-8b-4bit: needs arch arm64, this is amd64"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q:\n%s", want, r.out)
		}
	}
	// a rig without models has no such section, and detects no hardware
	if r := runHW(m, appleHW, "plan", plainRig(t, ""), "--no-git"); strings.Contains(r.out, "MODELS") {
		t.Fatal("no models: no section")
	}
}

func TestModelsListShowsTheChoiceForThisMachine(t *testing.T) {
	m := newMachine(t)
	r := runHW(m, appleHW, "models", "list")
	if r.code != 0 || !strings.Contains(r.out, "This machine: macos/arm64, 16 GB memory") || !strings.Contains(r.out, "local-coder:") || !strings.Contains(r.out, "Qwen3-8B-4bit") {
		t.Fatalf("%+v", r)
	}
	if r := runHW(m, appleHW, "models"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

type fakeOllamaServer struct {
	srv  *httptest.Server
	port int
}

func startFakeOllama(t *testing.T, digest string) *fakeOllamaServer {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprintf(w, `{"models":[{"name":"qwen3:8b","model":"qwen3:8b","digest":"sha256:%s"}]}`, digest+strings.Repeat("0", 64-len(digest)))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return &fakeOllamaServer{srv, n}
}

type modelsFixture struct {
	m       *machine
	rig     string
	ran     [][]string
	agents  [][]string
	agentE  [][]string
	dropped [][]string
}

func newModelsFixture(t *testing.T, port int, extraServe string) *modelsFixture {
	m := newMachine(t)
	m.tools.have["ollama"] = true
	rig := plainRig(t, fmt.Sprintf("models:\n  local-coder:\n    role: local-coder\n    serve: {port: %d%s}\n", port, extraServe))
	return &modelsFixture{m: m, rig: rig}
}

func (f *modelsFixture) run(hw platform.Hardware, args ...string) result {
	var out, errb bytes.Buffer
	code := runWith(f.m, &out, &errb, func(e *env) {
		e.hardware = &hw
		e.lookPath = func(n string) (string, error) { return "/usr/local/bin/" + n, nil }
		e.modelDeps = func(d models.Deps) models.Deps {
			d.LookPath = func(n string) (string, error) { return filepath.Join(f.m.home, "bin", n), nil }
			d.Run = func(_ context.Context, argv, env []string, _ io.Writer) error {
				f.ran = append(f.ran, argv)
				return nil
			}
			d.StartTemp = func(context.Context, []string, []string) (func(), error) { return func() {}, nil }
			d.Act = &okAct{}
			return d
		}
		e.runAgent = func(path string, a, env, drop []string) int {
			f.agents, f.agentE, f.dropped = append(f.agents, append([]string{path}, a...)), append(f.agentE, env), append(f.dropped, drop)
			return 0
		}
	}, args...)
	return result{code, portable(out.String()), portable(errb.String())}
}

type okAct struct{}

func (okAct) Run([]string) (string, error) { return "", nil }

var linuxHW = platform.Hardware{OS: "linux", Arch: "amd64", MemoryGB: 16, FreeDiskGB: 100}

func TestApplyModelsNowPullsChecksAndRecords(t *testing.T) {
	o := startFakeOllama(t, "500a1f067a9f")
	f := newModelsFixture(t, o.port, "")
	// --yes alone never downloads gigabytes
	r := f.run(linuxHW, "apply", f.rig, "--yes", "--no-git")
	if r.code != 0 || !strings.Contains(r.out, "Local models were not downloaded") || len(f.ran) != 0 {
		t.Fatalf("%+v %v", r, f.ran)
	}
	r = f.run(linuxHW, "apply", f.rig, "--yes", "--no-git", "--models", "now")
	if r.code != 0 || !strings.Contains(r.out, "Model local-coder is ready: http://127.0.0.1:"+strconv.Itoa(o.port)+"/v1") {
		t.Fatalf("%+v", r)
	}
	if len(f.ran) != 1 || f.ran[0][1] != "pull" || f.ran[0][2] != "qwen3:8b" {
		t.Fatalf("%v", f.ran)
	}
	// recorded, and the commands that use the record work
	if r := f.run(linuxHW, "models", "url"); r.code != 0 || strings.TrimSpace(r.out) != "http://127.0.0.1:"+strconv.Itoa(o.port)+"/v1" {
		t.Fatalf("%+v", r)
	}
	if r := f.run(linuxHW, "models", "status"); r.code != 0 || !strings.Contains(r.out, "qwen3:8b via ollama") || !strings.Contains(r.out, "answering") {
		t.Fatalf("%+v", r)
	}
	// claude: environment for that process only, a real key is dropped, and the experiment is named
	r = f.run(linuxHW, "models", "run", "claude", "--", "-p", "hi")
	if r.code != 0 || !strings.Contains(r.err, "EXPERIMENTAL") {
		t.Fatalf("%+v", r)
	}
	if got := strings.Join(f.agents[0][1:], " "); got != "--model qwen3:8b -p hi" {
		t.Fatalf("%q", got)
	}
	if strings.Join(f.agentE[0], " ") != "ANTHROPIC_BASE_URL=http://127.0.0.1:"+strconv.Itoa(o.port)+" ANTHROPIC_AUTH_TOKEN=ollama" || strings.Join(f.dropped[0], ",") != "ANTHROPIC_API_KEY" {
		t.Fatalf("%v %v", f.agentE, f.dropped)
	}
	// codex: only on Ollama's default port (anything else was never verified)
	if r := f.run(linuxHW, "models", "run", "codex"); r.code != 1 || !strings.Contains(r.err, "default port 11434") {
		t.Fatalf("%+v", r)
	}
	// remove
	if r := f.run(linuxHW, "models", "rm", "local-coder"); r.code != 0 || !strings.Contains(r.out, "ollama rm qwen3:8b") {
		t.Fatalf("%+v", r)
	}
	if r := f.run(linuxHW, "models", "url", "local-coder"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestModelsRunCodexOnTheDefaultPort(t *testing.T) {
	f := newModelsFixture(t, models.OllamaPort, "")
	rec := models.Records{"local-coder": {Name: "local-coder", Engine: "ollama", Model: "qwen3:8b", Host: "127.0.0.1", Port: models.OllamaPort, API: "http://127.0.0.1:11434/v1"}}
	if err := rec.Save(f.m.stateDir()); err != nil {
		t.Fatal(err)
	}
	old := probe
	probe = func(string) bool { return true }
	defer func() { probe = old }()
	r := f.run(linuxHW, "models", "run", "codex", "--", "exec", "hello")
	if r.code != 0 || strings.Join(f.agents[0][1:], " ") != "--oss --local-provider ollama -m qwen3:8b exec hello" {
		t.Fatalf("%+v %v", r, f.agents)
	}
	if len(f.agentE[0]) != 0 {
		t.Fatal("codex needs no environment")
	}
	// a server that is not answering is said so
	probe = func(string) bool { return false }
	if r := f.run(linuxHW, "models", "run", "codex"); r.code != 1 || !strings.Contains(r.err, "not answering") {
		t.Fatalf("%+v", r)
	}
}

func TestOllamaDigestMismatchIsRefusedAndNotRecorded(t *testing.T) {
	o := startFakeOllama(t, "ffffffffffff") // the server holds a different model than the catalog pinned
	f := newModelsFixture(t, o.port, "")
	r := f.run(linuxHW, "apply", f.rig, "--yes", "--no-git", "--models", "now")
	if r.code != 1 || !strings.Contains(r.err, "not the pinned 500a1f067a9f") {
		t.Fatalf("%+v", r)
	}
	if r := f.run(linuxHW, "models", "status"); !strings.Contains(r.out, "no local models are set up") {
		t.Fatalf("a refused model must not be recorded: %+v", r)
	}
}

func TestModelsSkipLeavesEverythingOut(t *testing.T) {
	f := newModelsFixture(t, 18080, "")
	r := f.run(linuxHW, "plan", f.rig, "--no-git", "--models", "skip")
	if r.code != 0 || strings.Contains(r.out, "MODELS") || strings.Contains(r.out, "ollama") {
		t.Fatalf("--models skip must leave out the section and the engine install:\n%s", r.out)
	}
	r = f.run(linuxHW, "plan", f.rig, "--no-git")
	if !strings.Contains(r.out, "MODELS") {
		t.Fatalf("%s", r.out)
	}
}

func TestMLXModelsAreNotRunnableThroughAgents(t *testing.T) {
	f := newModelsFixture(t, 18081, "")
	rec := models.Records{"local-coder": {Name: "local-coder", Engine: "mlx-lm", Model: "mlx-community/Qwen3-8B-4bit", Host: "127.0.0.1", Port: 8080, API: "http://127.0.0.1:8080/v1"}}
	if err := rec.Save(f.m.stateDir()); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"codex", "claude"} {
		if r := f.run(appleHW, "models", "run", agent); r.code != 1 || !strings.Contains(r.err, "never verified") {
			t.Fatalf("%s: %+v", agent, r)
		}
	}
	// but its URL is available to any OpenAI-compatible client
	if r := f.run(appleHW, "models", "url"); strings.TrimSpace(r.out) != "http://127.0.0.1:8080/v1" {
		t.Fatalf("%+v", r)
	}
}

func TestInitCapturesARunningOllamaModelPinnedByDigest(t *testing.T) {
	src := newMachine(t)
	put(t, filepath.Join(src.home, ".claude"), "CLAUDE.md", "# Mine\n", 0o644)
	out := filepath.Join(t.TempDir(), "rig")
	var buf, errb bytes.Buffer
	code := runWith(src, &buf, &errb, func(e *env) {
		e.detectModels = func() []models.Detected {
			return []models.Detected{{Engine: "ollama", Version: "0.34.4", Model: "qwen3:8b", Digest: "500a1f067a9f", Port: 11434}}
		}
	}, "init", "--out", out, "--name", "jia/captured")
	if code != 0 || !strings.Contains(buf.String(), "qwen3:8b") || !strings.Contains(buf.String(), "only the network port was read") {
		t.Fatalf("%d\n%s\n%s", code, buf.String(), errb.String())
	}
	doc := string(mustRead(t, filepath.Join(out, "rigfile.yaml")))
	for _, want := range []string{"models:\n  qwen3-8b:", "engine: ollama", `engine_version: "0.34.4"`, "digest: 500a1f067a9f", "model: qwen3:8b"} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q:\n%s", want, doc)
		}
	}
	// the captured rig plans, with the digest carried into the plan
	r := runHW(src, linuxHW, "plan", out, "--no-git")
	if r.code != 0 || !strings.Contains(r.out, "qwen3:8b via ollama 0.34.4") {
		t.Fatalf("%+v", r)
	}
	// with no Ollama running there is no models block
	out2 := filepath.Join(t.TempDir(), "rig2")
	if r := src.run("", "init", "--out", out2); r.code != 0 || strings.Contains(string(mustRead(t, filepath.Join(out2, "rigfile.yaml"))), "models:") {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorChecksEachSetUpModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[]}`))
		case "/v1/chat/completions":
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		}
	}))
	defer srv.Close()
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	m := newMachine(t)
	rec := models.Records{"local-coder": {Name: "local-coder", Engine: "ollama", Model: "qwen3:8b", Host: "127.0.0.1", Port: port, API: srv.URL + "/v1"}}
	if err := rec.Save(m.stateDir()); err != nil {
		t.Fatal(err)
	}
	r := m.run("", "doctor")
	for _, want := range []string{"model local-coder: server", "model local-coder: loopback only", "model local-coder: chat", "model local-coder: tool calls", "CHAT ONLY"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q:\n%s", want, r.out)
		}
	}
	// a model whose server is down is a warning line, not a crash
	srv.Close()
	if r := m.run("", "doctor"); !strings.Contains(r.out, "not answering") {
		t.Fatalf("%s", r.out)
	}
}
