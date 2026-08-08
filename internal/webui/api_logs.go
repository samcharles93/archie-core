package webui

import (
	"encoding/json"
	"net/http"
	"slices"
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
			"entries":    s.LogFeed.Snapshot(),
			"file":       "",
			"disabled":   true,
			"components": mergeComponents(nil, s.LogFeed.Snapshot()),
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
	liveEntries := s.LogFeed.Snapshot()
	components = mergeComponents(components, liveEntries)

	writeJSON(w, map[string]any{
		"entries":    append(result.Entries, liveEntries...),
		"truncated":  result.Truncated,
		"file":       result.File,
		"components": components,
	})
}

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	if s.LogFeed == nil {
		http.Error(w, "live daemon log feed unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	entries := s.LogFeed.Subscribe(r.Context())
	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			_, _ = w.Write([]byte("id: " + strconv.FormatInt(entry.ID, 10) + "\ndata: " + string(data) + "\n\n"))
			flusher.Flush()
		}
	}
}

func mergeComponents(history []string, live []logging.Entry) []string {
	seen := make(map[string]struct{}, len(history))
	for _, component := range history {
		seen[component] = struct{}{}
	}
	for _, entry := range live {
		if component, ok := entry.Fields["component"].(string); ok && component != "" {
			seen[component] = struct{}{}
		}
	}
	components := make([]string, 0, len(seen))
	for component := range seen {
		components = append(components, component)
	}
	slices.Sort(components)
	return components
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
