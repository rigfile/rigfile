package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Detected is a model server found running on loopback.
type Detected struct {
	Engine  string
	Version string
	Model   string
	Digest  string
	Port    int
}

// DetectOllama asks a loopback Ollama what it serves (`rigfile init` turns it into a `models:` entry). It touches only the
// network port; no configuration folder is read.
func DetectOllama(ctx context.Context, c *http.Client, port int) []Detected {
	if c == nil {
		c = &http.Client{Timeout: 3 * time.Second}
	}
	base := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	get := func(path string, v any) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return false
		}
		resp, err := c.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v) == nil
	}
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if !get("/api/tags", &tags) {
		return nil
	}
	var ver struct {
		Version string `json:"version"`
	}
	get("/api/version", &ver)
	var out []Detected
	for _, m := range tags.Models {
		d := strings.ToLower(strings.TrimPrefix(m.Digest, "sha256:"))
		if len(d) >= 12 {
			d = d[:12]
		}
		out = append(out, Detected{Engine: "ollama", Version: ver.Version, Model: m.Name, Digest: d, Port: port})
	}
	return out
}

var keyRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// ManifestSection renders detected models as a `models:` block for a captured rig. Every entry pins the model by digest;
// an unknown engine version is left out (and reported) rather than guessed.
func ManifestSection(ds []Detected) (string, []string) {
	if len(ds) == 0 {
		return "", nil
	}
	var sb strings.Builder
	var notes []string
	sb.WriteString("models:\n")
	seen := map[string]bool{}
	for _, d := range ds {
		key := strings.Trim(keyRe.ReplaceAllString(d.Model, "-"), "-")
		if key == "" || len(key) > 64 || seen[key] {
			notes = append(notes, fmt.Sprintf("model %q was not captured (its name cannot be used as a key)", d.Model))
			continue
		}
		seen[key] = true
		fmt.Fprintf(&sb, "  %s:\n    variants:\n      - when: {}\n        engine: %s\n", key, d.Engine)
		if d.Version != "" {
			fmt.Fprintf(&sb, "        engine_version: %q\n", d.Version)
		} else {
			notes = append(notes, fmt.Sprintf("model %s: the engine version could not be read; pin engine_version by hand", d.Model))
		}
		fmt.Fprintf(&sb, "        model: %s\n", d.Model)
		if d.Digest != "" {
			fmt.Fprintf(&sb, "        digest: %s\n", d.Digest)
		}
		fmt.Fprintf(&sb, "    serve: {port: %d}\n", d.Port)
	}
	return sb.String(), notes
}
