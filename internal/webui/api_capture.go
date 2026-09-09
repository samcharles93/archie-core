package webui

import (
	"net/http"
	"strconv"
)

// defaultCapturesListLimit is used when the limit query param is absent,
// non-numeric, or non-positive. A raw strconv.Atoi zero value (its error
// case) must never reach ListCaptures directly -- SQL "LIMIT 0" returns zero
// rows, which would make an unrecognised or missing limit look identical to
// "no captures exist" instead of "show the default page."
const defaultCapturesListLimit = 100

// handleCaptures lists recent captured events, newest first, for the
// dashboard's event inspector (t2db.2). Token-gated like every other
// /api/* route -- captured payloads are visible only to an authenticated
// operator, per docs/prds/webhook-intake-security.md point 5.
//
// This is the read half of event capture. The write half is the receiver in
// internal/infrastructure/captureintake, mounted on the bypass mux via
// CaptureIntake; the dashboard displays captures and mounts the route; it
// no longer stores them (archie-core-8cda.5.4).
func (s *Server) handleCaptures(w http.ResponseWriter, r *http.Request) {
	if s.Captures == nil {
		writeJSON(w, map[string]any{"captures": []any{}, "enabled": false})
		return
	}

	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = defaultCapturesListLimit
	}
	captures, err := s.Captures.ListCaptures(r.Context(), limit)
	if err != nil {
		s.Log.Error("list captures", "err", err)
		http.Error(w, "list captures failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"captures": captures, "enabled": true})
}
