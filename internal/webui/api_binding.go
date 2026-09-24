package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// bindingRequest is the wire shape POST /api/bindings and
// PATCH /api/bindings/{id} both accept. ID/Version/Status/CreatedAt/UpdatedAt
// are server-assigned (or server-driven), never taken from the request body.
type bindingRequest struct {
	Name      string          `json:"name"`
	Matcher   binding.Matcher `json:"matcher"`
	MappingID string          `json:"mapping_id"`
	Filter    string          `json:"filter"`
	Workflow  string          `json:"workflow"`
	// Owner and Repo optionally pin the binding to one configured repo,
	// for multi-repo deployments. Both empty is valid (falls back to the
	// daemon's single-configured-repo behaviour); binding.Validate
	// rejects setting only one.
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// RepoParam takes the repository from a mapped parameter instead.
	RepoParam string                         `json:"repo_param"`
	Inputs    map[string]binding.InputSource `json:"inputs"`
}

// bindingView is a listed binding with its source's signing state, so the
// list can flag a binding that dispatches unsigned events.
type bindingView struct {
	binding.Binding
	Unsigned bool `json:"unsigned"`
}

func (s *Server) handleBindingsList(w http.ResponseWriter, r *http.Request) {
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	bindings, err := s.Bindings.ListBindings(r.Context())
	if err != nil {
		s.Log.Error("list bindings", "err", err)
		http.Error(w, "list bindings failed", http.StatusInternalServerError)
		return
	}
	unsigned, err := s.unsignedSources(r.Context())
	if err != nil {
		s.Log.Error("list sources", "err", err)
		http.Error(w, "list bindings failed", http.StatusInternalServerError)
		return
	}
	views := make([]bindingView, 0, len(bindings))
	for _, b := range bindings {
		views = append(views, bindingView{Binding: b, Unsigned: unsigned[b.Matcher.Source]})
	}
	writeJSON(w, map[string]any{"bindings": views})
}

func (s *Server) handleBindingCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	var req bindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	b, ok := s.checkedBinding(w, r, "", req)
	if !ok {
		return
	}
	id, err := s.Bindings.InsertBinding(r.Context(), b)
	if err != nil {
		s.Log.Error("insert binding", "err", err)
		http.Error(w, "create binding failed", http.StatusInternalServerError)
		return
	}
	created, err := s.Bindings.GetBinding(r.Context(), id)
	if err != nil || created == nil {
		s.Log.Error("get binding after insert", "err", err, "id", id)
		http.Error(w, "create binding failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

func (s *Server) handleBindingGet(w http.ResponseWriter, r *http.Request) {
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "invalid binding id", http.StatusBadRequest)
		return
	}
	b, err := s.Bindings.GetBinding(r.Context(), id)
	if err != nil {
		s.Log.Error("get binding", "err", err, "id", id)
		http.Error(w, "get binding failed", http.StatusInternalServerError)
		return
	}
	if b == nil {
		http.Error(w, "binding not found", http.StatusNotFound)
		return
	}
	writeJSON(w, b)
}

func (s *Server) handleBindingUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "invalid binding id", http.StatusBadRequest)
		return
	}
	var req bindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	b, ok := s.checkedBinding(w, r, id, req)
	if !ok {
		return
	}
	if err := s.Bindings.UpdateBinding(r.Context(), b); err != nil {
		switch {
		case errors.Is(err, storecontract.ErrBindingNotFound):
			http.Error(w, "binding not found", http.StatusNotFound)
		default:
			s.Log.Error("update binding", "err", err, "id", id)
			http.Error(w, "update binding failed", http.StatusInternalServerError)
		}
		return
	}
	updated, err := s.Bindings.GetBinding(r.Context(), id)
	if err != nil || updated == nil {
		s.Log.Error("get binding after update", "err", err, "id", id)
		http.Error(w, "update binding failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, updated)
}

func (s *Server) handleBindingDelete(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "invalid binding id", http.StatusBadRequest)
		return
	}
	if err := s.Bindings.DeleteBinding(r.Context(), id); err != nil {
		if errors.Is(err, storecontract.ErrBindingNotFound) {
			http.Error(w, "binding not found", http.StatusNotFound)
			return
		}
		s.Log.Error("delete binding", "err", err, "id", id)
		http.Error(w, "delete binding failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBindingApprove(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "invalid binding id", http.StatusBadRequest)
		return
	}
	if err := s.Bindings.ApproveBinding(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, storecontract.ErrBindingNotFound):
			http.Error(w, "binding not found", http.StatusNotFound)
		case errors.Is(err, storecontract.ErrBindingTransition):
			http.Error(w, "binding cannot be approved from its current state", http.StatusConflict)
		default:
			s.Log.Error("approve binding", "err", err, "id", id)
			http.Error(w, "approve binding failed", http.StatusInternalServerError)
		}
		return
	}
	updated, err := s.Bindings.GetBinding(r.Context(), id)
	if err != nil || updated == nil {
		s.Log.Error("get binding after approve", "err", err, "id", id)
		http.Error(w, "approve binding failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, updated)
}

// checkedBinding builds the binding a create or update request describes and
// refuses it, having written the error, unless it is well-formed, its mapping
// exists, its filter compiles against the mapping's parameters, and its inputs
// and repository satisfy the workflow it targets.
func (s *Server) checkedBinding(w http.ResponseWriter, r *http.Request, id string, req bindingRequest) (binding.Binding, bool) {
	b := binding.Binding{
		ID:        id,
		Name:      req.Name,
		Matcher:   req.Matcher,
		MappingID: req.MappingID,
		Filter:    req.Filter,
		Workflow:  req.Workflow,
		Owner:     req.Owner,
		Repo:      req.Repo,
		RepoParam: req.RepoParam,
		Inputs:    req.Inputs,
		Status:    binding.StatusPendingApproval,
	}
	if err := b.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return binding.Binding{}, false
	}
	entry, found, err := s.workflowEntry(r.Context(), req.Workflow)
	if err != nil {
		http.Error(w, "workflow definitions unavailable", http.StatusServiceUnavailable)
		return binding.Binding{}, false
	}
	if !found {
		http.Error(w, "binding: workflow not found: "+req.Workflow, http.StatusBadRequest)
		return binding.Binding{}, false
	}
	iface, err := task.ParseWorkflowInterface(entry.YAML)
	if err != nil {
		http.Error(w, "binding: workflow "+req.Workflow+": "+err.Error(), http.StatusBadRequest)
		return binding.Binding{}, false
	}
	if s.Mappings == nil {
		http.Error(w, "mappings not configured", http.StatusServiceUnavailable)
		return binding.Binding{}, false
	}
	m, err := s.Mappings.GetMapping(r.Context(), b.MappingID)
	if err != nil {
		s.Log.Error("get mapping for binding", "err", err, "mapping", b.MappingID)
		http.Error(w, "get mapping failed", http.StatusInternalServerError)
		return binding.Binding{}, false
	}
	if m == nil {
		http.Error(w, "binding: mapping not found: "+b.MappingID, http.StatusBadRequest)
		return binding.Binding{}, false
	}
	if _, err := binding.CompileFilter(b.Filter, m.Fields); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return binding.Binding{}, false
	}
	if err := b.CheckWorkflow(iface, m.Fields); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return binding.Binding{}, false
	}
	return b, true
}
