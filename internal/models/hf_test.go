package models

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const rev = "545dc4251c05440727734bcd94334791f6ab0192"

// fakeHF serves one repository the way the real API does: a tree listing (LFS oid = sha256 for weights, git blob id for
// small files), a resolve URL that redirects to a "CDN" path, and Range support.
type fakeHF struct {
	srv     *httptest.Server
	mu      sync.Mutex
	files   map[string][]byte
	tamper  map[string][]byte // path -> bytes the CDN serves instead
	hits    map[string]int
	pages   bool
	cutAt   map[string]int // path -> stop after this many bytes (simulate a dropped connection), once
	listErr int
}

func newFakeHF(t *testing.T, files map[string]string) *fakeHF {
	f := &fakeHF{files: map[string][]byte{}, tamper: map[string][]byte{}, hits: map[string]int{}, cutAt: map[string]int{}}
	for p, c := range files {
		f.files[p] = []byte(c)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/models/{org}/{name}/tree/{rev}", func(w http.ResponseWriter, r *http.Request) {
		if f.listErr != 0 {
			http.Error(w, "boom", f.listErr)
			return
		}
		var entries []map[string]any
		paths := make([]string, 0, len(f.files))
		for p := range f.files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			b := f.files[p]
			e := map[string]any{"type": "file", "path": p, "size": len(b)}
			if strings.HasSuffix(p, ".safetensors") || strings.HasSuffix(p, ".gguf") {
				s := sha256.Sum256(b)
				e["oid"] = "ignored-pointer-oid"
				e["lfs"] = map[string]any{"oid": hex.EncodeToString(s[:]), "size": len(b)}
			} else {
				h := sha1.New() //nolint:gosec
				fmt.Fprintf(h, "blob %d\x00", len(b))
				h.Write(b)
				e["oid"] = hex.EncodeToString(h.Sum(nil))
			}
			entries = append(entries, e)
		}
		entries = append(entries, map[string]any{"type": "directory", "path": "sub"})
		if f.pages {
			if r.URL.Query().Get("cursor") == "" {
				w.Header().Set("Link", `<`+f.srv.URL+r.URL.Path+`?recursive=true&cursor=2>; rel="next"`)
				json.NewEncoder(w).Encode(entries[:1])
				return
			}
			json.NewEncoder(w).Encode(entries[1:])
			return
		}
		json.NewEncoder(w).Encode(entries)
	})
	mux.HandleFunc("GET /repo/{org}/{name}/resolve/{rev}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.PathValue("path")]++
		f.mu.Unlock()
		http.Redirect(w, r, "/cdn/"+r.PathValue("path"), http.StatusFound)
	})
	mux.HandleFunc("GET /cdn/{path...}", func(w http.ResponseWriter, r *http.Request) {
		p := r.PathValue("path")
		b, ok := f.files[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if t, ok := f.tamper[p]; ok {
			b = t
		}
		if n, ok := f.cutAt[p]; ok && r.Header.Get("Range") == "" {
			delete(f.cutAt, p)
			w.Header().Set("Content-Length", fmt.Sprint(len(b)))
			w.WriteHeader(200)
			w.Write(b[:n])
			return // the connection ends short
		}
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(b))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHF) resolveHits(p string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.hits[p] }

func goodRepo() map[string]string {
	return map[string]string{
		".gitattributes":    "*.safetensors filter=lfs\n", // real model repositories carry one
		"config.json":       `{"model_type":"qwen3"}`,
		"tokenizer.json":    strings.Repeat("token ", 100),
		"model.safetensors": strings.Repeat("weights!", 5000),
	}
}

func TestFetchDownloadsAndVerifiesEverything(t *testing.T) {
	f := newFakeHF(t, goodRepo())
	dest := t.TempDir()
	hf := &HF{Base: f.srv.URL, RepoPrefix: "/repo"}
	var last int64
	files, err := hf.Fetch(context.Background(), "mlx-community/Qwen3-8B-4bit", rev, "mlx-safetensors", dest, func(done, total int64) { last = done })
	if err != nil || len(files) != 4 {
		t.Fatalf("%v %v", files, err)
	}
	for p, c := range goodRepo() {
		if b, _ := os.ReadFile(filepath.Join(dest, p)); string(b) != c {
			t.Errorf("%s differs", p)
		}
		if _, err := os.Stat(filepath.Join(dest, p+".part")); err == nil {
			t.Errorf("%s: a .part file was left behind", p)
		}
	}
	var want int64
	for _, c := range goodRepo() {
		want += int64(len(c))
	}
	if last != want {
		t.Fatalf("progress ended at %d, want %d", last, want)
	}
	// a second run downloads nothing: everything already verifies
	before := f.resolveHits("model.safetensors")
	if _, err := hf.Fetch(context.Background(), "mlx-community/Qwen3-8B-4bit", rev, "mlx-safetensors", dest, nil); err != nil {
		t.Fatal(err)
	}
	if f.resolveHits("model.safetensors") != before {
		t.Fatal("a verified file must not be downloaded again")
	}
}

func TestFetchRefusesTamperedBytes(t *testing.T) {
	for name, tc := range map[string]struct {
		path string
		evil string
		want string
	}{
		"same-size tampered weights": {"model.safetensors", strings.Repeat("WEIGHTS!", 5000), "verification failed"},
		"a shorter file":             {"model.safetensors", "short", "received"},
		"tampered small file":        {"tokenizer.json", strings.Repeat("evil! ", 100), "verification failed"},
	} {
		f := newFakeHF(t, goodRepo())
		f.tamper[tc.path] = []byte(tc.evil)
		dest := t.TempDir()
		_, err := (&HF{Base: f.srv.URL, RepoPrefix: "/repo"}).Fetch(context.Background(), "mlx-community/x", rev, "safetensors", dest, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
		if _, e := os.Stat(filepath.Join(dest, tc.path)); e == nil {
			t.Errorf("%s: a file that failed verification is at its final name", name)
		}
		if _, e := os.Stat(filepath.Join(dest, tc.path+".part")); tc.want == "verification failed" && e == nil {
			t.Errorf("%s: a corrupt .part must be discarded, not resumed", name)
		}
	}
}

func TestFetchRefusesUnsafeRepositories(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"pickle weights":    {map[string]string{"pytorch_model.bin": "x", "config.json": "{}", "model.safetensors": "w"}, "pickle-based"},
		"a .pt file":        {map[string]string{"model.pt": "x", "model.safetensors": "w"}, "pickle-based"},
		"python source":     {map[string]string{"modeling_x.py": "import os", "model.safetensors": "w"}, "trust_remote_code"},
		"no weights at all": {map[string]string{"config.json": "{}"}, "no safetensors weights"},
		"an odd path":       {map[string]string{"a/../../evil": "x", "model.safetensors": "w"}, "unsafe file path"},
		"a .git directory":  {map[string]string{".git/config": "x", "model.safetensors": "w"}, "unsafe file path"},
	} {
		f := newFakeHF(t, tc.files)
		dest := t.TempDir()
		_, err := (&HF{Base: f.srv.URL, RepoPrefix: "/repo"}).Fetch(context.Background(), "org/name", rev, "safetensors", dest, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
		if ents, _ := os.ReadDir(dest); len(ents) != 0 {
			t.Errorf("%s: nothing may be downloaded from a refused repository: %v", name, ents)
		}
	}
	// a config that asks to run repository code is refused after config.json, before any weights
	f := newFakeHF(t, map[string]string{"config.json": `{"auto_map":{"AutoModel":"modeling.X"}}`, "model.safetensors": "w"})
	dest := t.TempDir()
	if _, err := (&HF{Base: f.srv.URL, RepoPrefix: "/repo"}).Fetch(context.Background(), "org/name", rev, "safetensors", dest, nil); err == nil || !strings.Contains(err.Error(), "auto_map") {
		t.Fatalf("%v", err)
	}
	if f.resolveHits("model.safetensors") != 0 {
		t.Fatal("the weights must not be downloaded for a model that wants remote code")
	}
	if _, err := os.Stat(filepath.Join(dest, "config.json")); err == nil {
		t.Fatal("the offending config must be removed")
	}
}

func TestFetchResumesAnInterruptedDownload(t *testing.T) {
	f := newFakeHF(t, goodRepo())
	f.cutAt["model.safetensors"] = 12345
	dest := t.TempDir()
	hf := &HF{Base: f.srv.URL, RepoPrefix: "/repo"}
	if _, err := hf.Fetch(context.Background(), "org/name", rev, "safetensors", dest, nil); err == nil {
		t.Fatal("a dropped connection is an error")
	}
	st, err := os.Stat(filepath.Join(dest, "model.safetensors.part"))
	if err != nil || st.Size() != 12345 {
		t.Fatalf("the partial file must be kept: %v %v", st, err)
	}
	if _, err := hf.Fetch(context.Background(), "org/name", rev, "safetensors", dest, nil); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "model.safetensors")); string(b) != goodRepo()["model.safetensors"] {
		t.Fatal("the resumed file is wrong")
	}
}

func TestListInputsAndPagination(t *testing.T) {
	f := newFakeHF(t, goodRepo())
	f.pages = true
	files, err := (&HF{Base: f.srv.URL, RepoPrefix: "/repo"}).List(context.Background(), "org/name", rev)
	if err != nil || len(files) != 4 {
		t.Fatalf("both pages must be read: %v %v", files, err)
	}
	hf := &HF{Base: f.srv.URL, RepoPrefix: "/repo"}
	if _, err := hf.List(context.Background(), "not a repo", rev); err == nil {
		t.Fatal("bad repo id")
	}
	if _, err := hf.List(context.Background(), "org/name", "main"); err == nil || !strings.Contains(err.Error(), "40-character") {
		t.Fatalf("a branch is not a revision: %v", err)
	}
	f.listErr = 404
	if _, err := hf.List(context.Background(), "org/name", rev); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("%v", err)
	}
}

func TestATokenIsSentOnlyToTheRegistryItself(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "cdn:"+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Write([]byte("w"))
	}))
	defer cdn.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "api:"+r.Header.Get("Authorization"))
		mu.Unlock()
		if strings.Contains(r.URL.Path, "/tree/") {
			s := sha256.Sum256([]byte("w"))
			json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "model.safetensors", "size": 1, "lfs": map[string]any{"oid": hex.EncodeToString(s[:]), "size": 1}}})
			return
		}
		http.Redirect(w, r, cdn.URL+"/x", http.StatusFound)
	}))
	defer api.Close()
	hf := &HF{Base: api.URL, RepoPrefix: "", Token: func() string { return "hf_TESTTOKEN" }}
	if _, err := hf.Fetch(context.Background(), "org/name", rev, "safetensors", t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "cdn:") && s != "cdn:" {
			t.Fatalf("the token followed a redirect to another host: %v", seen)
		}
	}
	if seen[0] != "api:Bearer hf_TESTTOKEN" {
		t.Fatalf("%v", seen)
	}
}
