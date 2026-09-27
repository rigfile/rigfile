package registry

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The web forms for collections and organisations. They call the same store methods as the API, so every rule (limits,
// ownership, visibility, roles) is the API's; the forms add only the browser protections webPost already provides: a
// session, a same-origin request and a CSRF token. There is no script.

func (s *Server) manageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /manage/collections", s.formCollectionCreate)
	mux.HandleFunc("POST /manage/collections/{slug}/add", s.formCollectionAdd)
	mux.HandleFunc("POST /manage/collections/{slug}/remove", s.formCollectionRemove)
	mux.HandleFunc("POST /manage/collections/{slug}/delete", s.formCollectionDelete)
	mux.HandleFunc("POST /manage/orgs", s.formOrgCreate)
	mux.HandleFunc("POST /manage/orgs/{org}/member", s.formOrgMember)
	mux.HandleFunc("POST /manage/orgs/{org}/remove", s.formOrgRemove)
}

// manageUser is webPost plus a per-user rate limit for these writes.
func (s *Server) manageUser(w http.ResponseWriter, r *http.Request) *User {
	u := s.webPost(w, r)
	if u == nil {
		return nil
	}
	if !s.Lim.Allow("manage|"+strconv.FormatInt(u.ID, 10), 60, 20) {
		w.Header().Set("Retry-After", "60")
		s.message(w, r, http.StatusTooManyRequests, "Slow down", "Too many changes in a short time. Wait a minute and try again.")
		return nil
	}
	return u
}

// formError turns a store error into a page. Missing and private are one message, as everywhere else.
func (s *Server) formError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		s.message(w, r, http.StatusNotFound, "Not found", "There is no such collection, rig, organisation or member (or you may not see it).")
	case errors.Is(err, ErrForbidden):
		s.message(w, r, http.StatusForbidden, "Not allowed", err.Error())
	case errors.Is(err, ErrBadInput):
		s.message(w, r, http.StatusBadRequest, "Please check the form", err.Error())
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNameTaken):
		s.message(w, r, http.StatusConflict, "That cannot be done", err.Error())
	default:
		s.Log.Error("manage form", "err", err)
		s.message(w, r, http.StatusInternalServerError, "Something went wrong", "The change was not made. Try again later.")
	}
}

func (s *Server) formCollectionCreate(w http.ResponseWriter, r *http.Request) {
	u := s.manageUser(w, r)
	if u == nil {
		return
	}
	vis := "public"
	if r.PostFormValue("private") != "" {
		vis = "private"
	}
	c, err := s.Store.CreateCollection(r.Context(), u, NewCollection{Slug: strings.ToLower(strings.TrimSpace(r.PostFormValue("slug"))), Title: r.PostFormValue("title"), Description: r.PostFormValue("description"), Visibility: vis})
	if err != nil {
		s.formError(w, r, err)
		return
	}
	http.Redirect(w, r, "/c/"+url.PathEscape(c.Owner)+"/"+url.PathEscape(c.Slug), http.StatusSeeOther)
}

func (s *Server) collectionBack(u *User, slug string) string {
	return "/c/" + url.PathEscape(u.Login) + "/" + url.PathEscape(slug)
}

func (s *Server) formCollectionAdd(w http.ResponseWriter, r *http.Request) {
	u := s.manageUser(w, r)
	if u == nil {
		return
	}
	owner, name, ok := strings.Cut(strings.ToLower(strings.TrimSpace(r.PostFormValue("rig"))), "/")
	if !ok || !ownerRe.MatchString(owner) || !rigNameRe.MatchString(name) {
		s.formError(w, r, ErrBadInput)
		return
	}
	if err := s.Store.AddToCollection(r.Context(), u, r.PathValue("slug"), owner, name, r.PostFormValue("note")); err != nil {
		s.formError(w, r, err)
		return
	}
	http.Redirect(w, r, s.collectionBack(u, r.PathValue("slug")), http.StatusSeeOther)
}

func (s *Server) formCollectionRemove(w http.ResponseWriter, r *http.Request) {
	u := s.manageUser(w, r)
	if u == nil {
		return
	}
	owner, name, ok := strings.Cut(strings.ToLower(strings.TrimSpace(r.PostFormValue("rig"))), "/")
	if !ok {
		s.formError(w, r, ErrBadInput)
		return
	}
	if err := s.Store.RemoveFromCollection(r.Context(), u, r.PathValue("slug"), owner, name); err != nil {
		s.formError(w, r, err)
		return
	}
	http.Redirect(w, r, s.collectionBack(u, r.PathValue("slug")), http.StatusSeeOther)
}

func (s *Server) formCollectionDelete(w http.ResponseWriter, r *http.Request) {
	u := s.manageUser(w, r)
	if u == nil {
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.message(w, r, http.StatusBadRequest, "Please confirm", "Tick the box to confirm that you want to delete the collection.")
		return
	}
	if err := s.Store.DeleteCollection(r.Context(), u, r.PathValue("slug")); err != nil {
		s.formError(w, r, err)
		return
	}
	http.Redirect(w, r, "/u/"+url.PathEscape(u.Login), http.StatusSeeOther)
}

func (s *Server) formOrgCreate(w http.ResponseWriter, r *http.Request) {
	u := s.manageUser(w, r)
	if u == nil {
		return
	}
	o, err := s.Store.CreateOrg(r.Context(), u, r.PostFormValue("login"), r.PostFormValue("name"))
	if err != nil {
		s.formError(w, r, err)
		return
	}
	http.Redirect(w, r, "/u/"+url.PathEscape(o.Login), http.StatusSeeOther)
}

func (s *Server) formOrgMember(w http.ResponseWriter, r *http.Request) {
	u := s.manageUser(w, r)
	if u == nil {
		return
	}
	o, err := s.Store.OrgByLogin(r.Context(), r.PathValue("org"))
	if err != nil {
		s.formError(w, r, err)
		return
	}
	if err := s.Store.SetMember(r.Context(), u, o, strings.ToLower(strings.TrimSpace(r.PostFormValue("login"))), r.PostFormValue("role")); err != nil {
		s.formError(w, r, err)
		return
	}
	http.Redirect(w, r, "/u/"+url.PathEscape(o.Login), http.StatusSeeOther)
}

func (s *Server) formOrgRemove(w http.ResponseWriter, r *http.Request) {
	u := s.manageUser(w, r)
	if u == nil {
		return
	}
	o, err := s.Store.OrgByLogin(r.Context(), r.PathValue("org"))
	if err != nil {
		s.formError(w, r, err)
		return
	}
	if err := s.Store.RemoveMember(r.Context(), u, o, strings.ToLower(strings.TrimSpace(r.PostFormValue("login")))); err != nil {
		s.formError(w, r, err)
		return
	}
	http.Redirect(w, r, "/u/"+url.PathEscape(o.Login), http.StatusSeeOther)
}
