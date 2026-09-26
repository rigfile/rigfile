package models

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type recAct struct {
	mu  sync.Mutex
	ran []string
}

func (a *recAct) Run(argv []string) (string, error) {
	a.mu.Lock()
	a.ran = append(a.ran, strings.Join(argv, " "))
	a.mu.Unlock()
	return "", nil
}

func portOf(t *testing.T, srv *httptest.Server) int {
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

func absExe(t *testing.T, name string) string {
	p := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func testDeps(t *testing.T, hf *HF, act *recAct, exes map[string]string) Deps {
	home := t.TempDir()
	goos := map[string]string{"darwin": "darwin", "linux": "linux", "windows": "windows"}[runtime.GOOS]
	if goos == "" {
		goos = "linux"
	}
	return Deps{
		HF: hf, Act: act, GOOS: goos, Home: home, UID: "501", StateDir: filepath.Join(home, "state"), CacheRoot: filepath.Join(home, "hub"), BackupRoot: filepath.Join(home, "backups"),
		LookPath: func(n string) (string, error) {
			if p, ok := exes[n]; ok {
				return p, nil
			}
			return "", os.ErrNotExist
		},
		Run: func(context.Context, []string, []string, io.Writer) error { return nil },
	}
}

func mlxPlan(port int, autostart bool) *Plan {
	v := Variant{Engine: "mlx-lm", EngineVersion: "0.31.3", Model: "mlx-community/Qwen3-8B-4bit", Revision: rev, WeightsFormat: "mlx-safetensors",
		Args: map[string]any{"prompt-cache-size": 1, "prompt-cache-bytes": 1073741824, "verbose": true, "quiet": false}}
	return &Plan{Name: "local-coder", Chosen: &v, Serve: Serve{Host: "127.0.0.1", Port: port, Autostart: autostart}, DownloadBytes: 1000}
}

func TestSetupMLXDownloadsVerifiesAndInstallsTheService(t *testing.T) {
	f := newFakeHF(t, goodRepo())
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":[]}`) }))
	defer api.Close()
	act := &recAct{}
	exe := absExe(t, "mlx_lm.server")
	d := testDeps(t, &HF{Base: f.srv.URL, RepoPrefix: "/repo"}, act, map[string]string{"mlx_lm.server": exe})
	p := mlxPlan(portOf(t, api), true)
	rec, err := Setup(context.Background(), p, d)
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(d.CacheRoot, "models--mlx-community--Qwen3-8B-4bit", "snapshots", rev)
	if rec.Dir != snap || rec.Service != "model-local-coder" || !strings.HasSuffix(rec.API, "/v1") {
		t.Fatalf("%+v", rec)
	}
	if b, _ := os.ReadFile(filepath.Join(snap, "model.safetensors")); len(b) == 0 {
		t.Fatal("the weights were not downloaded into the snapshot directory")
	}
	// the service runs exactly what the plan says, on loopback, against the verified local snapshot
	cmd, args, env := ServeCommand(p, exe, snap)
	if cmd != exe || env != nil {
		t.Fatal(cmd, env)
	}
	want := "--model " + snap + " --host 127.0.0.1 --port " + strconv.Itoa(portOf(t, api)) + " --prompt-cache-bytes 1073741824 --prompt-cache-size 1 --verbose"
	if strings.Join(args, " ") != want {
		t.Fatalf("\n got %q\nwant %q", strings.Join(args, " "), want)
	}
	if len(act.ran) == 0 || !strings.Contains(strings.Join(act.ran, "|"), "model-local-coder") {
		t.Fatalf("the service manager was not asked to start it: %v", act.ran)
	}
	unit := filepath.Join(d.Home, ".config", "systemd", "user", "rigfile-model-local-coder.service")
	if d.GOOS == "linux" {
		b, err := os.ReadFile(unit)
		if err != nil || !strings.Contains(string(b), "--host 127.0.0.1") || strings.Contains(string(b), "0.0.0.0") {
			t.Fatalf("%v %s", err, b)
		}
	}
	// removing takes the snapshot and the service away
	if notes := Remove(context.Background(), *rec, d); len(notes) != 0 {
		t.Fatal(notes)
	}
	if _, err := os.Stat(snap); err == nil {
		t.Fatal("the snapshot must be removed")
	}
}

func TestSetupMLXRefusesWhatFailsVerificationAndInstallsNothing(t *testing.T) {
	f := newFakeHF(t, goodRepo())
	f.tamper["model.safetensors"] = []byte(strings.Repeat("WEIGHTS!", 5000))
	act := &recAct{}
	d := testDeps(t, &HF{Base: f.srv.URL, RepoPrefix: "/repo"}, act, map[string]string{"mlx_lm.server": absExe(t, "mlx_lm.server")})
	if _, err := Setup(context.Background(), mlxPlan(8080, true), d); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("%v", err)
	}
	if len(act.ran) != 0 {
		t.Fatalf("no service may be installed for a model that failed verification: %v", act.ran)
	}
	// the engine must be installed first
	d.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if _, err := Setup(context.Background(), mlxPlan(8080, false), d); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
	// an unsafe plan is refused even if it reaches Setup
	bad := mlxPlan(8080, false)
	bad.Chosen.Args = map[string]any{"trust-remote-code": true}
	if _, err := Setup(context.Background(), bad, d); err == nil || !strings.Contains(err.Error(), "never allowed") {
		t.Fatalf("%v", err)
	}
	if _, err := Setup(context.Background(), &Plan{Name: "x", Blocked: "nothing fits"}, d); err == nil || !strings.Contains(err.Error(), "cannot be applied") {
		t.Fatalf("%v", err)
	}
}

type fakeOllama struct {
	srv    *httptest.Server
	mu     sync.Mutex
	up     bool
	models []map[string]string
}

func newFakeOllama(t *testing.T, up bool) *fakeOllama {
	f := &fakeOllama{up: up}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.up || r.URL.Path != "/api/tags" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"models": f.models})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func ollamaPlan(port int, digest string, autostart bool) *Plan {
	v := Variant{Engine: "ollama", EngineVersion: "0.34.4", Model: "qwen3:8b", Digest: digest}
	return &Plan{Name: "local-coder", Chosen: &v, Serve: Serve{Host: "127.0.0.1", Port: port, Autostart: autostart}}
}

func TestSetupOllamaPullsAndChecksTheDigest(t *testing.T) {
	o := newFakeOllama(t, false)
	act := &recAct{}
	d := testDeps(t, nil, act, map[string]string{"ollama": absExe(t, "ollama")})
	var ran [][]string
	var envs [][]string
	d.Run = func(_ context.Context, argv, env []string, _ io.Writer) error {
		ran, envs = append(ran, argv), append(envs, env)
		o.mu.Lock()
		o.models = []map[string]string{{"name": "qwen3:8b", "model": "qwen3:8b", "digest": "sha256:500a1f067a9f" + strings.Repeat("0", 52)}}
		o.mu.Unlock()
		return nil
	}
	started, stopped := false, false
	d.StartTemp = func(_ context.Context, argv, env []string) (func(), error) {
		started = true
		o.mu.Lock()
		o.up = true
		o.mu.Unlock()
		return func() { stopped = true }, nil
	}
	port := portOf(t, o.srv)
	rec, err := Setup(context.Background(), ollamaPlan(port, "500a1f067a9f", false), d)
	if err != nil {
		t.Fatal(err)
	}
	if !started || !stopped {
		t.Fatal("without a service the server runs only while the model is pulled")
	}
	if len(ran) != 1 || ran[0][1] != "pull" || ran[0][2] != "qwen3:8b" || len(envs[0]) != 1 || envs[0][0] != "OLLAMA_HOST=127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("%v %v", ran, envs)
	}
	if rec.Digest != "500a1f067a9f" || rec.Service != "" || len(act.ran) != 0 {
		t.Fatalf("%+v %v", rec, act.ran)
	}
	// a different model behind the tag is refused, loudly
	if _, err := Setup(context.Background(), ollamaPlan(port, "deadbeef0000", false), d); err == nil || !strings.Contains(err.Error(), "not the pinned deadbeef0000") || !strings.Contains(err.Error(), "ollama rm qwen3:8b") {
		t.Fatalf("%v", err)
	}
	// no pinned digest: allowed, with a warning printed
	var out strings.Builder
	d.Out = &out
	if _, err := Setup(context.Background(), ollamaPlan(port, "", false), d); err != nil || !strings.Contains(out.String(), "not pinned to a digest") {
		t.Fatalf("%v %s", err, out.String())
	}
}

func TestSetupOllamaWithAServiceSetsLoopbackHostInTheUnit(t *testing.T) {
	o := newFakeOllama(t, true)
	o.models = []map[string]string{{"name": "qwen3:8b", "digest": "sha256:500a1f067a9f" + strings.Repeat("0", 52)}}
	act := &recAct{}
	d := testDeps(t, nil, act, map[string]string{"ollama": absExe(t, "ollama")})
	rec, err := Setup(context.Background(), ollamaPlan(portOf(t, o.srv), "500a1f067a9f", true), d)
	if err != nil || rec.Service != "model-local-coder" {
		t.Fatalf("%v %+v", err, rec)
	}
	if d.GOOS == "linux" {
		b, _ := os.ReadFile(filepath.Join(d.Home, ".config", "systemd", "user", "rigfile-model-local-coder.service"))
		if !strings.Contains(string(b), `Environment="OLLAMA_HOST=127.0.0.1:`) || !strings.Contains(string(b), "ollama") {
			t.Fatalf("%s", b)
		}
	}
	if notes := Remove(context.Background(), *rec, d); len(notes) != 1 || !strings.Contains(notes[0], "ollama rm qwen3:8b") {
		t.Fatalf("%v", notes)
	}
}
