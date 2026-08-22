package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/store"
)

// mappingRequest is the wire shape POST /api/mappings and
// PATCH /api/mappings/{id} both accept. ID/CreatedAt/UpdatedAt are
// server-assigned, never taken from the request body.
type mappingRequest struct {
	Name       string          `json:"name"`
	SourceHint string          `json:"source_hint"`
	Fields     []mapping.Field `json:"fields"`
}

func (s *Server) handleMappingsList(w http.ResponseWriter, r *http.Request) {
	if s.Mappings == nil {
		http.Error(w, "mappings not configured", http.StatusServiceUnavailable)
		return
	}
	mappings, err := s.Mappings.ListMappings(r.Context())
	if err != nil {
		s.Log.Error("list mappings", "err", err)
		http.Error(w, "list mappings failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"mappings": mappings})
}

func (s *Server) handleMappingCreate(w http.ResponseWriter, r *http.Request) {
	if s.Mappings == nil {
		http.Error(w, "mappings not configured", http.StatusServiceUnavailable)
		return
	}
	var req mappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	m := mapping.Mapping{Name: req.Name, SourceHint: req.SourceHint, Fields: req.Fields}
	if err := m.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Mappings.InsertMapping(r.Context(), m)
	if err != nil {
		s.Log.Error("insert mapping", "err", err)
		http.Error(w, "create mapping failed", http.StatusInternalServerError)
		return
	}
	created, err := s.Mappings.GetMapping(r.Context(), id)
	if err != nil || created == nil {
		s.Log.Error("get mapping after insert", "err", err, "id", id)
		http.Error(w, "create mapping failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

func (s *Server) handleMappingGet(w http.ResponseWriter, r *http.Request) {
	if s.Mappings == nil {
		http.Error(w, "mappings not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid mapping id", http.StatusBadRequest)
		return
	}
	m, err := s.Mappings.GetMapping(r.Context(), id)
	if err != nil {
		s.Log.Error("get mapping", "err", err, "id", id)
		http.Error(w, "get mapping failed", http.StatusInternalServerError)
		return
	}
	if m == nil {
		http.Error(w, "mapping not found", http.StatusNotFound)
		return
	}
	writeJSON(w, m)
}

func (s *Server) handleMappingUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Mappings == nil {
		http.Error(w, "mappings not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid mapping id", http.StatusBadRequest)
		return
	}
	var req mappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	m := mapping.Mapping{ID: id, Name: req.Name, SourceHint: req.SourceHint, Fields: req.Fields}
	if err := m.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Mappings.UpdateMapping(r.Context(), m); err != nil {
		if errors.Is(err, store.ErrMappingNotFound) {
			http.Error(w, "mapping not found", http.StatusNotFound)
			return
		}
		s.Log.Error("update mapping", "err", err, "id", id)
		http.Error(w, "update mapping failed", http.StatusInternalServerError)
		return
	}
	updated, err := s.Mappings.GetMapping(r.Context(), id)
	if err != nil || updated == nil {
		s.Log.Error("get mapping after update", "err", err, "id", id)
		http.Error(w, "update mapping failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, updated)
}

func (s *Server) handleMappingDelete(w http.ResponseWriter, r *http.Request) {
	if s.Mappings == nil {
		http.Error(w, "mappings not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid mapping id", http.StatusBadRequest)
		return
	}
	if err := s.Mappings.DeleteMapping(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrMappingNotFound) {
			http.Error(w, "mapping not found", http.StatusNotFound)
			return
		}
		s.Log.Error("delete mapping", "err", err, "id", id)
		http.Error(w, "delete mapping failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mappingPreviewRequest lets an operator resolve a candidate set of fields
// against a real captured event before saving anything -- Fields is loose
// input, not a saved mapping ID, per docs/prds/payload-field-mapping.md.
type mappingPreviewRequest struct {
	CaptureID int64           `json:"capture_id"`
	Fields    []mapping.Field `json:"fields"`
}

func (s *Server) handleMappingPreview(w http.ResponseWriter, r *http.Request) {
	if s.Mappings == nil {
		http.Error(w, "mappings not configured", http.StatusServiceUnavailable)
		return
	}
	if s.Captures == nil {
		http.Error(w, "captures not configured", http.StatusServiceUnavailable)
		return
	}
	var req mappingPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	capture, err := s.captureByID(r.Context(), req.CaptureID)
	if err != nil {
		s.Log.Error("get capture for preview", "err", err, "capture_id", req.CaptureID)
		http.Error(w, "preview failed", http.StatusInternalServerError)
		return
	}
	if capture == nil {
		http.Error(w, "capture not found", http.StatusNotFound)
		return
	}
	values, failures := mapping.Resolve(req.Fields, []byte(capture.Body))
	if failures == nil {
		failures = []mapping.Failure{}
	}
	writeJSON(w, map[string]any{"values": values, "failures": failures})
}

// captureByID finds one captured event by ID. CaptureStore exposes only
// ListCaptures (see docs/prds/event-capture-storage.md -- capture has
// exactly one reader, the dashboard list view, so a by-ID lookup was never
// needed until preview), so this scans the newest window rather than
// adding a new store method for a single low-volume caller.
func (s *Server) captureByID(ctx context.Context, id int64) (*store.CapturedEvent, error) {
	captures, err := s.Captures.ListCaptures(ctx, mappingCaptureScanWindow(s.CaptureMaxEvents))
	if err != nil {
		return nil, err
	}
	for i := range captures {
		if captures[i].ID == id {
			return &captures[i], nil
		}
	}
	return nil, nil
}

// captureScanWindowDefault bounds captureByID's linear scan when
// CaptureMaxEvents is unset (zero or negative, matching CaptureConfig's own
// "zero means default" convention). Matches CaptureConfig's documented
// default so an unconfigured deployment's scan window still covers its
// whole table.
const captureScanWindowDefault = 5000

// mappingCaptureScanWindow ties captureByID's scan bound to the operator's
// actual configured retention (CaptureMaxEvents), rather than a constant
// that silently drifts out of sync with it -- an operator raising
// [capture] max_events above the old hardcoded 5000 would otherwise make a
// genuinely still-retained capture 404 as "not found" during preview.
func mappingCaptureScanWindow(configuredMaxEvents int) int {
	if configuredMaxEvents <= 0 {
		return captureScanWindowDefault
	}
	return configuredMaxEvents
}
