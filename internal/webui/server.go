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
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/ui"
)

type Server struct {
	Store store.TaskStore
	Log   *slog.Logger

	// Cfg backs the setup checklist and configuration views. Optional: the
	// dashboard degrades to task data alone when it is nil. Held through a
	// Holder so a reload swaps the snapshot atomically across handler
	// goroutines that would otherwise race on the raw shared pointer.
	//
	// In production this is the daemon's OWN Holder (main.go wires
	// web.Cfg = d.Cfg), so Set here publishes to the running daemon. A
	// config-mutating handler must never call Set directly: it goes
	// through the same path as reload -- apply to a copy, validate the
	// materialised config, persist, then Set.
	Cfg *config.Holder

	// Workflows is the executable registry snapshot supplied by composition.
	Workflows []workflow.Definition
	// WorkRequests admits dashboard requests through the same task-creation
	// boundary used by chat; it never invokes a workflow runner directly.
	WorkRequests gateway.TaskCreator
	// LogFeed is the daemon diagnostic stream. It is separate from Events,
	// which contains persisted task lifecycle activity only.
	LogFeed *logging.Feed
	// TaskLogs holds each task's persisted log output. Archiving a task
	// removes its log files here too, via TaskLogs.Remove -- nil (task
	// logging not configured) makes that a no-op.
	TaskLogs *logging.TaskRegistry

	// ConfigProvenance lists the files that produced the running config,
	// in precedence order. Held through atomic.Pointer so a reload can
	// swap it independently of Cfg. A reader may therefore observe the
	// new config alongside the old provenance (or vice versa); this is
	// display-only, and the skew is accepted rather than serialised.
	ConfigProvenance atomic.Pointer[[]ConfigOrigin]

	// LastReload reports the most recent config reload outcome, so the
	// dashboard can tell the operator when the running config is stale
	// (a reload failed validation and the daemon kept the previous
	// config). Optional: when nil, the /api/config response omits the
	// reload status.
	LastReload func() config.ReloadStatus

	// UpdateConfig applies a set of dotted-path config updates through
	// the same validate-persist-publish path as reload (wired by the
	// composition root). Optional: when nil, PATCH /api/config answers
	// 503. The handler never touches the Cfg Holder directly -- see the
	// Cfg field doc.
	UpdateConfig func(context.Context, map[string]any) error

	// ConfigOverrides lists the dotted config keys currently overridden
	// by the runtime overlay, so the dashboard can mark those rows and
	// offer a reset. Optional: when nil, the /api/config response omits
	// the overridden list.
	ConfigOverrides func(context.Context) ([]string, error)

	// ResetConfig deletes one runtime-overlay row and republishes file +
	// remaining overlay. Optional: when nil, POST /api/config/reset
	// answers 503. Like UpdateConfig, it never touches the Cfg Holder
	// directly.
	ResetConfig func(context.Context, string) error

	// Channels reports actual adapter lifecycle, independently of configuration
	// presence. Nil preserves the configuration-only fallback for tests and
	// minimal embedding.
	Channels *channels.Manager

	// ReloadChannel invokes a channel-specific reload seam. Only adapters that
	// explicitly support reload are wired here; nil means reload is unavailable.
	ReloadChannel func(context.Context, string) error

	// Memory backs the memory view. Optional: the section reports memory as
	// unavailable rather than failing when it is nil.
	Memory *memory.Manager

	// Chat exposes the same gateway router used by the conversational
	// channels. Optional: the dashboard simply omits chat when it is nil.
	Chat *ChatService

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

	// TaskStopper reaches work that is actually executing. A store row cannot
	// stop a goroutine or container; the daemon supplies this narrow runtime
	// seam so the Stop action cancels work before parking its task record.
	TaskStopper TaskStopper

	mu    sync.Mutex
	conns map[chan events.Event]struct{}
}

// ConfigOrigin explains one source file contributing to the effective
// configuration. Sources are ordered from lowest to highest precedence.
type ConfigOrigin struct {
	Path    string `json:"path"`
	Role    string `json:"role"`
	Layer   string `json:"layer"`
	Feature string `json:"feature,omitempty"`
}

// SetProvenance publishes a fresh provenance list after a config reload.
func (s *Server) SetProvenance(origins []ConfigOrigin) {
	s.ConfigProvenance.Store(&origins)
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

// TaskStopper interrupts one task currently executing in the daemon.
type TaskStopper interface {
	CancelTask(taskID int64) bool
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
	mux.HandleFunc("GET /api/workflows", s.handleWorkflows)
	mux.HandleFunc("POST /api/work-requests", s.handleWorkRequest)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("GET /api/channels", s.handleChannels)
	mux.HandleFunc("POST /api/channels/{id}/reload", s.handleChannelReload)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("PATCH /api/config", s.handleConfigUpdate)
	mux.HandleFunc("POST /api/config/reset", s.handleConfigReset)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/tasks/{id}/logs", s.handleTaskLogs)
	mux.HandleFunc("GET /api/logs/stream", s.handleLogStream)
	mux.HandleFunc("GET /api/memory", s.handleMemory)
	mux.HandleFunc("GET /api/chat/sessions", s.handleChatSessions)
	mux.HandleFunc("GET /api/chat/sessions/{id}/messages", s.handleChatMessages)
	mux.HandleFunc("POST /api/chat/message", s.handleChatMessage)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("POST /api/chat/cancel", s.handleChatCancel)
	mux.HandleFunc("POST /api/chat/persona", s.handleChatPersona)
	mux.HandleFunc("GET /api/chat/update", s.handleChatUpdate)
	mux.HandleFunc("POST /api/chat/update/defer", s.handleChatUpdateDefer)
	mux.HandleFunc("POST /api/chat/update/install", s.handleChatUpdateInstall)
	mux.HandleFunc("GET /api/chat/dangerous", s.handleChatDangerousState)
	mux.HandleFunc("POST /api/chat/dangerous/{kind}", s.handleChatDangerousRequest)
	mux.HandleFunc("POST /api/chat/dangerous/{id}/decision", s.handleChatDangerousDecision)
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
