package webui

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/samcharles93/archie-core/internal/events"
)

// handleSSE streams events: catch-up from the store (?since=<event id>),
// then live from the broadcast hub.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	send := func(e events.Event) bool {
		b, err := json.Marshal(e)
		if err != nil {
			return true
		}
		if _, err := w.Write([]byte("id: " + strconv.FormatInt(e.ID, 10) + "\ndata: " + string(b) + "\n\n")); err != nil {
			return false
		}
		fl.Flush()
		return true
	}

	if backlog, err := s.Store.EventsSince(r.Context(), since, 200); err != nil {
		s.Log.Error("sse backlog fetch failed", "error", err, "since", since)
		// A comment frame keeps the stream open; the client sees the
		// backlog gap rather than a silent truncation.
		_, _ = w.Write([]byte(":error " + err.Error() + "\n"))
		fl.Flush()
	} else {
		for _, e := range backlog {
			if !send(e) {
				return
			}
			since = e.ID
		}
	}

	c := make(chan events.Event, 64)
	s.mu.Lock()
	if s.conns == nil {
		s.conns = map[chan events.Event]struct{}{}
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-c:
			if e.ID <= since {
				continue // already replayed from the backlog
			}
			if !send(e) {
				return
			}
		}
	}
}
