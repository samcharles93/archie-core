// Package webui is archied's dashboard: a control room over the daemon's
// task board, activity stream and configuration.
//
// The Go side owns serving and the JSON API only; the frontend lives in the
// repo-root ui/ package, which exposes the built assets as an embedded FS.
// Handlers are split by concern -- one file per API area -- so a section can
// be added without growing a single file without end.
package webui

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/ui"
)

type Server struct {
	Store store.TaskStore
	Log   *slog.Logger

	// Cfg backs the setup checklist and configuration views. Optional: the
	// dashboard degrades to task data alone when it is nil.
	Cfg *config.Config

	// Memory backs the memory view. Optional: the section reports memory as
	// unavailable rather than failing when it is nil.
	Memory *memory.Manager

	// Token gates access. Empty means no check, which is how loopback binds
	// stay frictionless -- see IsLoopback.
	Token string

	// Issues closes the forge issue behind a rejected task. Optional: the
	// action still records the operator's decision without it, and says so
	// in the log. Deliberately the narrowest interface that does the job
	// rather than the whole forge client -- the dashboard has no business
	// commenting, labelling or opening PRs.
	Issues IssueCloser

	// Events publishes operator actions so they reach the task timeline and
	// the live activity stream. Optional: nil means the action is recorded
	// in the store but invisible to anyone watching.
	Events EventPublisher

	mu    sync.Mutex
	conns map[chan events.Event]struct{}
}

// IssueCloser closes the forge issue behind a task.
type IssueCloser interface {
	CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error
}

// EventPublisher accepts events for the store and the live stream. The bus
// in cmd/archied satisfies it.
type EventPublisher interface {
	Publish(events.Event)
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
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/setup", s.handleSetup)
	mux.HandleFunc("GET /api/tasks", s.handleTasks)
	mux.HandleFunc("POST /api/tasks/{id}/action", s.handleTaskAction)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleTask)
	mux.HandleFunc("GET /api/tasks/clear", s.handleClearTasks)
	mux.HandleFunc("GET /api/workflows", s.handleWorkflows)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("GET /api/channels", s.handleChannels)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/memory", s.handleMemory)
	mux.HandleFunc("GET /events", s.handleSSE)
	mux.Handle("GET /", s.assets())
	return s.requireToken(mux)
}

// assets serves the embedded dashboard, falling back to index.html so client
// routes deep-link correctly. Under the no_ui build tag the assets are absent
// and a plain explanation is served instead of a confusing 404.
func (s *Server) assets() http.Handler {
	if ui.DistDirFS == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "dashboard not built into this binary (no_ui)", http.StatusNotFound)
		})
	}
	files := http.FileServerFS(ui.DistDirFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(ui.DistDirFS, trimLeadingSlash(r.URL.Path)); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}

func trimLeadingSlash(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	if p == "" {
		return "."
	}
	return p
}

// Run serves until ctx ends.
func (s *Server) Run(ctx context.Context, listen string) error {
	srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
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
