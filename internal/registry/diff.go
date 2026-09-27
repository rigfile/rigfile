package registry

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rigfile/rigfile/internal/rigdiff"
	"github.com/rigfile/rigfile/internal/source"
)

// Diffs are CPU and disk work on data the publisher chose, so they are limited three ways: a per-client rate limit, at most
// a few running at once, and a small cache. The cache key is the pair of tarball hashes, which are immutable, and it is
// consulted only AFTER the visibility check for both versions, so a cached result can never show a viewer more than the
// predicate allows.
const maxConcurrentDiffs = 3

type diffState struct {
	once  sync.Once
	sem   chan struct{}
	mu    sync.Mutex
	cache map[string]*rigdiff.Result
}

func (s *Server) diffState() *diffState {
	d := &s.diff
	d.once.Do(func() {
		d.sem = make(chan struct{}, maxConcurrentDiffs)
		d.cache = map[string]*rigdiff.Result{}
	})
	return d
}

// extractVersion unpacks a stored version into dir/<name> with the hardened extractor.
func (s *Server) extractVersion(ctx context.Context, sha, dir, name string) (string, error) {
	rc, _, err := s.Blobs.Get(ctx, sha)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	root := filepath.Join(dir, name)
	if err := source.Extract(rc, root, false, source.DefaultLimits); err != nil {
		return "", err
	}
	return root, nil
}

var errNoEarlier = errors.New("there is no earlier version to compare with")

// compareVersions compares two versions of a rig the viewer may see. Empty from means "the version before to"; empty to
// means "the newest published version".
func (s *Server) compareVersions(ctx context.Context, owner, name, from, to string, v Viewer) (*rigdiff.Result, error) {
	rig, err := s.Store.GetRig(ctx, owner, name, v)
	if err != nil {
		return nil, err
	}
	vs, err := s.Store.ListVersions(ctx, rig.ID, v) // newest first, only what the viewer may see
	if err != nil {
		return nil, err
	}
	find := func(ver string) int {
		for i := range vs {
			if vs[i].Version == ver {
				return i
			}
		}
		return -1
	}
	ti := -1
	if to == "" {
		for i := range vs {
			if vs[i].Status == "published" {
				ti = i
				break
			}
		}
	} else {
		ti = find(to)
	}
	if ti < 0 {
		return nil, ErrNotFound
	}
	fi := -1
	if from == "" {
		for i := ti + 1; i < len(vs); i++ {
			if vs[i].Status == "published" || vs[i].Status == "yanked" {
				fi = i
				break
			}
		}
		if fi < 0 {
			return nil, errNoEarlier
		}
	} else if fi = find(from); fi < 0 {
		return nil, ErrNotFound
	}
	a, b := vs[fi], vs[ti]

	st := s.diffState()
	key := a.TarballSHA256 + "|" + b.TarballSHA256
	st.mu.Lock()
	cached := st.cache[key]
	st.mu.Unlock()
	res := cached
	if res == nil {
		select {
		case st.sem <- struct{}{}:
			defer func() { <-st.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		dir, err := os.MkdirTemp("", "rigfile-diff-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		da, err := s.extractVersion(ctx, a.TarballSHA256, dir, "a")
		if err != nil {
			return nil, err
		}
		db, err := s.extractVersion(ctx, b.TarballSHA256, dir, "b")
		if err != nil {
			return nil, err
		}
		if res, err = rigdiff.Rigs(da, db, a.Version, b.Version); err != nil {
			return nil, err
		}
		st.mu.Lock()
		if len(st.cache) >= 64 {
			st.cache = map[string]*rigdiff.Result{}
		}
		st.cache[key] = res
		st.mu.Unlock()
	}
	// the labels belong to this request, not to the cached content
	out := *res
	out.From, out.To = a.Version, b.Version
	return &out, nil
}

func (s *Server) apiDiff(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "diff", 30, 10) {
		return
	}
	v, _, ok := s.viewer(w, r)
	if !ok {
		return
	}
	res, err := s.compareVersions(r.Context(), r.PathValue("owner"), r.PathValue("name"), r.URL.Query().Get("from"), r.URL.Query().Get("to"), v)
	switch {
	case errors.Is(err, ErrNotFound):
		apiError(w, http.StatusNotFound, "no such rig or version")
	case errors.Is(err, errNoEarlier):
		apiError(w, http.StatusNotFound, err.Error())
	case err != nil:
		s.Log.Error("diff", "err", err)
		apiError(w, http.StatusInternalServerError, "could not compare these versions")
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

// diffLine is one rendered line of a unified diff.
type diffLine struct{ Kind, Text string }

type diffFile struct {
	rigdiff.FileChange
	Lines []diffLine
}

type diffPage struct {
	Rig      *Rig
	Result   *rigdiff.Result
	Review   []string
	Files    []diffFile
	FromPath string
	Err      string
}

func diffLines(d string) []diffLine {
	var out []diffLine
	for _, l := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
		k := "ctx"
		switch {
		case strings.HasPrefix(l, "@@"):
			k = "hunk"
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
			k = "file"
		case strings.HasPrefix(l, "+"):
			k = "add"
		case strings.HasPrefix(l, "-"):
			k = "del"
		}
		out = append(out, diffLine{k, l})
	}
	return out
}

func (s *Server) pageDiff(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "diff", 30, 10) {
		return
	}
	v, u, csrf := s.pageViewer(r)
	owner, name := r.PathValue("owner"), r.PathValue("name")
	rig, err := s.Store.GetRig(r.Context(), owner, name, v)
	if err != nil {
		s.notFound(w, r)
		return
	}
	res, err := s.compareVersions(r.Context(), owner, name, r.URL.Query().Get("from"), r.URL.Query().Get("to"), v)
	switch {
	case errors.Is(err, ErrNotFound):
		s.notFound(w, r)
		return
	case errors.Is(err, errNoEarlier):
		s.render(w, r, http.StatusOK, "diff.html", Page{Title: "Changes", User: u, CSRF: csrf, Data: diffPage{Rig: rig, Err: err.Error()}})
		return
	case err != nil:
		s.Log.Error("diff", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dp := diffPage{Rig: rig, Result: res, Review: res.Review(), FromPath: "/r/" + url.PathEscape(owner) + "/" + url.PathEscape(name)}
	for _, f := range res.Files {
		dp.Files = append(dp.Files, diffFile{FileChange: f, Lines: diffLines(f.Diff)})
	}
	s.render(w, r, http.StatusOK, "diff.html", Page{Title: owner + "/" + name + " " + res.From + " to " + res.To, User: u, CSRF: csrf, Data: dp})
}
