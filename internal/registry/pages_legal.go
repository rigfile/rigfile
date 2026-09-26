package registry

import "net/http"

func (s *Server) pageLegal(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) }

func (s *Server) reportForm(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) }

func (s *Server) reportSubmit(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) }
