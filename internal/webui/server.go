// Package webui implements the dashboard's HTTP API and serves the embedded SPA.
package webui

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/captureintake"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/ui"
)

type Server struct {
	// chatAsks holds the chat questions waiting for the operator's answer.
	chatAsks chatAsks
	// chatFiles holds the downloads for files chat turns sent.
	chatFiles chatFiles

	Store storecontract.TaskStore
	Log   *slog.Logger

	// Steps lists an execution's recorded steps. Nil reports none.
	Steps storecontract.StepReader

	// ConfigSource supplies the configuration projection GET /api/config
	// renders. Composition sets it to RemoteConfigView in a process that
	// only displays configuration; unset means this process builds the view
	// from the configuration it holds (LocalConfigView).
	ConfigSource ConfigViewSource

	// LogFeed is the daemon diagnostic stream. It is separate from Events,
	// which contains persisted task lifecycle activity only.
	LogFeed    *logging.Feed
	LogSources map[string]LogSource
	// TaskLogs reads task logs. Nil means this process cannot read them.
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

	// Curators is the daemon's live curator registry.
	// Backs GET /api/curators: registered names, per-curator health, and
	// recent activity. Optional: nil reports
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

	// Packages is the State Store's installed-package surface, behind the
	// extensions page. Nil answers 503.
	Packages storepkg.Installations

	// ApplyStatus reports which control-plane resource version each process
	// is running. Optional: nil renders an empty page rather than failing the
	// dashboard.
	ApplyStatus storecontract.ApplyStatusStore

	// Presence lists every service instance's record, behind the services
	// card. Nil answers 503.
	Presence storecontract.PresenceStore

	// Refusals counts the events the network rules refused, per source.
	Refusals storecontract.CaptureRefusalStore

	// Policies is the stored access policy chain, behind the policies page.
	// Nil answers 503.
	Policies access.PolicyStore
	// ReloadAccess rebuilds Access from the stored policies, so an edit here
	// applies to the next request. Nil leaves the periodic reload.
	ReloadAccess func(context.Context) error
	// now is the clock applyState reads staleness against. Nil means
	// time.Now; tests set it to pin a record's age.
	now func() time.Time

	// Token gates access. Empty means no check, which is how loopback binds
	// stay frictionless -- see IsLoopback.
	Token string

	// Authenticate resolves a presented credential to an identity. Non-nil
	// replaces the shared token.
	Authenticate func(context.Context, string) (identity.Identity, error)

	// PersonalTokens manages the signed-in person's API tokens. Nil answers
	// Unavailable.
	PersonalTokens identity.PersonalTokens

	// Login drives the provider's browser sign-in. Nil removes the sign-in
	// routes.
	Login identity.LoginFlow

	// Access evaluates the policy chain, Principals builds the request principal
	// and Denials records refusals. All three or none; nil leaves the credential
	// check as the only gate.
	Access     access.Authorizer
	Principals access.PrincipalSource
	Denials    access.DenialRecorder

	// Orgs serves the org pages: the caller's org, its workspaces, members and
	// agent assignments. Nil answers the org routes Unavailable.
	Orgs org.API

	// Events publishes operator actions so they reach the task timeline and
	// the live activity stream. Optional: nil means the action is recorded
	// in the store but invisible to anyone watching.
	Events EventPublisher

	// Captures backs the dashboard's read of captured events: the
	// GET /api/captures list and the mapping preview's by-ID scan.
	// Optional: nil makes the list
	// answer {"enabled": false} rather than the dashboard failing to start.
	Captures storecontract.CaptureStore
	// CaptureMaxEvents is the operator's configured [capture] max_events,
	// used to bound captureByID's scan window (api_mapping.go).
	// CaptureIntake is the write half's mount.
	CaptureMaxEvents int
	// CaptureIntake serves unauthenticated POST /webhooks/capture/{source}. Nil
	// removes the route.
	CaptureIntake http.Handler

	// Mappings persists payload field mappings.
	// Optional: nil makes every /api/mappings route answer 503 rather than
	// the dashboard failing to start.
	Mappings storecontract.MappingStore

	// EventTypes persists event types. Optional:
	// nil reports the inspector's event types as disabled.
	EventTypes storecontract.EventTypeStore

	// Bindings persists playbook bindings: matcher + mapping + workflow
	// triples that turn a captured webhook into an archie task.
	// Optional: nil makes every
	// /api/bindings route answer 503 rather than the dashboard failing to start.
	Bindings storecontract.BindingStore
	// Dispatches reads the binding dispatch ledger. Nil answers the dispatch
	// routes 503 and lists bindings without their last outcome.
	Dispatches storecontract.DispatchLedger

	// Sources persists capture sources and their signing setting.
	// Optional: nil makes every
	// /api/sources route answer 503 and marks no binding unsigned.
	Sources storecontract.SourceStore

	// HarnessSecrets reports each credential binding's OAuth token status, never
	// the values. Nil answers 503.
	HarnessSecrets storecontract.HarnessSecretStore

	// HarnessTerminal opens the setup terminal, a duplex PTY session in an
	// ephemeral Kit container. Optional: nil answers /api/harness/terminal 503.
	// The implementation belongs to a process that owns containers; the
	// dashboard consumes it over a contract and never links one.
	HarnessTerminal HarnessTerminal

	// TelegramUpdateReportPath and TelegramUpdateChatID route a dashboard
	// update's post-restart report to Telegram. Either empty uses
	// UpdateReportPath.
	TelegramUpdateReportPath string
	TelegramUpdateChatID     int64

	// UpdateReportPath is where the update watchdog leaves a dashboard-initiated
	// update's outcome for relay on next boot. Empty disables it.
	UpdateReportPath string

	// RunningVersions reports, per component ID (see releaseupdate.Report.Verify),
	// the version that component reports for itself right now. Optional:
	// nil leaves every claim in a relayed update report Unverified rather
	// than Confirmed.
	RunningVersions func() map[string]string

	// TrustForwardedHeaders controls whether X-Forwarded-Proto and
	// X-Forwarded-Host are trusted when validating Origin on mutating
	// requests. When false, Origin scheme must match the direct connection (r.TLS).
	// When Cfg is present, Cfg.Get().Web.TrustForwardedHeaders is also checked.
	TrustForwardedHeaders bool

	mu     sync.Mutex
	conns  map[*liveSubscriber]struct{}
	latest map[string]liveUpdate
}

func (s *Server) trustForwardedHeaders() bool {
	if s == nil {
		return false
	}
	return s.TrustForwardedHeaders
}

// EventPublisher accepts events for the store and the live stream. The bus
// in cmd/archied satisfies it.
type EventPublisher interface {
	Publish(events.Event)
}

// Broadcast delivers a persisted task event through the shared stream hub.
func (s *Server) Broadcast(e events.Event) {
	s.publish(liveUpdate{topic: "tasks", data: e}, "")
}

func (s *Server) registerCoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/setup", s.handleSetup)
	mux.HandleFunc("GET /api/workflows", s.handleWorkflows)
	mux.HandleFunc("PUT /api/workflows/{id}/enabled", s.handleWorkflowEnabled)
	mux.HandleFunc("POST /api/work-requests", s.handleWorkRequest)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("GET /api/curators", instanceOnly(s.handleCurators))
	mux.HandleFunc("GET /api/captures", s.handleCaptures)
	mux.HandleFunc("GET /api/channels", instanceOnly(s.handleChannels))
	mux.HandleFunc("POST /api/channels/{id}/reload", instanceOnly(s.handleChannelReload))
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
	mux.HandleFunc("POST /api/bindings/{id}/approve", s.handleBindingTransition("approve", storecontract.BindingStore.ApproveBinding))
	mux.HandleFunc("POST /api/bindings/{id}/pause", s.handleBindingTransition("pause", storecontract.BindingStore.PauseBinding))
	mux.HandleFunc("POST /api/bindings/{id}/resume", s.handleBindingTransition("resume", storecontract.BindingStore.ResumeBinding))
	mux.HandleFunc("GET /api/bindings/{id}/dispatches", s.handleDispatches(func(id string) (storecontract.DispatchFilter, bool) {
		return storecontract.DispatchFilter{BindingID: id}, id != ""
	}))
	mux.HandleFunc("GET /api/captures/{id}/dispatches", s.handleDispatches(func(id string) (storecontract.DispatchFilter, bool) {
		return storecontract.DispatchFilter{CaptureID: id}, id != ""
	}))
	mux.HandleFunc("GET /api/tasks/{id}/dispatches", s.handleDispatches(func(id string) (storecontract.DispatchFilter, bool) {
		taskID, err := strconv.ParseInt(id, 10, 64)
		return storecontract.DispatchFilter{TaskID: taskID}, err == nil && taskID > 0
	}))
	mux.HandleFunc("GET /api/sources", s.handleSourcesList)
	mux.HandleFunc("POST /api/sources", s.handleSourceCreate)
	mux.HandleFunc("POST /api/sources/{path}/signing", s.handleSourceSigning)
	mux.HandleFunc("POST /api/sources/{path}/approve-unsigned", s.handleSourceApproveUnsigned)
	mux.HandleFunc("POST /api/sources/{path}/secret", s.handleSourceSecret)
	mux.HandleFunc("PUT /api/sources/{path}/name", s.handleSourceName)
	mux.HandleFunc("PUT /api/sources/{path}/delivery-header", s.handleSourceDeliveryHeader)
	mux.HandleFunc("DELETE /api/sources/{path}", s.handleSourceDelete)
}

func (s *Server) registerHarnessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/harness/bindings", s.handleHarnessBindings)
	mux.HandleFunc("GET /api/harness/terminal", s.handleHarnessTerminal)
}

func (s *Server) registerConfigAndLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/extensions", s.handleExtensions)
	mux.HandleFunc("POST /api/extensions", s.handleExtensionInstall)
	mux.HandleFunc("GET /api/extensions/catalogue", s.handleCatalogue)
	mux.HandleFunc("POST /api/extensions/catalogue/{name}/install", s.handleCatalogueInstall)
	mux.HandleFunc("POST /api/extensions/{name}/accept", s.handleExtensionAccept)
	mux.HandleFunc("PUT /api/extensions/{name}/enabled", s.handleExtensionEnabled)
	mux.HandleFunc("DELETE /api/extensions/{name}", s.handleExtensionRemove)
	mux.HandleFunc("GET /api/control-plane/catalog", s.handleControlPlaneCatalog)
	mux.HandleFunc("GET /api/control-plane/apply-status", instanceOnly(s.handleApplyStatus))
	mux.HandleFunc("GET /api/services", instanceOnly(s.handleServices))
	mux.HandleFunc("GET /api/access/policies", s.handleListPolicies)
	mux.HandleFunc("PUT /api/access/policies", s.handlePutPolicy)
	mux.HandleFunc("DELETE /api/access/policies", s.handleDeletePolicy)
	mux.HandleFunc("GET /api/control-plane/resources/{kind}", s.handleControlPlaneQuery)
	mux.HandleFunc("GET /api/control-plane/resources/{kind}/history", s.handleControlPlaneHistory)
	mux.HandleFunc("POST /api/control-plane/resources/{kind}/commands/{command}", s.handleControlPlaneCommand)
	mux.HandleFunc("GET /api/control-plane/audit", s.handleAudit)
	mux.HandleFunc("GET /api/identities", instanceOnly(s.handleIdentitiesList))
	mux.HandleFunc("GET /api/orgs", s.handleOrgsList)
	mux.HandleFunc("POST /api/orgs", s.handleOrgCreate)
	mux.HandleFunc("GET /api/orgs/{id}", s.handleOrgGet)
	mux.HandleFunc("GET /api/orgs/{id}/workspaces", s.handleOrgWorkspacesList)
	mux.HandleFunc("POST /api/orgs/{id}/workspaces", s.handleOrgWorkspaceCreate)
	mux.HandleFunc("GET /api/orgs/{id}/members", s.handleOrgMembersList)
	mux.HandleFunc("PUT /api/orgs/{id}/members/{identity}", s.handleOrgMemberSet)
	mux.HandleFunc("DELETE /api/orgs/{id}/members/{identity}", s.handleOrgMemberRemove)
	mux.HandleFunc("PUT /api/orgs/{id}/agents/{identity}", instanceOnly(s.handleOrgAgentAssign))
	mux.HandleFunc("GET /api/tokens", s.handlePersonalTokensList)
	mux.HandleFunc("POST /api/tokens", s.handlePersonalTokenCreate)
	mux.HandleFunc("DELETE /api/tokens/{id}", s.handlePersonalTokenRevoke)
	mux.HandleFunc("POST /api/identities", instanceOnly(s.handleIdentityCreate))
	mux.HandleFunc("POST /api/identities/{id}/{command}", instanceOnly(s.handleIdentityCommand))
	mux.HandleFunc("GET /api/config", instanceOnly(s.handleConfig))
	mux.HandleFunc("GET /api/logs", instanceOnly(s.handleLogs))
}

func (s *Server) registerChatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/chat/sessions", s.handleChatSessions)
	mux.HandleFunc("GET /api/chat/sessions/{id}/messages", s.handleChatMessages)
	mux.HandleFunc("GET /api/chat/sessions/{id}/turns", s.handleChatTurns)
	mux.HandleFunc("POST /api/chat/message", s.handleChatMessage)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("POST /api/chat/cancel", s.handleChatCancel)
	mux.HandleFunc("POST /api/chat/answer", s.handleChatAnswer)
	mux.HandleFunc("GET /api/chat/file/{token}", s.handleChatFile)
	mux.HandleFunc("POST /api/chat/persona", s.handleChatPersona)
	mux.HandleFunc("GET /api/chat/update", instanceOnly(s.handleChatUpdate))
	mux.HandleFunc("POST /api/chat/update/defer", instanceOnly(s.handleChatUpdateDefer))
	mux.HandleFunc("POST /api/chat/update/install", instanceOnly(s.handleChatUpdateInstall))
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	s.registerCoreRoutes(mux)
	s.registerTaskRoutes(mux)
	s.registerMappingAndBindingRoutes(mux)
	s.registerHarnessRoutes(mux)
	s.registerConfigAndLogRoutes(mux)
	s.registerChatRoutes(mux)

	mux.HandleFunc("GET /health/detailed", instanceOnly(s.handleHealthDetailed))
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
		// captureintake.Path is the single spelling of this route: the receiver
		// owns the route's semantics, so mounting re-spells nothing and a rename
		// there cannot silently unmount here.
		top.Handle(captureintake.Path, s.CaptureIntake)
	}
	// The chain runs after the credential check and before any handler: the
	// principal exists only once the credential resolved
	top.Handle("/", s.requireToken(s.authorize(mux)))
	return top
}

// handleHealthz is the unauthenticated liveness probe the update watchdog
// polls.
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

// decodeBody decodes the JSON request body into a T, answering 400 itself
// when the body is invalid.
func decodeBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return v, false
	}
	return v, true
}
