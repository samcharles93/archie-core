package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/presence"
)

type serviceView struct {
	presence.Service
	Applied []applyStatusRecord `json:"applied"`
}

// handleServices lists every expected service with its presence and the
// control-plane resource versions it applied. Both live in the State Store, so
// when it cannot be read the list still answers, naming the store as down.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if s.Presence == nil {
		http.Error(w, "presence unavailable", http.StatusServiceUnavailable)
		return
	}
	mesh := presence.Unreachable()
	if records, err := s.Presence.ListPresence(r.Context()); err == nil {
		mesh = presence.Mesh(records, s.clock())
	}
	// Apply status is decoration on the list; without it the list still stands.
	applied, _ := s.readApplyStatus(r.Context())
	out := make([]serviceView, 0, len(mesh))
	for _, service := range mesh {
		view := serviceView{Service: service, Applied: []applyStatusRecord{}}
		for _, record := range applied.Records {
			if record.Process == service.Service {
				view.Applied = append(view.Applied, record)
			}
		}
		out = append(out, view)
	}
	writeJSON(w, out)
}
