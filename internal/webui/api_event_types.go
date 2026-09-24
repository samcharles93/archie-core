package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// proposalScanLimit bounds how many recent captures are grouped into
// proposals on one read.
const proposalScanLimit = 500

// eventTypeExample is a pasted (or sampled) event: headers by name and the
// raw body. The type's schema is inferred from it.
type eventTypeExample struct {
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// eventTypeRequest is POST /api/event-types. Rule, when absent, is the one
// inferred from the example.
type eventTypeRequest struct {
	Source  string           `json:"source"`
	Name    string           `json:"name"`
	Rule    *eventtype.Rule  `json:"rule"`
	Example eventTypeExample `json:"example"`
}

// eventTypeUpdate is PUT /api/event-types/{id}: the name and rule are the
// editable parts of a type.
type eventTypeUpdate struct {
	Name string         `json:"name"`
	Rule eventtype.Rule `json:"rule"`
}

// handleEventTypes lists the named event types and the proposals grouped from
// recent captures that were unidentified on arrival and still match no type.
func (s *Server) handleEventTypes(w http.ResponseWriter, r *http.Request) {
	if s.EventTypes == nil {
		writeJSON(w, map[string]any{"enabled": false, "event_types": []any{}, "proposals": []any{}})
		return
	}
	types, err := s.EventTypes.ListEventTypes(r.Context())
	if err != nil {
		s.Log.Error("list event types", "err", err)
		http.Error(w, "list event types failed", http.StatusInternalServerError)
		return
	}
	proposals := []eventtype.Proposal{}
	if s.Captures != nil {
		captures, err := s.Captures.ListCaptures(r.Context(), proposalScanLimit)
		if err != nil {
			s.Log.Error("list captures for proposals", "err", err)
			http.Error(w, "list captures failed", http.StatusInternalServerError)
			return
		}
		proposals = append(proposals, eventtype.Propose(unmatchedCaptures(types, captures))...)
	}
	if types == nil {
		types = []eventtype.EventType{}
	}
	writeJSON(w, map[string]any{"enabled": true, "event_types": types, "proposals": proposals})
}

// unmatchedCaptures keeps the captures that were unidentified on arrival and
// that no current type would identify either. A capture a newer type now
// matches stays unidentified (it was never dispatched) but is no longer
// proposed.
func unmatchedCaptures(types []eventtype.EventType, captures []storecontract.CapturedEvent) []eventtype.Capture {
	var out []eventtype.Capture
	for _, c := range captures {
		if c.EventType != "" {
			continue
		}
		sample := eventtype.Sample{Headers: eventtype.ParseHeaders(c.Headers), Body: []byte(c.Body)}
		if _, ok := eventtype.Identify(types, c.Source, sample); ok {
			continue
		}
		out = append(out, eventtype.Capture{ID: c.ID, Source: c.Source, ReceivedAt: c.ReceivedAt, Sample: sample})
	}
	return out
}

func (s *Server) handleEventTypeCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.EventTypes == nil {
		http.Error(w, "event types not configured", http.StatusServiceUnavailable)
		return
	}
	var req eventTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	t := eventtype.FromExample(req.Source, req.Name, eventtype.Sample{
		Headers: req.Example.Headers,
		Body:    []byte(req.Example.Body),
	})
	if req.Rule != nil {
		t.Rule = *req.Rule
	}
	id, err := s.EventTypes.InsertEventType(r.Context(), t)
	if err != nil {
		s.writeEventTypeError(w, "create", err)
		return
	}
	t.ID = id
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t)
}

func (s *Server) handleEventTypeUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.EventTypes == nil {
		http.Error(w, "event types not configured", http.StatusServiceUnavailable)
		return
	}
	var req eventTypeUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Source is not editable: the store validates against the stored one.
	t := eventtype.EventType{ID: r.PathValue("id"), Name: req.Name, Rule: req.Rule}
	if err := s.EventTypes.UpdateEventType(r.Context(), t); err != nil {
		s.writeEventTypeError(w, "update", err)
		return
	}
	writeJSON(w, map[string]string{"id": t.ID})
}

func (s *Server) handleEventTypeDelete(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.EventTypes == nil {
		http.Error(w, "event types not configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.EventTypes.DeleteEventType(r.Context(), r.PathValue("id")); err != nil {
		s.writeEventTypeError(w, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeEventTypeError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, eventtype.ErrOverlap):
		http.Error(w, "this rule can match events another type on the source already matches", http.StatusConflict)
	case errors.Is(err, eventtype.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, storecontract.ErrEventTypeNotFound):
		http.Error(w, "event type not found", http.StatusNotFound)
	default:
		s.Log.Error(action+" event type", "err", err)
		http.Error(w, action+" event type failed", http.StatusInternalServerError)
	}
}
