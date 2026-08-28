package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/taskstate"
)

// handleTaskMeta serves the lifecycle presentation catalog: the status
// vocabulary (with label, pill severity, "needs you" grouping) and the operator
// action controls (with label, button variant, confirm prompt). Everything the
// dashboard can present is derived here, so the frontend never has to keep a
// hand-synced copy of the vocabulary or the control set.
func (s *Server) handleTaskMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"statuses": taskstate.Statuses(),
		"actions":  taskstate.ActionCatalog(),
	})
}
