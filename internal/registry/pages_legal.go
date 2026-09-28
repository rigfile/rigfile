package registry

import (
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

//go:embed web/legal/*.md
var legalFS embed.FS

var legalTitles = map[string]string{"terms": "Terms of Service", "acceptable-use": "Acceptable Use Policy", "takedown": "Takedown and abuse policy", "privacy": "Privacy notice"}

// pageLegal serves the policy documents. They are DRAFTS that need a lawyer's review before launch (docs/owner-checklist.md),
// and every page says so.
func (s *Server) pageLegal(w http.ResponseWriter, r *http.Request) {
	doc := r.PathValue("doc")
	title, ok := legalTitles[doc]
	if !ok {
		s.notFound(w, r)
		return
	}
	b, err := legalFS.ReadFile("web/legal/" + doc + ".md")
	if err != nil {
		s.notFound(w, r)
		return
	}
	_, u, csrf := s.pageViewer(r)
	s.render(w, r, http.StatusOK, "legal.html", Page{Title: title, User: u, CSRF: csrf, Data: struct {
		Title string
		Body  template.HTML
	}{title, renderMarkdown(string(b))}})
}

type reportData struct {
	Rig, Version string
	Reasons      []string
	Sent         bool
}

func (s *Server) reportForm(w http.ResponseWriter, r *http.Request) {
	u, csrf := s.webUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	s.render(w, r, http.StatusOK, "report.html", Page{Title: "Report a rig", User: u, CSRF: csrf,
		Data: reportData{Rig: trunc(r.URL.Query().Get("rig"), 100), Version: trunc(r.URL.Query().Get("version"), 40), Reasons: ReportReasons}})
}

func (s *Server) reportSubmit(w http.ResponseWriter, r *http.Request) {
	u := s.webPost(w, r)
	if u == nil {
		return
	}
	if !s.Lim.Allow("report|"+strconv.FormatInt(u.ID, 10), 6, 5) || !s.Lim.Allow("reportip|"+clientIP(r, s.Cfg.TrustProxy), 20, 10) {
		s.message(w, r, http.StatusTooManyRequests, "Slow down", "Too many reports in a short time. Try again later.")
		return
	}
	owner, name, _ := strings.Cut(strings.TrimSpace(r.PostFormValue("rig")), "/")
	if !ownerRe.MatchString(owner) || !rigNameRe.MatchString(name) {
		s.message(w, r, http.StatusBadRequest, "Report not sent", "Give the rig as owner/name.")
		return
	}
	if err := s.Store.CreateReport(r.Context(), u, owner, name, trunc(strings.TrimSpace(r.PostFormValue("version")), 40), r.PostFormValue("reason"), r.PostFormValue("details")); err != nil {
		s.message(w, r, http.StatusBadRequest, "Report not sent", "We could not find that rig or the reason was not valid.")
		return
	}
	s.message(w, r, http.StatusOK, "Report received", "Thank you. An administrator will look at it. See the takedown and abuse policy for what happens next.")
}
