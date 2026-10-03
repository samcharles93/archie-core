package webui

import "net/http"

// handleHealth is an unauthenticated liveness probe.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleHealthDetailed runs every readiness probe and returns each result and
// the rollup. Authenticated; 503 when no probes are wired.
func (s *Server) handleHealthDetailed(w http.ResponseWriter, r *http.Request) {
	if s.Health == nil {
		http.Error(w, "readiness probes are not configured", http.StatusServiceUnavailable)
		return
	}
	report := s.Health.Run(r.Context())
	writeJSON(w, report)
}
