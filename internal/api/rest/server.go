package rest

import (
	"net/http"
)

type Server struct{ mux *http.ServeMux }

func New() *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request){ w.WriteHeader(200); _,_ = w.Write([]byte("ok")) })
	mux.HandleFunc("/balances", func(w http.ResponseWriter, r *http.Request){ w.WriteHeader(200) })
	mux.HandleFunc("/open_positions", func(w http.ResponseWriter, r *http.Request){ w.WriteHeader(200) })
	return &Server{mux:mux}
}

func (s *Server) Handler() http.Handler { return s.mux }
