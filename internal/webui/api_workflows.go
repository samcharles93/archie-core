package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
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
	// Inputs assigns the inputs the named workflow declares -- pr-review's
	// pr_number, for instance. The handler checks the assignment against the
	// declaration before the task is admitted, so a request that could only
	// fail at the workflow's first stage is refused here instead
	Inputs map[string]any `json:"inputs"`
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
	iface, enabled, err := s.enabledWorkflowInterface(r.Context(), request.Workflow)
	if err != nil {
		http.Error(w, "workflow definitions unavailable", http.StatusServiceUnavailable)
		return
	}
	if !enabled {
		http.Error(w, "workflow is not enabled", http.StatusConflict)
		return
	}
	if err := iface.CheckInputs(request.Inputs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	taskID, err := s.WorkRequests.CreateTask(r.Context(), messaging.SpawnRequest{
		Identity: request.Identity, Repo: request.Repository, Workflow: request.Workflow,
		Title: request.Title, Body: request.Instructions, Inputs: request.Inputs,
	})
	if err != nil {
		http.Error(w, "work request rejected", http.StatusBadRequest)
		return
	}
	s.emit(r.Context(), events.Event{Kind: events.KindWorkRequestSubmitted, TaskID: taskID, Repo: request.Repository, Workflow: request.Workflow, Detail: request.Title})
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"ok": true, "task_id": taskID})
}

// enabledWorkflowInterface returns a workflow's declared interface and whether
// it is defined and enabled for the caller's org.
func (s *Server) enabledWorkflowInterface(ctx context.Context, id string) (task.WorkflowInterface, bool, error) {
	entry, found, err := s.workflowEntry(ctx, id)
	if err != nil || !found {
		return task.WorkflowInterface{}, false, err
	}
	enablement, _, err := s.workflowEnablement(ctx)
	if err != nil || !enablement.Enabled(org.OrgFromContext(ctx), id) {
		return task.WorkflowInterface{}, false, err
	}
	iface, err := task.ParseWorkflowInterface(entry.YAML)
	if err != nil {
		return task.WorkflowInterface{}, false, err
	}
	return iface, true, nil
}

func (s *Server) hasWorkflowDefinition(ctx context.Context, id string) (bool, error) {
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
	response, err := s.ControlPlane.Query(ctx, controlplanerpc.QueryRequest(ctx, workflowDefinitionsKind))
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
	enablement, _, err := s.workflowEnablement(ctx)
	if err != nil {
		return nil, err
	}
	definitions := make([]task.Definition, 0, len(collection.Definitions))
	for _, entry := range collection.Definitions {
		definition := task.Definition{ID: entry.ID, Name: entry.ID, Origin: "database", Enabled: enablement.Enabled(org.OrgFromContext(ctx), entry.ID), Repository: task.RepositoryRequired}
		if iface, err := task.ParseWorkflowInterface(entry.YAML); err == nil {
			definition.Inputs, definition.Repository = iface.Inputs, iface.RepositoryMode()
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}
