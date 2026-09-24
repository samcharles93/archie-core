package webui

import (
	"net/http"
	"strconv"
)

// defaultCapturesListLimit is used when the limit query param is absent,
// non-numeric, non-positive, or wider than the int32 SQL LIMIT the store
// binds. A raw strconv.ParseInt zero value (its error
// case) must never reach ListCaptures directly -- SQL "LIMIT 0" returns zero
// rows, which would make an unrecognised or missing limit look identical to
// "no captures exist" instead of "show the default page."
const defaultCapturesListLimit = 100

// handleCaptures lists recent captured events, newest first, for the
// dashboard's event inspector. Token-gated like every other
// /api/* route -- captured payloads are visible only to an authenticated
// operator.
//
// This is the read half of event capture. The write half is the receiver in
// internal/infrastructure/captureintake, mounted on the bypass mux via
// CaptureIntake; the dashboard displays captures and mounts the route.
func (s *Server) handleCaptures(w http.ResponseWriter, r *http.Request) {
	if s.Captures == nil {
		writeJSON(w, map[string]any{"captures": []any{}, "enabled": false})
		return
	}

	limit, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32)
	if err != nil || limit <= 0 {
		limit = defaultCapturesListLimit
	}
	captures, err := s.Captures.ListCaptures(r.Context(), int(limit))
	if err != nil {
		s.Log.Error("list captures", "err", err)
		http.Error(w, "list captures failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"captures": captures, "enabled": true})
}
