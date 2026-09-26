package registry

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func (s *Server) orgRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/orgs", s.apiOrgCreate)
	mux.HandleFunc("GET /v1/me/orgs", s.apiMyOrgs)
	mux.HandleFunc("GET /v1/orgs/{org}", s.apiOrgGet)
	mux.HandleFunc("GET /v1/orgs/{org}/members", s.apiOrgMembers)
	mux.HandleFunc("PUT /v1/orgs/{org}/members/{login}", s.apiOrgSetMember)
	mux.HandleFunc("DELETE /v1/orgs/{org}/members/{login}", s.apiOrgRemoveMember)
}

func (s *Server) orgLimit(w http.ResponseWriter, r *http.Request) bool {
	return s.limit(w, r, "org", 60, 20)
}

func (s *Server) apiOrgCreate(w http.ResponseWriter, r *http.Request) {
	if !s.orgLimit(w, r) {
		return
	}
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	var in struct{ Login, Name string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		apiError(w, http.StatusBadRequest, "bad request")
		return
	}
	o, err := s.Store.CreateOrg(r.Context(), u, in.Login, in.Name)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"login": o.Login, "name": o.Name, "role": "owner"})
}

// orgError maps store errors. Naming a taken name is a 409 with a message (a name is public anyway); everything about an
// organisation the caller may not see is the same 404.
func orgError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNameTaken), errors.Is(err, ErrConflict):
		apiError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrBadInput):
		apiError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrForbidden):
		apiError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrNotFound):
		apiError(w, http.StatusNotFound, "no such organisation or member")
	default:
		apiError(w, http.StatusInternalServerError, "could not do that")
	}
}

func (s *Server) apiMyOrgs(w http.ResponseWriter, r *http.Request) {
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	list, err := s.Store.UserOrgs(r.Context(), u)
	if err != nil {
		orgError(w, err)
		return
	}
	out := make([]map[string]string, 0, len(list))
	for _, m := range list {
		out = append(out, map[string]string{"login": m.Login, "role": m.Role})
	}
	writeJSON(w, http.StatusOK, map[string]any{"orgs": out})
}

func (s *Server) apiOrgGet(w http.ResponseWriter, r *http.Request) {
	v, u, ok := s.viewer(w, r)
	if !ok {
		return
	}
	o, err := s.Store.OrgByLogin(r.Context(), r.PathValue("org"))
	if err != nil {
		orgError(w, err)
		return
	}
	rigs, _ := s.Store.OrgRigs(r.Context(), o, v)
	names := make([]string, 0, len(rigs))
	for _, x := range rigs {
		names = append(names, x.Owner+"/"+x.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"login": o.Login, "name": o.Name, "role": s.Store.OrgRole(r.Context(), u, o.ID), "rigs": names})
}

func (s *Server) apiOrgMembers(w http.ResponseWriter, r *http.Request) {
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	o, err := s.Store.OrgByLogin(r.Context(), r.PathValue("org"))
	if err != nil {
		orgError(w, err)
		return
	}
	ms, err := s.Store.Members(r.Context(), u, o)
	if err != nil {
		orgError(w, err)
		return
	}
	out := make([]map[string]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, map[string]string{"login": m.Login, "role": m.Role})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (s *Server) apiOrgSetMember(w http.ResponseWriter, r *http.Request) {
	if !s.orgLimit(w, r) {
		return
	}
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	var in struct{ Role string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil {
		apiError(w, http.StatusBadRequest, "bad request")
		return
	}
	o, err := s.Store.OrgByLogin(r.Context(), r.PathValue("org"))
	if err != nil {
		orgError(w, err)
		return
	}
	if err := s.Store.SetMember(r.Context(), u, o, strings.ToLower(r.PathValue("login")), in.Role); err != nil {
		orgError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiOrgRemoveMember(w http.ResponseWriter, r *http.Request) {
	if !s.orgLimit(w, r) {
		return
	}
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	o, err := s.Store.OrgByLogin(r.Context(), r.PathValue("org"))
	if err != nil {
		orgError(w, err)
		return
	}
	if err := s.Store.RemoveMember(r.Context(), u, o, strings.ToLower(r.PathValue("login"))); err != nil {
		orgError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
