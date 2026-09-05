package webui

import "net/http"

// handleHealth is a liveness probe: the process is up and able to serve a
// request. It deliberately bypasses requireToken, like /healthz -- a local
// orchestration tool (systemd, a watchdog, a container healthcheck) must be
// able to ask "is the process alive?" without the dashboard token. It does no
// subsystem work and always answers 200 when the process can respond at all.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleHealthDetailed is the authenticated readiness surface. It runs every
// registered readiness probe and returns each subsystem's live result plus
// the overall rollup, so an operator can see exactly which subsystem is
// degraded rather than only that the daemon is unhealthy.
//
// It requires the dashboard token (and is mounted behind requireToken) because
// it exposes real subsystem state -- a remote caller should not be able to
// probe the disk layout or provider reachability unauthenticated. When no
// probe registry is wired, it answers 503 rather than pretending to know.
func (s *Server) handleHealthDetailed(w http.ResponseWriter, r *http.Request) {
	if s.Health == nil {
		http.Error(w, "readiness probes are not configured", http.StatusServiceUnavailable)
		return
	}
	report := s.Health.Run(r.Context())
	writeJSON(w, report)
}
