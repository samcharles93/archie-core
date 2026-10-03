package webui

import (
	"net/http"
	"strconv"
)

// defaultCapturesListLimit applies when the limit parameter is missing,
// invalid or out of range.
const defaultCapturesListLimit = 100

// handleCaptures lists recent captured events, newest first.
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
