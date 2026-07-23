// Package webui is archied's observability dashboard: task board,
// per-task timelines, workflow/stage metrics, and a live event tail
// over SSE. Read-only — the daemon is steered through GitHub, not here.
// Bind it to localhost or a tailnet address; it has no auth.
package webui

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

//go:embed index.html
var indexHTML []byte

type Server struct {
	Store *store.Store
	Log   *slog.Logger

	mu    sync.Mutex
	conns map[chan events.Event]struct{}
}

// Broadcast fans an (ID-stamped) event out to every connected SSE
// client; stalled clients drop events rather than blocking the caller.
func (s *Server) Broadcast(e events.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		select {
		case c <- e:
		default:
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/tasks", s.handleTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleTask)
	mux.HandleFunc("POST /api/tasks/clear", s.handleClearTasks)
	mux.HandleFunc("GET /events", s.handleSSE)
	return mux
}

// Run serves until ctx ends.
func (s *Server) Run(ctx context.Context, listen string) error {
	srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	s.Log.Info("web ui listening", "addr", "http://"+listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	statuses, err := s.Store.StatusCounts(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	workflows, _ := s.Store.WorkflowStats(ctx)
	stages, _ := s.Store.StageStats(ctx)
	days, _ := s.Store.TokensByDay(ctx, 14)
	writeJSON(w, map[string]any{
		"statuses": statuses, "workflows": workflows,
		"stages": stages, "tokens_by_day": days,
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Store.Tasks(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, tasks)
}

func (s *Server) handleClearTasks(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.Store.ClearTerminalTasks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"deleted": deleted})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	evs, err := s.Store.TaskEvents(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, evs)
}

// handleSSE streams events: catch-up from the store (?since=<event id>),
// then live from the broadcast hub.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
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

	if backlog, err := s.Store.EventsSince(r.Context(), since, 200); err == nil {
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
