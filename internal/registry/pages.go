package registry

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func (s *Server) pageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.pageHome)
	mux.HandleFunc("GET /search", s.pageSearch)
	mux.HandleFunc("GET /u/{login}", s.pageProfile)
	mux.HandleFunc("GET /r/{owner}/{name}", s.pageRig)
	mux.HandleFunc("GET /r/{owner}/{name}/v/{version}", s.pageRig)
	mux.HandleFunc("GET /r/{owner}/{name}/v/{version}/files/{path...}", s.pageFile)
	mux.HandleFunc("GET /r/{owner}/{name}/v/{version}/raw/{path...}", s.rawFile)
	mux.HandleFunc("POST /r/{owner}/{name}/star", s.pageStar)
	mux.HandleFunc("POST /r/{owner}/{name}/visibility", s.pageVisibility)
	mux.HandleFunc("GET /legal/{doc}", s.pageLegal)
	mux.HandleFunc("GET /admin", s.adminPage)
	mux.HandleFunc("POST /admin/held/{id}", s.adminHeld)
	mux.HandleFunc("POST /admin/approve/{owner}/{name}", s.adminApprove)
	mux.HandleFunc("POST /admin/report/{id}", s.adminReport)
	mux.HandleFunc("POST /admin/verify", s.adminVerify)
	mux.HandleFunc("GET /report", s.reportForm)
	mux.HandleFunc("POST /report", s.reportSubmit)
	mux.HandleFunc("GET /", s.notFound)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.message(w, r, http.StatusNotFound, "Not found", "There is nothing at this address.")
}

// pageViewer is the signed-in user (web session) as a Viewer.
func (s *Server) pageViewer(r *http.Request) (Viewer, *User, string) {
	u, csrf := s.webUser(r)
	return ViewerOf(u), u, csrf
}

type listData struct {
	Rigs        []RigSummary
	Empty       string
	RegistryURL string
}

func (s *Server) pageHome(w http.ResponseWriter, r *http.Request) {
	v, u, csrf := s.pageViewer(r)
	rs, _ := s.Store.Search(r.Context(), "", 12, v)
	s.render(w, r, http.StatusOK, "home.html", Page{Title: "", User: u, CSRF: csrf, Data: listData{Rigs: rs, Empty: "No public rigs yet."}})
}

func (s *Server) pageSearch(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "search", 120, 30) {
		return
	}
	v, u, csrf := s.pageViewer(r)
	q := trunc(strings.TrimSpace(r.URL.Query().Get("q")), 100)
	rs, _ := s.Store.Search(r.Context(), q, 30, v)
	s.render(w, r, http.StatusOK, "search.html", Page{Title: "Search", Query: q, User: u, CSRF: csrf, Data: listData{Rigs: rs, Empty: "Nothing matches. Try fewer words."}})
}

type profileData struct {
	Login string
	Rigs  []RigSummary
}

func (s *Server) pageProfile(w http.ResponseWriter, r *http.Request) {
	v, u, csrf := s.pageViewer(r)
	login := strings.ToLower(r.PathValue("login"))
	if !ownerRe.MatchString(login) {
		s.notFound(w, r)
		return
	}
	if _, err := s.Store.UserByLogin(r.Context(), login); err != nil {
		s.notFound(w, r)
		return
	}
	rs, _ := s.Store.OwnedRigs(r.Context(), login, v)
	s.render(w, r, http.StatusOK, "profile.html", Page{Title: login, User: u, CSRF: csrf, Data: profileData{Login: login, Rigs: rs}})
}

// RigPage is everything the rig page shows.
type RigPage struct {
	Rig        *Rig
	Version    *Version
	Versions   []Version
	Files      []FileEntry
	Readme     template.HTML
	Install    string
	InstallPin string
	IsOwner    bool
	Trust      *Trust
	Registry   string
	Layers     []string
	Latest     string
}

func (s *Server) loadRig(w http.ResponseWriter, r *http.Request) (*RigPage, *User, string, bool) {
	v, u, csrf := s.pageViewer(r)
	owner, name := r.PathValue("owner"), r.PathValue("name")
	rig, err := s.Store.GetRig(r.Context(), owner, name, v)
	if err != nil {
		s.notFound(w, r)
		return nil, nil, "", false
	}
	vs, err := s.Store.ListVersions(r.Context(), rig.ID, v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, nil, "", false
	}
	page := &RigPage{Rig: rig, Versions: vs, Registry: s.Cfg.PublicURL, IsOwner: u != nil && (u.ID == rig.CreatedBy || u.IsAdmin)}
	for i := range vs {
		if vs[i].Status == "published" {
			page.Latest = vs[i].Version
			break
		}
	}
	want := r.PathValue("version")
	if want == "" {
		want = page.Latest
		if want == "" && len(vs) > 0 && page.IsOwner {
			want = vs[0].Version // the owner's newest, even if still pending or rejected
		}
	}
	for i := range vs {
		if vs[i].Version == want {
			page.Version = &vs[i]
		}
	}
	if r.PathValue("version") != "" && page.Version == nil {
		s.notFound(w, r)
		return nil, nil, "", false
	}
	return page, u, csrf, true
}

func (s *Server) pageRig(w http.ResponseWriter, r *http.Request) {
	page, u, csrf, ok := s.loadRig(w, r)
	if !ok {
		return
	}
	if page.Version != nil {
		page.Files, _ = s.Store.VersionFiles(r.Context(), page.Version.ID)
		page.Readme = renderMarkdown(page.Version.Readme)
		page.Layers = page.Version.Layers
		page.Install = "rigfile pull " + page.Rig.Owner + "/" + page.Rig.Name + " --registry " + s.Cfg.PublicURL
		page.Trust, _ = s.Store.Trust(r.Context(), page.Rig, page.Version)
		page.InstallPin = "rigfile pull " + page.Rig.Owner + "/" + page.Rig.Name + "@" + page.Version.Version + " --registry " + s.Cfg.PublicURL
	}
	title := page.Rig.Owner + "/" + page.Rig.Name
	s.render(w, r, http.StatusOK, "rig.html", Page{Title: title, User: u, CSRF: csrf, Data: page})
}

// fileFromTarball reads one file out of a stored version, refusing anything not in the index or not text.
func (s *Server) fileFromTarball(r *http.Request, v *Version, p string, files []FileEntry) ([]byte, error) {
	var want *FileEntry
	for i := range files {
		if files[i].Path == p {
			want = &files[i]
		}
	}
	if want == nil || !want.IsText {
		return nil, ErrNotFound
	}
	rc, _, err := s.Blobs.Get(r.Context(), v.TarballSHA256)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Clean(h.Name) == p {
			return io.ReadAll(io.LimitReader(tr, 256<<10+1))
		}
	}
}

type fileData struct {
	Rig     *Rig
	Version *Version
	Path    string
	Text    string
	RawURL  string
}

func (s *Server) pageFile(w http.ResponseWriter, r *http.Request) {
	page, u, csrf, ok := s.loadRig(w, r)
	if !ok || page.Version == nil {
		if ok {
			s.notFound(w, r)
		}
		return
	}
	files, _ := s.Store.VersionFiles(r.Context(), page.Version.ID)
	p := path.Clean(r.PathValue("path"))
	b, err := s.fileFromTarball(r, page.Version, p, files)
	if err != nil {
		s.notFound(w, r)
		return
	}
	base := "/r/" + url.PathEscape(page.Rig.Owner) + "/" + url.PathEscape(page.Rig.Name) + "/v/" + url.PathEscape(page.Version.Version)
	s.render(w, r, http.StatusOK, "file.html", Page{Title: p, User: u, CSRF: csrf, Data: fileData{Rig: page.Rig, Version: page.Version, Path: p, Text: string(b), RawURL: base + "/raw/" + p}})
}

// rawFile serves a text file as text/plain, never as something a browser would execute or render.
func (s *Server) rawFile(w http.ResponseWriter, r *http.Request) {
	page, _, _, ok := s.loadRig(w, r)
	if !ok || page.Version == nil {
		if ok {
			s.notFound(w, r)
		}
		return
	}
	files, _ := s.Store.VersionFiles(r.Context(), page.Version.ID)
	b, err := s.fileFromTarball(r, page.Version, path.Clean(r.PathValue("path")), files)
	if err != nil {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

func (s *Server) pageStar(w http.ResponseWriter, r *http.Request) {
	u := s.webPost(w, r)
	if u == nil {
		return
	}
	on := r.PostFormValue("action") == "star"
	if err := s.Store.SetStar(r.Context(), r.PathValue("owner"), r.PathValue("name"), u, on); err != nil {
		s.notFound(w, r)
		return
	}
	http.Redirect(w, r, "/r/"+url.PathEscape(r.PathValue("owner"))+"/"+url.PathEscape(r.PathValue("name")), http.StatusSeeOther)
}

func (s *Server) pageVisibility(w http.ResponseWriter, r *http.Request) {
	u := s.webPost(w, r)
	if u == nil {
		return
	}
	err := s.Store.SetVisibility(r.Context(), r.PathValue("owner"), r.PathValue("name"), r.PostFormValue("visibility"), u)
	switch {
	case errors.Is(err, ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, ErrForbidden):
		s.message(w, r, http.StatusForbidden, "Not allowed", "Only the owner can change this.")
	case err != nil:
		s.message(w, r, http.StatusConflict, "Cannot change visibility", err.Error())
	default:
		http.Redirect(w, r, "/r/"+url.PathEscape(r.PathValue("owner"))+"/"+url.PathEscape(r.PathValue("name")), http.StatusSeeOther)
	}
}
