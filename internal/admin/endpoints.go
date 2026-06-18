package admin

import "net/http"

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request)  { stub(w) }
func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request)   { stub(w) }
func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request)     { stub(w) }
func (s *Server) patchEndpoint(w http.ResponseWriter, r *http.Request)   { stub(w) }
func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request)  { stub(w) }
func (s *Server) rotateSecret(w http.ResponseWriter, r *http.Request)    { stub(w) }
