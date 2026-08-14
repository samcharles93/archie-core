package webui

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

// sseBacklogPageSize bounds one EventsSince fetch during catch-up.
const sseBacklogPageSize = 200

// handleSSE streams events: catch-up from the store (?since=<event id>, or
// the Last-Event-ID header on a reconnect), then live from the broadcast
// hub.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	stream, ok := newSSEStream(s.Store, s.Log, w, sseSince(r))
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Register before reading the backlog. An event published between the
	// backlog read and subscription would otherwise be lost permanently.
	conn, unregister := s.registerSSEConn()
	defer unregister()

	if !stream.catchUp(r.Context(), 0) {
		return
	}
	stream.drain(r.Context(), conn)
}

// sseSince resolves the event ID a client wants to resume after. A valid
// Last-Event-ID header  --  how EventSource itself resumes a dropped
// connection  --  takes precedence over the ?since= query parameter, which
// only matters for a client's very first connection.
func sseSince(r *http.Request) int64 {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if header := r.Header.Get("Last-Event-ID"); header != "" {
		if fromHeader, err := strconv.ParseInt(header, 10, 64); err == nil {
			since = fromHeader
		}
	}
	return since
}

// registerSSEConn subscribes a channel to Broadcast and returns the
// function that unsubscribes it.
func (s *Server) registerSSEConn() (chan events.Event, func()) {
	c := make(chan events.Event, 64)
	s.mu.Lock()
	if s.conns == nil {
		s.conns = map[chan events.Event]struct{}{}
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()

	return c, func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}
}

// sseStream renders one client's event-stream response: an SSE frame per
// event, an SSE comment when the persisted backlog can't be read, and the
// running "since" watermark that deduplicates the backlog against live
// broadcasts covering the same event.
type sseStream struct {
	store store.TaskStore
	log   *slog.Logger
	w     http.ResponseWriter
	fl    http.Flusher
	since int64
}

// newSSEStream builds a stream over w, reporting false if w cannot be
// flushed incrementally  --  SSE does not work without that.
func newSSEStream(st store.TaskStore, log *slog.Logger, w http.ResponseWriter, since int64) (*sseStream, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	return &sseStream{store: st, log: log, w: w, fl: fl, since: since}, true
}

// send writes one event as an SSE frame. It reports false on a write
// error, meaning the connection is gone and the caller must stop.
func (s *sseStream) send(e events.Event) bool {
	body, err := json.Marshal(e)
	if err != nil {
		s.log.Error("sse event marshal failed", "error", err, "event_id", e.ID)
		return false
	}
	if _, err := s.w.Write([]byte("id: " + strconv.FormatInt(e.ID, 10) + "\ndata: " + string(body) + "\n\n")); err != nil {
		return false
	}
	s.fl.Flush()
	return true
}

// writeErrorComment reports a backlog fetch failure as an SSE comment.
// Comments are invisible to EventSource's own event parsing, but writing
// one and flushing before the handler returns is what actually reaches the
// client: it ends the response, which is what drives EventSource's
// reconnect.
func (s *sseStream) writeErrorComment(err error) {
	_, _ = s.w.Write([]byte(":error " + err.Error() + "\n\n"))
	s.fl.Flush()
}

// catchUp sends every persisted event after s.since, advancing s.since as
// it goes, until it reaches targetID (0 means "drain everything currently
// available"), runs out of backlog, or fails. It reports false on failure
// -- a fetch error or a dead connection -- meaning the caller must stop.
func (s *sseStream) catchUp(ctx context.Context, targetID int64) bool {
	for {
		before := s.since
		backlog, err := s.store.EventsSince(ctx, s.since, sseBacklogPageSize)
		if err != nil {
			s.log.Error("sse backlog fetch failed", "error", err, "since", s.since)
			s.writeErrorComment(err)
			return false
		}
		reachedTarget, ok := s.sendPage(backlog, targetID)
		if !ok {
			return false
		}
		if reachedTarget || len(backlog) < sseBacklogPageSize || s.since == before {
			return true
		}
	}
}

// sendPage sends every event in backlog newer than s.since, advancing it as
// it goes. reachedTarget reports whether targetID was reached mid-page --
// stopping there is correct, since nothing beyond it is needed yet -- and
// ok reports whether every send succeeded.
func (s *sseStream) sendPage(backlog []events.Event, targetID int64) (reachedTarget, ok bool) {
	for _, e := range backlog {
		if e.ID <= s.since {
			continue
		}
		if !s.send(e) {
			return false, false
		}
		s.since = e.ID
		if targetID > 0 && s.since >= targetID {
			return true, true
		}
	}
	return false, true
}

// drain relays broadcast events after the initial catch-up. A broadcast can
// reach the client before its event lands in the durable store, so each one
// first fills any persisted gap ahead of it, then is delivered directly if
// catch-up did not already cover it -- both paths go through the same
// since-based deduplication.
func (s *sseStream) drain(ctx context.Context, conn <-chan events.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-conn:
			if e.ID <= s.since {
				continue
			}
			if !s.catchUp(ctx, e.ID) {
				return
			}
			if e.ID > s.since {
				if !s.send(e) {
					return
				}
				s.since = e.ID
			}
		}
	}
}
