package registry

import (
	"bytes"
	"embed"
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

// Page is what every template receives. Dynamic text is only ever emitted through html/template's escaping.
type Page struct {
	Title    string
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
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

// render executes a template into a buffer first, so an error never leaves half a page on the wire.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
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

func templateFuncs() template.FuncMap { return template.FuncMap{"join": strings.Join} }
