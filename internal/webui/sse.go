package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/logging"
)

// sseBacklogPageSize bounds one EventsSince fetch during catch-up.
const sseBacklogPageSize = 200

// sseNoiseKinds are event kinds published for internal wiring rather than
// for a dashboard viewer: turn_completed fires on every chat turn so
// input-driven curators can wake on it (see events.KindTurnCompleted), and
// carries no task correlation (TaskID is always 0, Detail a raw session ID)
// -- on a dashboard where "Live activity" is otherwise almost entirely
// task lifecycle events, it drowned the signal it sits next to. Curators
// read the Bus directly and are unaffected by excluding a kind here, at the
// SSE boundary, rather than at publish.
var sseNoiseKinds = map[string]bool{
	events.KindTurnCompleted: true,
}

// sseVisible reports whether e should reach an SSE client.
func sseVisible(e events.Event) bool {
	return !sseNoiseKinds[e.Kind]
}

// handleSSE serves task history, latest resource snapshots, and optional
// live logs over one connection. Append-only topics resume from independent
// cursors; state topics replay their latest value on each connection.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	cursors := sseCursors(r)
	stream, ok := newSSEStream(s.Store, s.Log, w, cursors.Tasks)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	stream.logsSince = cursors.Logs

	// Register before reading the backlog. An event published between the
	// backlog read and subscription would otherwise be lost permanently.
	conn, snapshot, stale, unregister := s.registerSSEConn()
	defer unregister()
	var logs <-chan logging.Entry
	if r.URL.Query().Get("topics") == "logs" && s.LogFeed != nil {
		logs = s.LogFeed.Subscribe(r.Context())
	}
	// Headers make an idle stream live before any resource changes.
	w.WriteHeader(http.StatusOK)
	stream.fl.Flush()

	if !stream.catchUp(r.Context(), "") {
		return
	}
	for _, update := range snapshot {
		if !stream.sendLive(update) {
			return
		}
	}
	if r.URL.Query().Get("topics") == "logs" && !stream.sendLogSnapshot(s.LogFeed) {
		return
	}
	stream.drain(r.Context(), conn, stale, logs)
}

// sseSince is the task-only cursor view retained for legacy clients/tests.
// Native reconnects prefer Last-Event-ID; deliberate reconnects use ?since=.
// An invalid or pre-cursor integer degrades to replay from the beginning.
func sseSince(r *http.Request) string { return sseCursors(r).Tasks }

type streamCursors struct {
	Tasks string `json:"tasks"`
	Logs  int64  `json:"logs"`
}

// EventSource only sends Last-Event-ID on reconnects it owns. A deliberate
// reconnect (entering/leaving Logs) supplies the same token in ?since=.
func sseCursors(r *http.Request) streamCursors {
	value := r.URL.Query().Get("since")
	if header := r.Header.Get("Last-Event-ID"); header != "" {
		value = header
	}
	var cursors streamCursors
	if data, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		_ = json.Unmarshal(data, &cursors)
	} else if _, _, ok := storecontract.ParseEventCursor(value); ok {
		cursors.Tasks = value // legacy task cursor
	}
	if _, _, ok := storecontract.ParseEventCursor(cursors.Tasks); !ok {
		cursors.Tasks = ""
	}
	if cursors.Logs < 0 {
		cursors.Logs = 0
	}
	return cursors
}

func (c streamCursors) id() string {
	encoded, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// sseStream renders one client's event-stream response: an SSE frame per
// event, an SSE comment when the persisted backlog can't be read, and the
// running "since" cursor that deduplicates the backlog against live
// broadcasts covering the same event.
type sseStream struct {
	store     storecontract.TaskStore
	log       *slog.Logger
	w         http.ResponseWriter
	fl        http.Flusher
	since     string
	logsSince int64
}

// newSSEStream builds a stream over w, reporting false if w cannot be
// flushed incrementally  --  SSE does not work without that.
func newSSEStream(st storecontract.TaskStore, log *slog.Logger, w http.ResponseWriter, since string) (*sseStream, bool) {
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
	return s.writeLive(liveUpdate{topic: "tasks", data: e}, streamCursors{Tasks: storecontract.EventCursor(e.At, e.ID), Logs: s.logsSince}.id())
}

func (s *sseStream) sendLogSnapshot(feed *logging.Feed) bool {
	if feed == nil {
		return s.sendLive(liveUpdate{topic: "logs-status", data: map[string]bool{"available": false}})
	}
	for _, entry := range feed.Snapshot() {
		if !s.sendLog(entry) {
			return false
		}
	}
	return true
}

func (s *sseStream) sendLog(entry logging.Entry) bool {
	if entry.ID <= s.logsSince {
		return true
	}
	if !s.writeLive(liveUpdate{topic: "logs", data: entry}, streamCursors{Tasks: s.since, Logs: entry.ID}.id()) {
		return false
	}
	s.logsSince = entry.ID
	return true
}

func (s *sseStream) sendLive(update liveUpdate) bool { return s.writeLive(update, "") }

func (s *sseStream) writeLive(update liveUpdate, id string) bool {
	body, err := marshalLiveFrame(update)
	if err != nil {
		return false
	}
	prefix := ""
	if id != "" {
		prefix = "id: " + id + "\n"
	}
	if _, err := s.w.Write([]byte(prefix + "data: " + string(body) + "\n\n")); err != nil {
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
// it goes, until it reaches target ("" means "drain everything currently
// available"), runs out of backlog, or fails. It reports false on failure
// -- a fetch error or a dead connection -- meaning the caller must stop.
func (s *sseStream) catchUp(ctx context.Context, target string) bool {
	for {
		before := s.since
		backlog, err := s.store.EventsSince(ctx, s.since, sseBacklogPageSize)
		if err != nil {
			s.log.Error("sse backlog fetch failed", "error", err, "since", s.since)
			s.writeErrorComment(err)
			return false
		}
		reachedTarget, ok := s.sendPage(backlog, target)
		if !ok {
			return false
		}
		if reachedTarget || len(backlog) < sseBacklogPageSize || s.since == before {
			return true
		}
	}
}

// sendPage sends every event in backlog newer than s.since, advancing it as
// it goes. reachedTarget reports whether target was reached mid-page --
// stopping there is correct, since nothing beyond it is needed yet -- and
// ok reports whether every send succeeded.
//
// since advances past a filtered-out event (sseVisible false) exactly as it
// would for a sent one: EventsSince only ever returns events after since, so
// leaving since behind a filtered event would make the next catchUp refetch
// it forever.
func (s *sseStream) sendPage(backlog []events.Event, target string) (reachedTarget, ok bool) {
	for _, e := range backlog {
		cursor := storecontract.EventCursor(e.At, e.ID)
		if cursor <= s.since {
			continue
		}
		if sseVisible(e) && !s.send(e) {
			return false, false
		}
		s.since = cursor
		if target != "" && cursor >= target {
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
func (s *sseStream) drain(ctx context.Context, conn <-chan liveUpdate, stale <-chan struct{}, logs <-chan logging.Entry) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-stale:
			return
		case entry, ok := <-logs:
			if !ok {
				logs = nil
				continue
			}
			if !s.sendLog(entry) {
				return
			}
		case update := <-conn:
			if update.topic == "tasks" {
				e, ok := update.data.(events.Event)
				if ok && !s.relayTask(ctx, e) {
					return
				}
			} else if !s.sendLive(update) {
				return
			}
		}
	}
}

func (s *sseStream) relayTask(ctx context.Context, e events.Event) bool {
	cursor := storecontract.EventCursor(e.At, e.ID)
	if cursor <= s.since {
		return true
	}
	if !s.catchUp(ctx, cursor) {
		return false
	}
	if cursor > s.since {
		if sseVisible(e) && !s.send(e) {
			return false
		}
		s.since = cursor
	}
	return true
}
