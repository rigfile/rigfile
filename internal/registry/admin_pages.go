package registry

import (
	"net/http"
	"strconv"
	"strings"
)

type adminData struct {
	Held    []HeldVersion
	Reports []Report
}

// admin returns the signed-in administrator, or writes the refusal.
func (s *Server) admin(w http.ResponseWriter, r *http.Request) (*User, string) {
	u, csrf := s.webUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/admin", http.StatusFound)
		return nil, ""
	}
	if !u.IsAdmin {
		s.notFound(w, r) // not a hint that the page exists
		return nil, ""
	}
	return u, csrf
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	u, csrf := s.admin(w, r)
	if u == nil {
		return
	}
	held, _ := s.Store.HeldVersions(r.Context())
	reports, _ := s.Store.OpenReports(r.Context())
	s.render(w, r, http.StatusOK, "admin.html", Page{Title: "Administration", User: u, CSRF: csrf, Data: adminData{Held: held, Reports: reports}})
}

// adminPost is webPost plus the administrator check.
func (s *Server) adminPost(w http.ResponseWriter, r *http.Request) *User {
	u := s.webPost(w, r)
	if u == nil {
		return nil
	}
	if !u.IsAdmin {
		s.notFound(w, r)
		return nil
	}
	return u
}

func (s *Server) adminHeld(w http.ResponseWriter, r *http.Request) {
	u := s.adminPost(w, r)
	if u == nil {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Store.DecideHeld(r.Context(), id, r.PostFormValue("decision") == "release", r.PostFormValue("note"), u); err != nil {
		s.message(w, r, http.StatusNotFound, "Not found", "That version is not waiting for review.")
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminApprove(w http.ResponseWriter, r *http.Request) {
	u := s.adminPost(w, r)
	if u == nil {
		return
	}
	if err := s.Store.ApprovePublic(r.Context(), r.PathValue("owner"), r.PathValue("name"), u); err != nil {
		s.message(w, r, http.StatusNotFound, "Not found", "No such rig.")
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminReport(w http.ResponseWriter, r *http.Request) {
	u := s.adminPost(w, r)
	if u == nil {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Store.ResolveReport(r.Context(), id, r.PostFormValue("status"), u); err != nil {
		s.message(w, r, http.StatusNotFound, "Not found", "That report is not open.")
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminVerify(w http.ResponseWriter, r *http.Request) {
	u := s.adminPost(w, r)
	if u == nil {
		return
	}
	if err := s.Store.SetVerified(r.Context(), strings.TrimSpace(r.PostFormValue("login")), r.PostFormValue("kind"), r.PostFormValue("note"), u); err != nil {
		s.message(w, r, http.StatusBadRequest, "Not verified", "Check the login and the kind.")
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
