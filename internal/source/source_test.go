package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	ok := map[string]string{
		"github.com/o/r":                                      "github.com/o/r",
		"github.com/o/r@v1.2.0":                               "github.com/o/r@v1.2.0",
		"github.com/o/r.git@feature/x":                        "github.com/o/r@feature/x",
		"github.com/o/r@main//rigs/py":                        "github.com/o/r@main//rigs/py",
		"https://github.com/o/r":                              "github.com/o/r",
		"gitlab.com/g/sub/p@v1":                               "gitlab.com/g/sub/p@v1",
		"https://git.example.test/x/y.git@v2":                 "https://git.example.test/x/y@v2",
		"ssh://git@git.example.test/x/y.git@v2//sub":          "ssh://git@git.example.test/x/y@v2//sub",
		"file:///tmp/repo.git":                                "file:///tmp/repo",
		"rigfile+https://registry.example.test/jia/demo":      "rigfile+https://registry.example.test/jia/demo",
		"rigfile+https://registry.example.test/jia/demo@^1.2": "rigfile+https://registry.example.test/jia/demo@^1.2",
		"rigfile+http://localhost:8080/jia/demo@1.0.0-rc1":    "rigfile+http://localhost:8080/jia/demo@1.0.0-rc1",
	}
	for in, want := range ok {
		s, err := Parse(in)
		if err != nil || s.String() != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, s.String(), err, want)
		}
	}
	for _, in := range []string{"", "github.com/o", "github.com/o/r/extra", "github.com/-o/r", "github.com/o/r@-x", "github.com/o/r@a..b",
		"github.com/o/r//../x", "github.com/o/r//C:\\x", "http://github.com/o/r", "ftp://x/y", "git@github.com:o/r", "example.com/o/r",
		"ext::sh -c id://x/y", "https://-oProxy=x/y/z",
		"rigfile+http://registry.example.test/jia/demo", "rigfile+https://x.test/onlyowner", "rigfile+https://x.test/a/b/c", "rigfile+https://x.test/a/b@$(id)", "rigfile+https://x.test/a/b@../x"} {
		if s, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail, got %+v", in, s)
		}
	}
	if !Looks("github.com/o/r") || Looks("./rig") || Looks("/abs/rig") || !Looks("https://x/y") {
		t.Error("Looks")
	}
}

type ent struct {
	name, body string
	typ        byte
	mode       int64
}

func mkTar(t *testing.T, gz bool, ents ...ent) []byte {
	t.Helper()
	var buf bytes.Buffer
	var tw *tar.Writer
	var gw *gzip.Writer
	if gz {
		gw = gzip.NewWriter(&buf)
		tw = tar.NewWriter(gw)
	} else {
		tw = tar.NewWriter(&buf)
	}
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: mode, Size: int64(len(e.body))}
		if typ == tar.TypeSymlink || typ == tar.TypeLink {
			h.Linkname, h.Size = "target", 0
		}
		if typ == tar.TypeDir {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	if gz {
		_ = gw.Close()
	}
	return buf.Bytes()
}

const manifestBody = "apiVersion: rigfile.dev/v1\nname: x/demo\nversion: 1.0.0\ninstructions:\n  - {id: hi, file: instructions/hi.md}\n"

func goodTar(t *testing.T, top string) []byte {
	return mkTar(t, true,
		ent{name: top + "/", typ: tar.TypeDir},
		ent{name: top + "/rigfile.yaml", body: manifestBody},
		ent{name: top + "/instructions/hi.md", body: "hello\n"},
		ent{name: top + "/scripts/run.sh", body: "#!/bin/sh\n", mode: 0o755})
}

func TestExtractStripsTheTopDirectoryAndKeepsExecutableBits(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(bytes.NewReader(goodTar(t, "repo-abc123")), dest, true, Limits{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"rigfile.yaml", "instructions/hi.md", "scripts/run.sh"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	// plain (not gzipped) tar works too
	d2 := filepath.Join(t.TempDir(), "o")
	if err := Extract(bytes.NewReader(mkTar(t, false, ent{name: "rigfile.yaml", body: manifestBody})), d2, false, Limits{}); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRefusesEveryUnsafeShape(t *testing.T) {
	big := strings.Repeat("A", 200)
	cases := map[string][]ent{
		"traversal":            {{name: "top/../../evil"}},
		"traversal in middle":  {{name: "top/a/../../b"}},
		"absolute":             {{name: "/etc/passwd"}},
		"backslash":            {{name: `top\evil`}},
		"drive letter":         {{name: "top/C:evil"}},
		"symlink":              {{name: "top/link", typ: tar.TypeSymlink}},
		"hard link":            {{name: "top/link", typ: tar.TypeLink}},
		"device":               {{name: "top/dev", typ: tar.TypeChar}},
		"fifo":                 {{name: "top/fifo", typ: tar.TypeFifo}},
		".git directory":       {{name: "top/.git/config", body: "x"}},
		"case collision":       {{name: "top/A.md", body: "1"}, {name: "top/a.md", body: "2"}},
		"reserved name":        {{name: "top/nul.txt", body: "1"}},
		"reserved com port":    {{name: "top/COM1", body: "1"}},
		"trailing dot":         {{name: "top/x.", body: "1"}},
		"second top directory": {{name: "top/a", body: "1"}, {name: "other/b", body: "2"}},
		"too large":            {{name: "top/big", body: big}},
	}
	lim := Limits{MaxEntries: 50, MaxFileSize: 100, MaxTotal: 1000}
	for name, ents := range cases {
		dest := filepath.Join(t.TempDir(), "out")
		if err := Extract(bytes.NewReader(mkTar(t, true, ents...)), dest, true, lim); err == nil {
			t.Errorf("%s: extraction should fail", name)
		}
	}
	// counts and totals
	var many []ent
	for i := 0; i < 60; i++ {
		many = append(many, ent{name: fmt.Sprintf("top/f%d", i), body: "x"})
	}
	if err := Extract(bytes.NewReader(mkTar(t, true, many...)), filepath.Join(t.TempDir(), "o"), true, lim); err == nil {
		t.Error("too many entries")
	}
	var total []ent
	for i := 0; i < 20; i++ {
		total = append(total, ent{name: fmt.Sprintf("top/f%d", i), body: strings.Repeat("x", 90)})
	}
	if err := Extract(bytes.NewReader(mkTar(t, true, total...)), filepath.Join(t.TempDir(), "o"), true, lim); err == nil {
		t.Error("too large in total")
	}
	if err := Extract(strings.NewReader("this is not an archive"), filepath.Join(t.TempDir(), "o"), true, lim); err == nil {
		t.Error("garbage")
	}
	nonEmpty := t.TempDir()
	_ = os.WriteFile(filepath.Join(nonEmpty, "x"), nil, 0o644)
	if err := Extract(bytes.NewReader(goodTar(t, "t")), nonEmpty, true, Limits{}); err == nil {
		t.Error("a non-empty target must be refused")
	}
}

// fakeGitHub serves the two endpoints the fetcher uses, over TLS on a loopback port.
type fakeGitHub struct {
	srv      *httptest.Server
	sha      string
	tarball  []byte
	tokens   []string // Authorization headers seen by the API host
	redirect string   // when set, the tarball endpoint redirects here
	resolves int
}

func newFakeGitHub(t *testing.T, tarball []byte) *fakeGitHub {
	f := &fakeGitHub{sha: strings.Repeat("ab", 20), tarball: tarball}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits/", func(w http.ResponseWriter, r *http.Request) {
		f.resolves++
		f.tokens = append(f.tokens, r.Header.Get("Authorization"))
		if !strings.Contains(r.Header.Get("Accept"), "vnd.github.sha") {
			http.Error(w, "bad accept", 400)
			return
		}
		fmt.Fprint(w, f.sha)
	})
	mux.HandleFunc("/repos/o/r/tarball/", func(w http.ResponseWriter, r *http.Request) {
		f.tokens = append(f.tokens, r.Header.Get("Authorization"))
		if f.redirect != "" {
			http.Redirect(w, r, f.redirect, http.StatusFound)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/"+f.sha) {
			// a different host name for the same server, like api.github.com -> codeload.github.com
			http.Redirect(w, r, "https://localhost:"+f.port()+"/codeload/o/r/"+f.sha, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/codeload/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("the token must not follow a redirect")
		}
		_, _ = w.Write(f.tarball)
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) port() string {
	u, _ := url.Parse(f.srv.URL)
	return u.Port()
}

func (f *fakeGitHub) fetcher(env map[string]string) *HTTPS {
	u, _ := url.Parse(f.srv.URL)
	c := f.srv.Client()
	c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &HTTPS{Client: c, GitHubAPI: f.srv.URL, AllowedHosts: []string{u.Host, "localhost:" + u.Port()}, Getenv: func(k string) string { return env[k] }}
}

func mustSpec(t *testing.T, s string) Spec {
	t.Helper()
	sp, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestPinnedByCommitAndTree(t *testing.T) {
	f := newFakeGitHub(t, goodTar(t, "o-r-abc"))
	cache := t.TempDir()
	c := &Client{CacheDir: cache, HTTPS: f.fetcher(map[string]string{"GITHUB_TOKEN": "fake-token-value"})}
	got, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r@v1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != f.sha || len(got.TreeSHA256) != 64 || !strings.HasPrefix(got.Dir, cache) {
		t.Fatalf("%+v", got)
	}
	if f.tokens[0] != "Bearer fake-token-value" {
		t.Fatalf("token was not sent to the API host: %v", f.tokens)
	}
	// a pinned fetch never re-resolves
	resolves := f.resolves
	again, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r@v1"), &Pin{Commit: got.Commit, TreeSHA256: got.TreeSHA256})
	if err != nil || again.Dir != got.Dir || f.resolves != resolves {
		t.Fatalf("%v resolves %d -> %d", err, resolves, f.resolves)
	}
	// the cache entry is re-checked: an edited file is discarded and fetched again
	if err := os.WriteFile(filepath.Join(got.Dir, "instructions", "hi.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r@v1"), &Pin{Commit: got.Commit, TreeSHA256: got.TreeSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(fresh.Dir, "instructions", "hi.md")); string(b) != "hello\n" {
		t.Fatalf("cache was not repaired: %q", b)
	}
	// a shipped rigfile.lock does not change the pin
	if err := os.WriteFile(filepath.Join(fresh.Dir, "rigfile.lock"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h, _ := TreeHash(fresh.Dir); h != got.TreeSHA256 {
		t.Fatal("rigfile.lock must not be part of the tree hash")
	}
}

func TestMovedTagIsRefused(t *testing.T) {
	f := newFakeGitHub(t, goodTar(t, "o-r-abc"))
	c := &Client{CacheDir: t.TempDir(), HTTPS: f.fetcher(nil)}
	first, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r@v1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// the same commit id now serves different files (a force-pushed/compromised source)
	f.tarball = mkTar(t, true, ent{name: "o-r-abc/rigfile.yaml", body: manifestBody + "# changed\n"}, ent{name: "o-r-abc/instructions/hi.md", body: "hello\n"})
	c2 := &Client{CacheDir: t.TempDir(), HTTPS: f.fetcher(nil)}
	_, err = c2.Get(context.Background(), mustSpec(t, "github.com/o/r@v1"), &Pin{Commit: first.Commit, TreeSHA256: first.TreeSHA256})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("want ErrChanged, got %v", err)
	}
}

func TestRedirectToAnotherHostAndPlainHTTPAreRefused(t *testing.T) {
	f := newFakeGitHub(t, goodTar(t, "t"))
	f.redirect = "https://evil.example.test/steal.tar.gz"
	c := &Client{CacheDir: t.TempDir(), HTTPS: f.fetcher(nil)}
	if _, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r"), nil); err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("%v", err)
	}
	f.redirect = "http://" + strings.TrimPrefix(f.srv.URL, "https://") + "/codeload/o/r/x"
	if _, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r"), nil); err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("plain http redirect: %v", err)
	}
	h := f.fetcher(nil)
	h.GitHubAPI = "http://127.0.0.1:1"
	if _, err := h.Resolve(context.Background(), mustSpec(t, "github.com/o/r")); err == nil || !strings.Contains(err.Error(), "refusing to contact") {
		t.Fatalf("%v", err)
	}
}

func TestOversizedDownloadIsCut(t *testing.T) {
	huge := mkTar(t, true, ent{name: "t/rigfile.yaml", body: manifestBody}, ent{name: "t/a", body: strings.Repeat("x", 4<<20)}, ent{name: "t/b", body: strings.Repeat("y", 4<<20)})
	f := newFakeGitHub(t, huge)
	h := f.fetcher(nil)
	h.Limits = Limits{MaxEntries: 100, MaxFileSize: 5 << 20, MaxTotal: 6 << 20}
	c := &Client{CacheDir: t.TempDir(), HTTPS: h}
	if _, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r"), nil); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("%v", err)
	}
}

func TestAMissingManifestOrSubdirectory(t *testing.T) {
	f := newFakeGitHub(t, mkTar(t, true, ent{name: "t/README.md", body: "x"}))
	c := &Client{CacheDir: t.TempDir(), HTTPS: f.fetcher(nil)}
	if _, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r"), nil); err == nil || !strings.Contains(err.Error(), "no rigfile.yaml") {
		t.Fatalf("%v", err)
	}
	f = newFakeGitHub(t, mkTar(t, true, ent{name: "t/rigs/a/rigfile.yaml", body: manifestBody}, ent{name: "t/rigs/a/instructions/hi.md", body: "hi"}))
	c = &Client{CacheDir: t.TempDir(), HTTPS: f.fetcher(nil)}
	got, err := c.Get(context.Background(), mustSpec(t, "github.com/o/r//rigs/a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "rigfile.yaml")); err != nil {
		t.Fatal(err)
	}
}

// ---- git fetcher against a local repository ----

func gitEnv(t *testing.T) []string {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	return []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test"}
}

func git(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitFetcherResolvesTagsBranchesAndPinsTheCommit(t *testing.T) {
	env := gitEnv(t)
	repo := t.TempDir()
	git(t, repo, env, "init", "-q", "-b", "main")
	git(t, repo, env, "config", "uploadpack.allowAnySHA1InWant", "true")
	put := func(rel, body string) {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("rigfile.yaml", manifestBody)
	put("instructions/hi.md", "v1\n")
	git(t, repo, env, "add", "-A")
	git(t, repo, env, "commit", "-q", "-m", "one")
	first := git(t, repo, env, "rev-parse", "HEAD")
	git(t, repo, env, "tag", "-a", "v1", "-m", "release one")
	put("instructions/hi.md", "v2\n")
	git(t, repo, env, "commit", "-q", "-am", "two")
	second := git(t, repo, env, "rev-parse", "HEAD")

	u := "file://" + filepath.ToSlash(repo)
	if !strings.HasPrefix(u, "file:///") {
		u = "file:///" + strings.TrimPrefix(filepath.ToSlash(repo), "/")
	}
	g := &GitFetcher{Env: env}
	c := &Client{CacheDir: t.TempDir(), Git: g}
	got, err := c.Get(context.Background(), mustSpec(t, u+"@v1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != first { // annotated tag peeled to its commit
		t.Fatalf("commit = %s, want %s", got.Commit, first)
	}
	if b, _ := os.ReadFile(filepath.Join(got.Dir, "instructions", "hi.md")); string(b) != "v1\n" {
		t.Fatalf("%q", b)
	}
	head, err := c.Get(context.Background(), mustSpec(t, u), nil)
	if err != nil || head.Commit != second {
		t.Fatalf("%v %+v", err, head)
	}
	byBranch, err := c.Get(context.Background(), mustSpec(t, u+"@main"), nil)
	if err != nil || byBranch.Commit != second || byBranch.TreeSHA256 != head.TreeSHA256 {
		t.Fatalf("%v", err)
	}
	byCommit, err := c.Get(context.Background(), mustSpec(t, u+"@"+first), nil)
	if err != nil || byCommit.TreeSHA256 != got.TreeSHA256 {
		t.Fatalf("%v", err)
	}
	if _, err := c.Get(context.Background(), mustSpec(t, u+"@nope"), nil); err == nil {
		t.Fatal("an unknown ref must fail")
	}
	// a repository that holds a symlink is refused whole
	if err := os.Symlink("/etc/passwd", filepath.Join(repo, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	git(t, repo, env, "add", "-A")
	git(t, repo, env, "commit", "-q", "-m", "link")
	if _, err := c.Get(context.Background(), mustSpec(t, u+"@main"), nil); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("%v", err)
	}
}
