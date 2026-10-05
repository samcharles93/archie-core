package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
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
	if request.Identity == "" || request.Workflow == "" || request.Title == "" || request.Instructions == "" {
		http.Error(w, "identity, workflow, title, and instructions are required", http.StatusBadRequest)
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
	owner, repo, status, err := s.workRequestRepository(r.Context(), iface.RepositoryMode(), request.Repository)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	created, err := s.Store.EnqueueChatTask(r.Context(), owner, repo, request.Title, request.Instructions, request.Workflow, request.Identity, "", request.Inputs)
	if err != nil {
		s.logf("work request enqueue failed", "err", err)
		http.Error(w, "work request rejected", http.StatusBadRequest)
		return
	}
	s.emit(r.Context(), events.Event{Kind: events.KindWorkRequestSubmitted, TaskID: created.ID, Repo: request.Repository, Workflow: request.Workflow, Detail: request.Title})
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"ok": true, "task_id": created.ID})
}

// workRequestRepository checks a requested "owner/name" against the
// workflow's repository mode and the configured repositories, and returns it
// split. A workflow that takes none refuses one; one that requires it refuses
// its absence.
func (s *Server) workRequestRepository(ctx context.Context, mode task.RepositoryMode, requested string) (owner, repo string, status int, err error) {
	switch {
	case requested == "" && mode == task.RepositoryRequired:
		return "", "", http.StatusBadRequest, errors.New("this workflow needs a repository")
	case requested == "":
		return "", "", 0, nil
	case mode == task.RepositoryNone:
		return "", "", http.StatusBadRequest, errors.New("this workflow takes no repository")
	}
	owner, repo, ok := strings.Cut(requested, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", http.StatusBadRequest, errors.New("repository must be owner/name")
	}
	view, found, err := s.configSource()(ctx)
	if err != nil || !found {
		return "", "", http.StatusServiceUnavailable, errors.New("configured repositories unavailable")
	}
	if !viewHasRepo(view, owner, repo) {
		return "", "", http.StatusBadRequest, fmt.Errorf("repository %s/%s is not configured", owner, repo)
	}
	return owner, repo, 0, nil
}

// viewHasRepo reports whether the default identity or any named identity
// manages owner/repo.
func viewHasRepo(view ConfigView, owner, repo string) bool {
	for _, r := range view.Repositories {
		if strings.EqualFold(r.Owner, owner) && strings.EqualFold(r.Name, repo) {
			return true
		}
	}
	for _, identity := range view.Identities {
		for _, r := range identity.Repos {
			if strings.EqualFold(r.Owner, owner) && strings.EqualFold(r.Name, repo) {
				return true
			}
		}
	}
	return false
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
