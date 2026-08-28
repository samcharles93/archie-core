package webui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
)

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
		"workflows":   workflows,
		"stages":      stages,
		"definitions": s.Workflows,
	})
}

type workRequest struct {
	Identity     string `json:"identity"`
	Repository   string `json:"repository"`
	Workflow     string `json:"workflow"`
	Title        string `json:"title"`
	Instructions string `json:"instructions"`
}

func (s *Server) handleWorkRequest(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.WorkRequests == nil {
		http.Error(w, "work intake unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var request workRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "work request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid work request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid work request", http.StatusBadRequest)
		return
	}
	request.Identity = strings.TrimSpace(request.Identity)
	request.Repository = strings.TrimSpace(request.Repository)
	request.Workflow = strings.TrimSpace(request.Workflow)
	request.Title = strings.TrimSpace(request.Title)
	request.Instructions = strings.TrimSpace(request.Instructions)
	if request.Identity == "" || request.Repository == "" || request.Workflow == "" || request.Title == "" || request.Instructions == "" {
		http.Error(w, "identity, repository, workflow, title, and instructions are required", http.StatusBadRequest)
		return
	}
	if !s.hasWorkflow(request.Workflow) {
		http.Error(w, "workflow is not enabled", http.StatusConflict)
		return
	}
	taskID, err := s.WorkRequests.CreateTask(r.Context(), gateway.SpawnRequest{
		Identity: request.Identity, Repo: request.Repository, Workflow: request.Workflow,
		Title: request.Title, Body: request.Instructions,
	})
	if err != nil {
		http.Error(w, "work request rejected", http.StatusBadRequest)
		return
	}
	s.emit(r.Context(), events.Event{Kind: events.KindWorkRequestSubmitted, TaskID: taskID, Repo: request.Repository, Workflow: request.Workflow, Detail: request.Title})
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"ok": true, "task_id": taskID})
}

func (s *Server) hasWorkflow(id string) bool {
	for _, workflow := range s.Workflows {
		if workflow.ID == id && workflow.Enabled {
			return true
		}
	}
	return false
}
