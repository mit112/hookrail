package admin

import "net/http"

func (s *Server) listDLQ(w http.ResponseWriter, r *http.Request)    { stub(w) }
func (s *Server) replayDLQ(w http.ResponseWriter, r *http.Request)  { stub(w) }
