package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/store"
)

// bindingRequest is the wire shape POST /api/bindings and
// PATCH /api/bindings/{id} both accept. ID/Version/Status/CreatedAt/UpdatedAt
// are server-assigned (or server-driven), never taken from the request body.
type bindingRequest struct {
	Name      string          `json:"name"`
	Matcher   binding.Matcher `json:"matcher"`
	MappingID int64           `json:"mapping_id"`
	Workflow  string          `json:"workflow"`
	Secret    string          `json:"secret"`
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
	stripBindingSecrets(bindings)
	writeJSON(w, map[string]any{"bindings": bindings})
}

func (s *Server) handleBindingCreate(w http.ResponseWriter, r *http.Request) {
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	var req bindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !s.hasWorkflow(req.Workflow) {
		http.Error(w, "binding: workflow not found: "+req.Workflow, http.StatusBadRequest)
		return
	}
	b := binding.Binding{
		Name:      req.Name,
		Matcher:   req.Matcher,
		MappingID: req.MappingID,
		Workflow:  req.Workflow,
		Secret:    req.Secret,
		Status:    binding.Normalize(binding.StatusDraft),
	}
	if err := b.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Bindings.InsertBinding(r.Context(), b)
	if err != nil {
		if errors.Is(err, store.ErrBindingOverlap) {
			http.Error(w, "binding source overlaps an existing binding", http.StatusConflict)
			return
		}
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
	created.Secret = ""
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

func (s *Server) handleBindingGet(w http.ResponseWriter, r *http.Request) {
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
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
	b.Secret = ""
	writeJSON(w, b)
}

func (s *Server) handleBindingUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid binding id", http.StatusBadRequest)
		return
	}
	var req bindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !s.hasWorkflow(req.Workflow) {
		http.Error(w, "binding: workflow not found: "+req.Workflow, http.StatusBadRequest)
		return
	}
	b := binding.Binding{
		ID:        id,
		Name:      req.Name,
		Matcher:   req.Matcher,
		MappingID: req.MappingID,
		Workflow:  req.Workflow,
		Secret:    req.Secret,
	}
	if err := b.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Bindings.UpdateBinding(r.Context(), b); err != nil {
		switch {
		case errors.Is(err, store.ErrBindingNotFound):
			http.Error(w, "binding not found", http.StatusNotFound)
		case errors.Is(err, store.ErrBindingOverlap):
			http.Error(w, "binding source overlaps an existing binding", http.StatusConflict)
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
	updated.Secret = ""
	writeJSON(w, updated)
}

func (s *Server) handleBindingDelete(w http.ResponseWriter, r *http.Request) {
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid binding id", http.StatusBadRequest)
		return
	}
	if err := s.Bindings.DeleteBinding(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrBindingNotFound) {
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
	if s.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid binding id", http.StatusBadRequest)
		return
	}
	if err := s.Bindings.ApproveBinding(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, store.ErrBindingNotFound):
			http.Error(w, "binding not found", http.StatusNotFound)
		case errors.Is(err, store.ErrBindingTransition):
			http.Error(w, "binding cannot be approved from its current state", http.StatusConflict)
		case errors.Is(err, store.ErrBindingOverlap):
			http.Error(w, "binding source overlaps an existing binding", http.StatusConflict)
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
	updated.Secret = ""
	writeJSON(w, updated)
}

// stripBindingSecrets zeroes the Secret field on every binding in the slice
// so the list handler never leaks a shared HMAC secret through /api/bindings.
// Single-binding reads do the same inline; this covers the list path.
func stripBindingSecrets(bs []binding.Binding) {
	for i := range bs {
		bs[i].Secret = ""
	}
}
