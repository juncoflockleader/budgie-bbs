package httpapi

import (
	"net/http"
)

// handleHealthz is the liveness probe. Always returns 200 OK.
// A 200 means the process is running; it says nothing about DB connectivity.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadyz is the readiness probe.
// Returns 200 when the database is reachable, 503 otherwise.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.core.DB.PingContext(r.Context()); err != nil {
		http.Error(w, "db unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
