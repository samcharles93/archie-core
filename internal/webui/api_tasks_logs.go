package webui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/logging"
)

// handleTaskLogs serves one task attempt's persisted log history, the same
// way handleLogs serves the daemon-wide log -- transport only, parsing
// belongs to the logging package.
//
// A request with no "attempt" query param defaults to the task's current
// Attempt from the store, since that is what a human or Archie's own chat
// tool means by "why did task N park?" almost every time: the most recent
// run, not an arbitrary earlier retry.
func (s *Server) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}

	task, err := s.Store.TaskByID(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	q := r.URL.Query()
	attempt := task.Attempt
	if raw := strings.TrimSpace(q.Get("attempt")); raw != "" {
		attempt, err = strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "bad attempt", http.StatusBadRequest)
			return
		}
	}

	path := s.TaskLogs.Path(id, attempt)
	if path == "" {
		// Not an error: task logging is optional, same convention as
		// handleLogs for the daemon-wide log.
		writeJSON(w, map[string]any{
			"entries":  []logging.Entry{},
			"file":     "",
			"disabled": true,
			"attempt":  attempt,
		})
		return
	}

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
		s.Log.Warn("task log components unavailable", "task_id", id, "err", err)
		components = []string{}
	}

	writeJSON(w, map[string]any{
		"entries":    result.Entries,
		"truncated":  result.Truncated,
		"file":       result.File,
		"components": components,
		"attempt":    attempt,
	})
}
