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
// control-plane resource versions it applied.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if s.Presence == nil {
		http.Error(w, "presence unavailable", http.StatusServiceUnavailable)
		return
	}
	records, err := s.Presence.ListPresence(r.Context())
	if err != nil {
		http.Error(w, "presence unavailable", http.StatusServiceUnavailable)
		return
	}
	applied, err := s.readApplyStatus(r.Context())
	if err != nil {
		http.Error(w, "apply status unavailable", http.StatusServiceUnavailable)
		return
	}
	mesh := presence.Mesh(records, s.clock())
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
