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

	querySince, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	since := querySince
	if headerValue := r.Header.Get("Last-Event-ID"); headerValue != "" {
		if headerSince, err := strconv.ParseInt(headerValue, 10, 64); err == nil {
			since = headerSince
		}
	}
	send := func(e events.Event) bool {
		b, err := json.Marshal(e)
		if err != nil {
			s.Log.Error("sse event marshal failed", "error", err, "event_id", e.ID)
			return false
		}
		if _, err := w.Write([]byte("id: " + strconv.FormatInt(e.ID, 10) + "\ndata: " + string(b) + "\n\n")); err != nil {
			return false
		}
		fl.Flush()
		return true
	}

	// Register before reading the backlog. An event published between the
	// backlog read and subscription would otherwise be lost permanently.
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

	catchUp := func(targetID int64) bool {
		for {
			before := since
			backlog, err := s.Store.EventsSince(r.Context(), since, 200)
			if err != nil {
				s.Log.Error("sse backlog fetch failed", "error", err, "since", since)
				// Comments are invisible to EventSource clients. Close after
				// surfacing the gap so EventSource invokes its reconnect path.
				_, _ = w.Write([]byte(":error " + err.Error() + "\n\n"))
				fl.Flush()
				return false
			}
			for _, e := range backlog {
				if e.ID <= since {
					continue
				}
				if !send(e) {
					return false
				}
				since = e.ID
				if targetID > 0 && since >= targetID {
					return true
				}
			}
			if len(backlog) < 200 || since == before || (targetID > 0 && since >= targetID) {
				return true
			}
		}
	}

	if !catchUp(0) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-c:
			if e.ID <= since {
				continue
			}
			if !catchUp(e.ID) {
				return
			}
			// Broadcast may carry an event that has not yet reached the
			// durable store. Deliver that live event after filling any
			// persisted gap, while keeping the ID deduplication invariant.
			if e.ID > since {
				if !send(e) {
					return
				}
				since = e.ID
			}
		}
	}
}
