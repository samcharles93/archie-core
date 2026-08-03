package webui

import "net/http"

// handleWorkflows returns per-workflow and per-stage statistics: run counts,
// outcome breakdowns and spend per workflow, plus duration and failure counts
// per stage -- "where does it get stuck" for the workflows page.
func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workflows, err := s.Store.WorkflowStats(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stages, err := s.Store.StageStats(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"workflows": workflows,
		"stages":    stages,
	})
}
