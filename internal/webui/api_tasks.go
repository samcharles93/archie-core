package webui

import (
	"net/http"
	"strconv"
)

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	statuses, err := s.Store.StatusCounts(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	days, err := s.Store.TokensByDay(ctx, 14)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"statuses": statuses, "workflows": workflows,
		"stages": stages, "tokens_by_day": days,
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Store.Tasks(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, tasks)
}

func (s *Server) handleClearTasks(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.Store.ClearTerminalTasks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"deleted": deleted})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	evs, err := s.Store.TaskEvents(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, evs)
}
