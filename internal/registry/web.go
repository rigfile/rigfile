package registry

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

//go:embed web/templates/*.html
var templateFS embed.FS

//go:embed web/static
var staticFS embed.FS

// staticVersion busts the browser cache when static assets change: the long Cache-Control on staticHandler is
// only safe because callers append ?v=staticVersion, so the URL itself changes whenever the content does.
var staticVersion = func() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, "web/static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
}()

// Page is what every template receives. Dynamic text is only ever emitted through html/template's escaping.
type Page struct {
	Title    string
	Desc     string // <meta name="description"> and link previews; a site-wide default when empty
	Query    string
	Path     string // url-escaped current path, for the sign-in link
	User     *User
	CSRF     string
	Lines    []string
	Link     string
	LinkText string
	Code     string
	Error    string
	Data     any
}

func loadTemplates() *template.Template {
	return template.Must(template.New("").Funcs(templateFuncs()).ParseFS(templateFS, "web/templates/*.html"))
}

func staticHandler() http.Handler {
	sub, _ := fs.Sub(staticFS, "web/static")
	h := http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			// only a version-stamped request (?v=staticVersion, from our own templates) is safe to cache
			// indefinitely: the URL changes whenever the content does, so a stale copy can never be served.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		h.ServeHTTP(w, r)
	})
}

// render executes a template into a buffer first, so an error never leaves half a page on the wire.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	if name == "home.html" {
		if d, ok := p.Data.(listData); ok {
			d.RegistryURL = s.Cfg.PublicURL
			p.Data = d
		}
	}
	if p.User == nil && p.CSRF == "" {
		if u, csrf := s.webUser(r); u != nil {
			p.User, p.CSRF = u, csrf
		}
	}
	p.Path = url.QueryEscape(r.URL.Path)
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, name, p); err != nil {
		s.Log.Error("template", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

func (s *Server) message(w http.ResponseWriter, r *http.Request, status int, title string, lines ...string) {
	s.render(w, r, status, "message.html", Page{Title: title, Lines: lines})
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{"join": strings.Join, "staticVersion": func() string { return staticVersion }}
}
