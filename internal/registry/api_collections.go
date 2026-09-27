package registry

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) collectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/collections", s.apiCollectionCreate)
	mux.HandleFunc("GET /v1/collections/{owner}/{slug}", s.apiCollectionGet)
	mux.HandleFunc("DELETE /v1/collections/{owner}/{slug}", s.apiCollectionDelete)
	mux.HandleFunc("PUT /v1/collections/{owner}/{slug}/items", s.apiCollectionAdd)
	mux.HandleFunc("DELETE /v1/collections/{owner}/{slug}/items/{rigowner}/{rigname}", s.apiCollectionRemove)
	mux.HandleFunc("GET /v1/users/{login}/collections", s.apiUserCollections)
	mux.HandleFunc("GET /c/{owner}/{slug}", s.pageCollection)
}

type collectionJSON struct {
	Owner       string `json:"owner"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	URL         string `json:"url,omitempty"`
	Rigs        []any  `json:"rigs,omitempty"`
}

func (s *Server) collectionURL(c *Collection) string {
	return strings.TrimRight(s.Cfg.PublicURL, "/") + "/c/" + url.PathEscape(c.Owner) + "/" + url.PathEscape(c.Slug)
}

func toJSON(s *Server, c *Collection) collectionJSON {
	return collectionJSON{Owner: c.Owner, Slug: c.Slug, Title: c.Title, Description: c.Description, Visibility: c.Visibility, URL: s.collectionURL(c)}
}

// collectionError maps a store error to a status. Missing and private are the same 404.
func collectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		apiError(w, http.StatusNotFound, "no such collection or rig")
	case errors.Is(err, ErrForbidden):
		apiError(w, http.StatusForbidden, "this is not yours")
	case errors.Is(err, ErrBadInput):
		apiError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrConflict):
		apiError(w, http.StatusConflict, err.Error())
	default:
		apiError(w, http.StatusInternalServerError, "could not do that")
	}
}

// mine authenticates the caller and requires the path's owner to be them.
func (s *Server) mine(w http.ResponseWriter, r *http.Request) *User {
	if !s.limit(w, r, "collection", 60, 20) {
		return nil
	}
	u := s.requireToken(w, r)
	if u == nil {
		return nil
	}
	if !strings.EqualFold(r.PathValue("owner"), u.Login) {
		apiError(w, http.StatusForbidden, "you can only change your own collections")
		return nil
	}
	return u
}

func (s *Server) apiCollectionCreate(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "collection", 60, 20) {
		return
	}
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	var in struct{ Slug, Title, Description, Visibility string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		apiError(w, http.StatusBadRequest, "bad request")
		return
	}
	c, err := s.Store.CreateCollection(r.Context(), u, NewCollection{in.Slug, in.Title, in.Description, in.Visibility})
	if err != nil {
		collectionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toJSON(s, c))
}

func (s *Server) apiCollectionGet(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.viewer(w, r)
	if !ok {
		return
	}
	c, err := s.Store.GetCollection(r.Context(), r.PathValue("owner"), r.PathValue("slug"), v)
	if err != nil {
		collectionError(w, err)
		return
	}
	items, err := s.Store.CollectionItems(r.Context(), c, v)
	if err != nil {
		collectionError(w, err)
		return
	}
	out := toJSON(s, c)
	out.Rigs = []any{}
	for _, it := range items {
		out.Rigs = append(out.Rigs, map[string]any{"owner": it.Owner, "name": it.Name, "description": it.Description, "stars": it.Stars, "latest": it.Latest, "note": it.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiUserCollections(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.viewer(w, r)
	if !ok {
		return
	}
	cs, err := s.Store.UserCollections(r.Context(), r.PathValue("login"), v)
	if err != nil {
		collectionError(w, err)
		return
	}
	out := make([]collectionJSON, 0, len(cs))
	for i := range cs {
		out = append(out, toJSON(s, &cs[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": out})
}

func (s *Server) apiCollectionAdd(w http.ResponseWriter, r *http.Request) {
	u := s.mine(w, r)
	if u == nil {
		return
	}
	var in struct{ Rig, Note string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		apiError(w, http.StatusBadRequest, "bad request")
		return
	}
	owner, name, ok := strings.Cut(strings.ToLower(strings.TrimSpace(in.Rig)), "/")
	if !ok || !ownerRe.MatchString(owner) || !rigNameRe.MatchString(name) {
		apiError(w, http.StatusBadRequest, "rig must be owner/name")
		return
	}
	if err := s.Store.AddToCollection(r.Context(), u, r.PathValue("slug"), owner, name, in.Note); err != nil {
		collectionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiCollectionRemove(w http.ResponseWriter, r *http.Request) {
	u := s.mine(w, r)
	if u == nil {
		return
	}
	if err := s.Store.RemoveFromCollection(r.Context(), u, r.PathValue("slug"), r.PathValue("rigowner"), r.PathValue("rigname")); err != nil {
		collectionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiCollectionDelete(w http.ResponseWriter, r *http.Request) {
	u := s.mine(w, r)
	if u == nil {
		return
	}
	if err := s.Store.DeleteCollection(r.Context(), u, r.PathValue("slug")); err != nil {
		collectionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type collectionPage struct {
	C     *Collection
	Items []CollectionItem
	Mine  bool
}

func (s *Server) pageCollection(w http.ResponseWriter, r *http.Request) {
	v, u, csrf := s.pageViewer(r)
	c, err := s.Store.GetCollection(r.Context(), r.PathValue("owner"), r.PathValue("slug"), v)
	if err != nil {
		s.notFound(w, r)
		return
	}
	items, err := s.Store.CollectionItems(r.Context(), c, v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, http.StatusOK, "collection.html", Page{Title: c.Title, User: u, CSRF: csrf, Data: collectionPage{C: c, Items: items, Mine: u != nil && u.ID == c.OwnerID}})
}
