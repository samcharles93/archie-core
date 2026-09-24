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

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/ui"
)

type Server struct {
	Store storecontract.TaskStore
	Log   *slog.Logger

	// ConfigSource supplies the configuration projection GET /api/config
	// renders. Composition sets it to RemoteConfigView in a process that
	// only displays configuration; unset means this process builds the view
	// from the configuration it holds (LocalConfigView).
	ConfigSource ConfigViewSource

	// WorkRequests admits dashboard requests through the same task-creation
	// boundary used by chat; it never invokes a workflow runner directly.
	WorkRequests messaging.TaskCreator
	// LogFeed is the daemon diagnostic stream. It is separate from Events,
	// which contains persisted task lifecycle activity only.
	LogFeed *logging.Feed
	// TaskLogs reads each task's persisted log output. Composition gives this
	// process whichever implementation it can use: the daemon (whose state
	// directory holds the files) its own *logging.TaskRegistry, and the
	// dashboard process, which owns no such directory, the State Store client
	// -- the read crosses a contract rather than opening a file
	// (docs/prds/ui-service-boundary.md). Nil means this process has no
	// task-log capability at all, which the handlers report as disabled rather
	// than as "the attempt has no log": those are different claims and only one
	// of them is about configuration.
	TaskLogs TaskLogSource

	// Channels reports actual adapter lifecycle, independently of configuration.
	// A process that hosts channels satisfies it from its own manager; the
	// dashboard process satisfies it by reading the State Store, where the
	// hosting process publishes (webui.RemoteChannelStatus).
	Channels ChannelStatusSource

	// ReloadChannel invokes a channel-specific reload seam. Only adapters that
	// explicitly support reload are wired here; nil means reload is unavailable.
	ReloadChannel func(context.Context, string) error

	// Skills is the webui-owned skill catalogue view; nil degrades
	// /api/skills to an empty page.
	Skills SkillCatalog

	// Curators is the daemon's live curator registry (epic archie-core-yp9).
	// Backs GET /api/curators: registered names, per-curator health, and
	// recent activity (archie-core-1786637489932-6). Optional: nil reports
	// an empty curator list rather than failing the dashboard.
	Curators CuratorStatus

	// Chat exposes the Gateway's wire-safe conversational contract. Optional:
	// the dashboard simply omits chat when it is nil.
	Chat *ChatService

	// Health runs the operator readiness probes served by /health/detailed.
	// Optional: nil answers 503 rather than fabricating a report, so a
	// deployment that has not wired probes never looks healthy by accident.
	Health *health.Registry

	// ControlPlane is the State Store's versioned runtime administration
	// contract. The HTTP adapter supplies operator attribution; browser JSON
	// never carries actor, source, or request IDs.
	ControlPlane controlpb.ControlPlaneServiceClient
	Identities   identity.Repository

	// ApplyStatus reports which control-plane resource version each process
	// is running. Optional: nil renders an empty page rather than failing the
	// dashboard (docs/prds/control-plane-apply-status.md).
	ApplyStatus storecontract.ApplyStatusStore
	// now is the clock applyState reads staleness against. Nil means
	// time.Now; tests set it to pin a record's age.
	now func() time.Time

	// Token gates access. Empty means no check, which is how loopback binds
	// stay frictionless -- see IsLoopback.
	Token string

	// Authenticate resolves a credential a request presented to the identity
	// that may act, and is the whole of the provider integration: verification
	// and binding both live behind it. Non-nil replaces the shared token, so
	// every request resolves to a named identity or is refused; nil keeps the
	// shared-token gate for an instance with no provider configured.
	Authenticate func(context.Context, string) (identity.Identity, error)

	// Login drives the provider's browser flow, so a person can sign in and the
	// dashboard learns who they are. Nil removes the sign-in routes, which is what
	// an instance with no provider configured gets. It is separate from
	// Authenticate because a caller may present a token without ever signing in
	// through a browser -- an agent does exactly that.
	Login identity.LoginFlow

	// Events publishes operator actions so they reach the task timeline and
	// the live activity stream. Optional: nil means the action is recorded
	// in the store but invisible to anyone watching.
	Events EventPublisher

	// Captures backs the dashboard's read of captured events: the
	// GET /api/captures list and the mapping preview's by-ID scan
	// (docs/prds/event-capture-storage.md). Optional: nil makes the list
	// answer {"enabled": false} rather than the dashboard failing to start.
	Captures storecontract.CaptureStore
	// CaptureMaxEvents is the operator's configured [capture] max_events,
	// used to bound captureByID's scan window (api_mapping.go).
	// CaptureIntake is the write half's mount.
	CaptureMaxEvents int
	// CaptureIntake serves POST /webhooks/capture/{source} on the bypass
	// mux, alongside /healthz: capture must accept unauthenticated
	// senders, so it cannot sit behind requireToken. The handler is owned
	// by internal/infrastructure/captureintake.Receiver; this package only
	// mounts the route and reads the rows back. The UI process composes
	// it from the cutover change (archie-core-8cda.5.4) -- it is the only
	// dashboard listener left, and a capture POST answered by the
	// token-gated mux would leave intake with no owner. Nil removes the
	// route, which is what a store without the capture contract gets:
	// two listeners with the same intake authority is what the boundary
	// forbids (docs/prds/ui-service-boundary.md:30-33).
	CaptureIntake http.Handler

	// Mappings persists payload field mappings (docs/prds/payload-field-mapping.md).
	// Optional: nil makes every /api/mappings route answer 503 rather than
	// the dashboard failing to start.
	Mappings storecontract.MappingStore

	// EventTypes persists event types (docs/prds/event-automation.md). Optional:
	// nil reports the inspector's event types as disabled.
	EventTypes storecontract.EventTypeStore

	// Bindings persists playbook bindings: matcher + mapping + workflow
	// triples that turn a captured webhook into an archie task
	// (docs/prds/webhook-intake-security.md). Optional: nil makes every
	// /api/bindings route answer 503 rather than the dashboard failing to start.
	Bindings storecontract.BindingStore

	// TelegramUpdateReportPath and TelegramUpdateChatID let a dashboard-
	// initiated update use the same post-restart notification route as a
	// Telegram-initiated update. The web UI has no durable chat identity, so
	// composition supplies an authorized Telegram recipient when one exists.
	// When either value is empty, UpdateReportPath remains in use.
	TelegramUpdateReportPath string
	TelegramUpdateChatID     int64

	// UpdateReportPath is where the update watchdog leaves the phase-2
	// outcome of a dashboard-initiated install for this process to relay on
	// its next boot -- the webui counterpart of
	// channels/telegram.Gateway.UpdateReportPath. Empty (the default unless
	// composition wires it) means dashboard-initiated updates get no
	// phase-2 report (archie-core-nln7): the operator only sees the
	// synchronous install result, never restart/health/version outcome.
	UpdateReportPath string

	// RunningVersions reports, per component ID (see releaseupdate.Report.Verify),
	// the version that component reports for itself right now. Optional:
	// nil leaves every claim in a relayed update report Unverified rather
	// than Confirmed.
	RunningVersions func() map[string]string

	// LogFile is the log file this process can read history from, for
	// GET /api/logs. Process-local: the file is on the daemon's disk, so
	// only a dashboard sharing that host can set it.
	LogFile string

	// TrustForwardedHeaders controls whether X-Forwarded-Proto and
	// X-Forwarded-Host are trusted when validating Origin on mutating
	// requests. When false, Origin scheme must match the direct connection (r.TLS).
	// When Cfg is present, Cfg.Get().Web.TrustForwardedHeaders is also checked.
	TrustForwardedHeaders bool

	mu    sync.Mutex
	conns map[chan events.Event]struct{}
}

func (s *Server) trustForwardedHeaders() bool {
	if s == nil {
		return false
	}
	return s.TrustForwardedHeaders
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

func (s *Server) registerCoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/setup", s.handleSetup)
	mux.HandleFunc("GET /api/workflows", s.handleWorkflows)
	mux.HandleFunc("POST /api/work-requests", s.handleWorkRequest)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("GET /api/curators", s.handleCurators)
	mux.HandleFunc("GET /api/captures", s.handleCaptures)
	mux.HandleFunc("GET /api/channels", s.handleChannels)
	mux.HandleFunc("POST /api/channels/{id}/reload", s.handleChannelReload)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/capabilities", s.handleCapabilities)
}

func (s *Server) registerTaskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks", s.handleTasks)
	mux.HandleFunc("GET /api/task-meta", s.handleTaskMeta)
	mux.HandleFunc("POST /api/tasks/{id}/action", s.handleTaskAction)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleTask)
	mux.HandleFunc("GET /api/tasks/{id}/attempts", s.handleTaskAttempts)
	mux.HandleFunc("GET /api/tasks/{id}/changes", s.handleTaskChanges)
	mux.HandleFunc("GET /api/tasks/{id}/debug", s.handleTaskDebug)
	mux.HandleFunc("GET /api/tasks/{id}/logs", s.handleTaskLogs)
	mux.HandleFunc("GET /api/tasks/{id}/logs/download", s.handleTaskLogDownload)
}

func (s *Server) registerMappingAndBindingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/event-types", s.handleEventTypes)
	mux.HandleFunc("POST /api/event-types", s.handleEventTypeCreate)
	mux.HandleFunc("PUT /api/event-types/{id}", s.handleEventTypeUpdate)
	mux.HandleFunc("DELETE /api/event-types/{id}", s.handleEventTypeDelete)
	mux.HandleFunc("GET /api/mappings", s.handleMappingsList)
	mux.HandleFunc("POST /api/mappings", s.handleMappingCreate)
	mux.HandleFunc("GET /api/mappings/{id}", s.handleMappingGet)
	mux.HandleFunc("PATCH /api/mappings/{id}", s.handleMappingUpdate)
	mux.HandleFunc("DELETE /api/mappings/{id}", s.handleMappingDelete)
	mux.HandleFunc("POST /api/mappings/preview", s.handleMappingPreview)
	mux.HandleFunc("GET /api/bindings", s.handleBindingsList)
	mux.HandleFunc("POST /api/bindings", s.handleBindingCreate)
	mux.HandleFunc("GET /api/bindings/{id}", s.handleBindingGet)
	mux.HandleFunc("PATCH /api/bindings/{id}", s.handleBindingUpdate)
	mux.HandleFunc("DELETE /api/bindings/{id}", s.handleBindingDelete)
	mux.HandleFunc("POST /api/bindings/{id}/approve", s.handleBindingApprove)
}

func (s *Server) registerConfigAndLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/control-plane/catalog", s.handleControlPlaneCatalog)
	mux.HandleFunc("GET /api/control-plane/apply-status", s.handleApplyStatus)
	mux.HandleFunc("GET /api/control-plane/resources/{kind}", s.handleControlPlaneQuery)
	mux.HandleFunc("GET /api/control-plane/resources/{kind}/history", s.handleControlPlaneHistory)
	mux.HandleFunc("POST /api/control-plane/resources/{kind}/commands/{command}", s.handleControlPlaneCommand)
	mux.HandleFunc("GET /api/control-plane/watch/{kind}", s.handleControlPlaneWatch)
	mux.HandleFunc("GET /api/identities", s.handleIdentitiesList)
	mux.HandleFunc("GET /api/identities/watch", s.handleIdentitiesWatch)
	mux.HandleFunc("POST /api/identities", s.handleIdentityCreate)
	mux.HandleFunc("POST /api/identities/{id}/{command}", s.handleIdentityCommand)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/logs/stream", s.handleLogStream)
}

func (s *Server) registerChatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/chat/sessions", s.handleChatSessions)
	mux.HandleFunc("GET /api/chat/sessions/{id}/messages", s.handleChatMessages)
	mux.HandleFunc("GET /api/chat/sessions/{id}/turns", s.handleChatTurns)
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
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	s.registerCoreRoutes(mux)
	s.registerTaskRoutes(mux)
	s.registerMappingAndBindingRoutes(mux)
	s.registerConfigAndLogRoutes(mux)
	s.registerChatRoutes(mux)

	mux.HandleFunc("GET /health/detailed", s.handleHealthDetailed)
	mux.HandleFunc("GET /api/stream", s.handleSSE)
	mux.Handle("GET /", s.assets())

	top := http.NewServeMux()
	top.HandleFunc("GET /healthz", s.handleHealthz)
	top.HandleFunc("GET /health", s.handleHealth)
	// The sign-in routes sit on the bypass mux because they are how a caller
	// becomes authenticated: gating them behind the credential they obtain would
	// make signing in impossible.
	if s.Login != nil {
		top.HandleFunc(loginRoute, s.handleLogin)
		top.HandleFunc(loginCallbackRoute, s.handleCallback)
	}
	if s.CaptureIntake != nil {
		top.Handle(captureIntakeRoute, s.CaptureIntake)
	}
	top.Handle("/", s.requireToken(mux))
	return top
}

// captureIntakeRoute is the bypass-mux route the host process's capture
// receiver mounts on. captureintake.Path is the authority for the route's
// semantics; this literal exists so webui can mount an http.Handler without
// depending on the receiver's concrete package.
const captureIntakeRoute = "POST /webhooks/capture/{source}"

// handleHealthz is a liveness probe for local, unauthenticated callers --
// most notably the update watchdog script, which polls it after restarting
// archied to decide whether the new version came up or the update needs to
// be rolled back (see scripts/archie-update-watchdog). It deliberately
// bypasses requireToken: the token protects the dashboard from remote
// access, not this process's own local restart tooling.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
