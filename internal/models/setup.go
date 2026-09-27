package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rigfile/rigfile/internal/svc"
)

// Deps is everything Setup touches outside this package; tests replace each piece.
type Deps struct {
	HF        *HF
	LookPath  func(string) (string, error)
	Run       func(ctx context.Context, argv []string, env []string, out io.Writer) error // runs a command to completion
	StartTemp func(ctx context.Context, argv []string, env []string) (stop func(), err error)
	Act       svc.Activator
	HTTP      *http.Client

	GOOS, Home, UID string
	StateDir        string
	CacheRoot       string // Hugging Face hub cache, e.g. ~/.cache/huggingface/hub
	BackupRoot      string
	Out             io.Writer
	WaitFor         time.Duration // how long to wait for a server to answer (default 60 s)
}

// Installed records what Setup put on the machine, so `models rm` can take it away again.
type Installed struct {
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Model    string `json:"model"`
	Revision string `json:"revision,omitempty"`
	Digest   string `json:"digest,omitempty"`
	Dir      string `json:"dir,omitempty"`     // mlx-lm: the snapshot directory
	Service  string `json:"service,omitempty"` // the service name, when one was installed
	Host     string `json:"host"`
	Port     int    `json:"port"`
	API      string `json:"api"` // base URL of the OpenAI-style API

	// How to start it by hand (`rigfile models serve`).
	Exe  string            `json:"exe,omitempty"`
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

func (d Deps) out() io.Writer {
	if d.Out == nil {
		return io.Discard
	}
	return d.Out
}

func (d Deps) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// snapshotDir is where a Hugging Face model lives: the standard hub cache layout.
func (d Deps) snapshotDir(model, rev string) string {
	return filepath.Join(d.CacheRoot, "models--"+strings.ReplaceAll(model, "/", "--"), "snapshots", rev)
}

func endpoint(host string, port int) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// Setup downloads (verified) and prepares one planned model. Nothing is started unless the plan asks for a service.
func Setup(ctx context.Context, p *Plan, d Deps) (*Installed, error) {
	if p.Chosen == nil {
		return nil, fmt.Errorf("model %s cannot be applied: %s", p.Name, p.Blocked)
	}
	if err := Validate(*p.Chosen); err != nil {
		return nil, err // never trust a plan that was not validated
	}
	if !IsLoopbackHost(p.Serve.Host) {
		return nil, errors.New("model servers listen on loopback only")
	}
	v := *p.Chosen
	rec := &Installed{Name: p.Name, Engine: v.Engine, Model: v.Model, Revision: v.Revision, Digest: v.Digest, Host: p.Serve.Host, Port: p.Serve.Port, API: endpoint(p.Serve.Host, p.Serve.Port) + "/v1"}
	switch v.Engine {
	case "mlx-lm":
		return rec, setupMLX(ctx, p, d, rec)
	case "ollama":
		return rec, setupOllama(ctx, p, d, rec)
	}
	return nil, fmt.Errorf("the %s engine is not supported", v.Engine)
}

func (d Deps) look(name string) (string, error) {
	if d.LookPath == nil {
		return "", errors.New("no way to find programs")
	}
	p, err := d.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not installed (rigfile apply installs the engine; or install it yourself)", name)
	}
	return p, nil
}

// ServeCommand is the exact command that serves the plan: program, arguments and environment. It is used by the service
// generator, by `rigfile models serve`, and shown on the plan screen.
func ServeCommand(p *Plan, exe, snapshot string) (string, []string, map[string]string) {
	v := *p.Chosen
	host, port := p.Serve.Host, strconv.Itoa(p.Serve.Port)
	switch v.Engine {
	case "ollama":
		return exe, []string{"serve"}, map[string]string{"OLLAMA_HOST": net.JoinHostPort(host, port)}
	}
	args := []string{"--model", snapshot, "--host", host, "--port", port}
	keys := make([]string, 0, len(v.Args))
	for k := range v.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch val := v.Args[k].(type) {
		case bool:
			if val {
				args = append(args, "--"+k)
			}
		default:
			args = append(args, "--"+k, fmt.Sprint(val))
		}
	}
	return exe, args, nil
}

func (d Deps) service(name, desc, exe string, args []string, env map[string]string) *svc.Service {
	return &svc.Service{
		Spec: svc.Spec{GOOS: d.GOOS, Home: d.Home, UID: d.UID, StateDir: d.StateDir, Name: "model-" + serviceName(name), Description: desc, Exe: exe, Args: args, Env: env,
			LogPath: filepath.Join(d.StateDir, "models", serviceName(name)+".log"), TaskFile: filepath.Join(d.StateDir, "models", serviceName(name)+"-task.xml")},
		Act: d.Act, BackupRoot: d.BackupRoot, InstallNote: "install model service " + name, UninstallNote: "remove model service " + name,
	}
}

func setupMLX(ctx context.Context, p *Plan, d Deps, rec *Installed) error {
	v := *p.Chosen
	exe, err := d.look("mlx_lm.server")
	if err != nil {
		return err
	}
	snap := d.snapshotDir(v.Model, v.Revision)
	fmt.Fprintf(d.out(), "downloading %s at %s (%s), verifying every file...\n", v.Model, v.Revision[:12], HumanBytes(p.DownloadBytes))
	last := time.Now()
	hf := d.HF
	if hf == nil {
		hf = &HF{}
	}
	if _, err := hf.Fetch(ctx, v.Model, v.Revision, v.WeightsFormat, snap, func(done, total int64) {
		if time.Since(last) > 2*time.Second {
			last = time.Now()
			fmt.Fprintf(d.out(), "  %s of %s\n", HumanBytes(done), HumanBytes(total))
		}
	}); err != nil {
		return fmt.Errorf("download of %s failed: %w", v.Model, err)
	}
	rec.Dir = snap
	rec.Exe, rec.Args, rec.Env = ServeCommand(p, exe, snap)
	if p.Serve.Autostart {
		cmd, args, env := ServeCommand(p, exe, snap)
		sv := d.service(p.Name, "Rigfile local model server ("+p.Name+")", cmd, args, env)
		if _, err := sv.Install(); err != nil {
			return err
		}
		rec.Service = "model-" + serviceName(p.Name)
		if err := d.waitHealthy(ctx, rec.API+"/models"); err != nil {
			return fmt.Errorf("the service started but the server did not answer: %w (log: %s)", err, filepath.Join(d.StateDir, "models", serviceName(p.Name)+".log"))
		}
	}
	return nil
}

func setupOllama(ctx context.Context, p *Plan, d Deps, rec *Installed) error {
	v := *p.Chosen
	exe, err := d.look("ollama")
	if err != nil {
		return err
	}
	hostPort := net.JoinHostPort(p.Serve.Host, strconv.Itoa(p.Serve.Port))
	env := []string{"OLLAMA_HOST=" + hostPort}
	rec.Exe, rec.Args, rec.Env = ServeCommand(p, exe, "")
	base := endpoint(p.Serve.Host, p.Serve.Port)
	tagsURL := base + "/api/tags"
	if p.Serve.Autostart {
		cmd, args, senv := ServeCommand(p, exe, "")
		sv := d.service(p.Name, "Rigfile local model server ("+p.Name+")", cmd, args, senv)
		if _, err := sv.Install(); err != nil {
			return err
		}
		rec.Service = "model-" + serviceName(p.Name)
	} else if !d.healthy(ctx, tagsURL) {
		// no service: run the server only while the model is pulled
		if d.StartTemp == nil {
			return errors.New("no way to start a temporary server")
		}
		stop, err := d.StartTemp(ctx, []string{exe, "serve"}, env)
		if err != nil {
			return err
		}
		defer stop()
	}
	if err := d.waitHealthy(ctx, tagsURL); err != nil {
		return fmt.Errorf("the ollama server did not answer on %s: %w", hostPort, err)
	}
	fmt.Fprintf(d.out(), "pulling %s (%s)...\n", v.Model, HumanBytes(p.DownloadBytes))
	if err := d.Run(ctx, []string{exe, "pull", v.Model}, env, d.out()); err != nil {
		return fmt.Errorf("ollama pull %s: %w", v.Model, err)
	}
	id, err := d.ollamaID(ctx, tagsURL, v.Model)
	if err != nil {
		return err
	}
	rec.Digest = id
	if v.Digest != "" && !strings.HasPrefix(id, strings.ToLower(v.Digest)) {
		return fmt.Errorf("the model %s that was pulled has id %s, not the pinned %s: it is not what the plan showed. Remove it with `ollama rm %s` and check the catalog", v.Model, id, v.Digest, v.Model)
	}
	if v.Digest == "" {
		fmt.Fprintf(d.out(), "warning: %s is not pinned to a digest; pulled id %s\n", v.Model, id)
	}
	return nil
}

func (d Deps) healthy(ctx context.Context, u string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (d Deps) waitHealthy(ctx context.Context, u string) error {
	wait := d.WaitFor
	if wait == 0 {
		wait = 60 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if d.healthy(ctx, u) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no answer from %s after %s", u, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// ollamaID returns the first 12 hex digits of the model's digest as /api/tags reports it (what `ollama list` shows as ID).
func (d Deps) ollamaID(ctx context.Context, tagsURL, model string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&tags); err != nil {
		return "", err
	}
	for _, m := range tags.Models {
		if m.Name == model || m.Model == model {
			id := strings.ToLower(strings.TrimPrefix(m.Digest, "sha256:"))
			if len(id) < 12 {
				return "", fmt.Errorf("ollama reported an unusable digest for %s", model)
			}
			return id[:12], nil
		}
	}
	return "", fmt.Errorf("ollama does not list %s after the pull", model)
}

// Remove takes a model away: its service, and (mlx-lm) its snapshot. Ollama's store is left to `ollama rm`, which is
// printed, because Ollama may share layers between models.
func Remove(ctx context.Context, rec Installed, d Deps) []string {
	var notes []string
	if rec.Service != "" {
		name := strings.TrimPrefix(rec.Service, "model-")
		sv := d.service(name, "", "/x", nil, nil)
		if sv.Spec.GOOS == "windows" {
			sv.Spec.Exe = `C:\x`
		}
		if err := sv.Uninstall(); err != nil {
			notes = append(notes, "could not remove the service: "+err.Error())
		}
	}
	if rec.Dir != "" {
		if err := os.RemoveAll(rec.Dir); err != nil {
			notes = append(notes, "could not remove "+rec.Dir+": "+err.Error())
		}
	}
	if rec.Engine == "ollama" {
		notes = append(notes, "to free the disk: ollama rm "+rec.Model)
	}
	return notes
}

// serviceName maps a `models:` key (letters, digits, underscores, hyphens) to a service name (lower case, hyphens).
func serviceName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}
