package webui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/logging"
)

// handleLogs serves a page of log history.
//
// This handler is transport only: parsing the log format belongs to the
// logging package, which owns it. Live output reaches the dashboard over the
// existing /events stream; this endpoint exists so the view has history from
// before the browser connected, which is the whole reason file logging landed
// first.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	path := s.logFile()
	if path == "" {
		// Not an error: file logging is optional. Say so plainly so the UI can
		// explain how to turn it on rather than showing an empty table.
		writeJSON(w, map[string]any{
			"entries":  []any{},
			"file":     "",
			"disabled": true,
		})
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	result, err := logging.Tail(path, logging.Query{
		Levels:    splitCSV(q.Get("level")),
		Component: strings.TrimSpace(q.Get("component")),
		Contains:  strings.TrimSpace(q.Get("q")),
		Limit:     limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	components, err := logging.Components(path)
	if err != nil {
		// The filter list is a convenience; failing to build it must not cost
		// the caller their log entries.
		s.Log.Warn("log components unavailable", "err", err)
		components = []string{}
	}

	writeJSON(w, map[string]any{
		"entries":    result.Entries,
		"truncated":  result.Truncated,
		"file":       result.File,
		"components": components,
	})
}

// logFile reports the configured log path, or "" when file logging is off.
func (s *Server) logFile() string {
	if s.Cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.Cfg.Log.File)
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
