package http

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/toolhub/toolhub/internal/core"
)

// Server represents the HTTP server
type Server struct {
	config *core.Config
	server *http.Server
}

// NewServer creates a new HTTP server
func NewServer(config *core.Config) *Server {
	s := &Server{
		config: config,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthHandler)
	mux.HandleFunc("/runs", s.runsHandler)
	mux.HandleFunc("/issues", s.issuesHandler)

	s.server = &http.Server{
		Addr:    config.HTTPListen,
		Handler: mux,
	}

	return s
}

// Start starts the HTTP server
func (s *Server) Start() error {
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Health check handler
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"status": "ok",
		"phase":  "A",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Runs handler
func (s *Server) runsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// Create a new run
		response := map[string]string{
			"status": "created",
			"run_id": "run_001",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	case http.MethodGet:
		// List runs
		response := map[string]interface{}{
			"runs": []map[string]string{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Issues handler
func (s *Server) issuesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// Create a new issue
		response := map[string]string{
			"status":     "created",
			"issue_id":   "issue_001",
			"github_url": "https://github.com/...",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
