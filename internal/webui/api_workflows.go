package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

const workflowDefinitionsKind = "workflow-definitions"

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
	definitions, err := s.workflowDefinitions(ctx)
	if err != nil {
		http.Error(w, "workflow definitions unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]any{
		"workflows":   workflows,
		"stages":      stages,
		"definitions": definitions,
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
	available, err := s.hasWorkflow(r.Context(), request.Workflow)
	if err != nil {
		http.Error(w, "workflow definitions unavailable", http.StatusServiceUnavailable)
		return
	}
	if !available {
		http.Error(w, "workflow is not enabled", http.StatusConflict)
		return
	}
	taskID, err := s.WorkRequests.CreateTask(r.Context(), messaging.SpawnRequest{
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

func (s *Server) hasWorkflow(ctx context.Context, id string) (bool, error) {
	_, found, err := s.workflowEntry(ctx, id)
	return found, err
}

// workflowEntry returns the stored definition of workflow id.
func (s *Server) workflowEntry(ctx context.Context, id string) (task.WorkflowDefinitionEntry, bool, error) {
	collection, err := s.workflowCollection(ctx)
	if err != nil {
		return task.WorkflowDefinitionEntry{}, false, err
	}
	entry, found := collection.DefinitionByID(id)
	return entry, found, nil
}

func (s *Server) workflowCollection(ctx context.Context) (task.WorkflowDefinitionCollection, error) {
	if s.ControlPlane == nil {
		return task.WorkflowDefinitionCollection{}, nil
	}
	response, err := s.ControlPlane.Query(ctx, &controlpb.QueryRequest{Kind: workflowDefinitionsKind})
	if err != nil {
		return task.WorkflowDefinitionCollection{}, err
	}
	if response.Resource == nil {
		return task.WorkflowDefinitionCollection{}, errors.New("workflow definitions missing")
	}
	var collection task.WorkflowDefinitionCollection
	if err := json.Unmarshal(response.Resource.ValueJson, &collection); err != nil {
		return task.WorkflowDefinitionCollection{}, err
	}
	return collection, nil
}

func (s *Server) workflowDefinitions(ctx context.Context) ([]task.Definition, error) {
	if s.ControlPlane == nil {
		return nil, nil
	}
	collection, err := s.workflowCollection(ctx)
	if err != nil {
		return nil, err
	}
	definitions := make([]task.Definition, 0, len(collection.Definitions))
	for _, entry := range collection.Definitions {
		definition := task.Definition{ID: entry.ID, Name: entry.ID, Origin: "database", Enabled: true, Repository: task.RepositoryRequired}
		if iface, err := task.ParseWorkflowInterface(entry.YAML); err == nil {
			definition.Inputs, definition.Repository = iface.Inputs, iface.RepositoryMode()
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}
