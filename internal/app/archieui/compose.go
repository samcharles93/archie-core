package archieui

import (
	"context"
	"log/slog"
	"time"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/captureintake"
	"github.com/samcharles93/archie-core/internal/webhookguard"
	"github.com/samcharles93/archie-core/internal/webui"
)

// deps are the resolved inputs the dashboard is built from: two remote
// contracts, the process's own logger and options, and the readiness registry
// assembled over those same contracts.
type deps struct {
	Options      Options
	Log          *slog.Logger
	Store        storecontract.TaskStore
	Chat         messaging.ChatContract
	Health       *health.Registry
	ControlPlane controlpb.ControlPlaneServiceClient
	Identities   identity.Repository
	// Authenticate resolves a presented credential to the identity that may
	// act. Nil when no provider is configured, which keeps the shared token.
	Authenticate func(context.Context, string) (identity.Identity, error)
	// Login drives the browser sign-in flow, or nil when no provider is
	// configured for it.
	Login identity.LoginFlow
}

// compose builds the dashboard server for the UI process. It sets exactly the
// fields a contract already carries plus this process's own settings, and
// leaves every remaining field at its zero value on purpose: each one is a
// daemon-local runtime handle with no contract behind it, and the handlers
// already degrade to a documented 501/503/empty response rather than panicking
// (docs/prds/ui-service-boundary.md:130-131).
//
// What is deliberately absent, and which bead fills it:
//
//   - Cfg, LastReload, UpdateConfig, ConfigOverrides, ResetConfig,
//     UpdateRepoField: the configuration owner stays the daemon. The read
//     crosses as the snapshot ConfigSource renders; the write routes answer
//     503 and the page hides their controls (archie-core-ymut).
//   - LogFeed, Events, Channels, ReloadChannel, Curators, Memory,
//     Workflows, WorkRequests, RunningVersions, Chat.Updates,
//     UpdateReportPath: no contract exists for these yet.
//     Defining one amends the owning service's contract first
//     (docs/prds/ui-service-boundary.md:205-207); the route migration is
//     archie-core-8cda.5.3.
//   - TaskLogs: wired below from the store client's TaskLogStore contract.
//     Task log files live in the state directory the State Store owns, so this
//     process reads one attempt's log over that contract rather than opening
//     the file itself -- which would only work on a single host and is exactly
//     what the boundary forbids (archie-core-iaqx).
//   - Captures, CaptureIntake, CaptureMaxEvents: wired below. From the
//     cutover change (archie-core-8cda.5.4) this process is the only
//     listener serving POST /webhooks/capture/{source}: the receiver is a
//     persistence shim over the State Store contract, and the daemon's
//     binding-dispatch loop keeps consuming captures from the same store,
//     so the HTTP front door moved here without moving the owner of work
//     intake. Mounting it before the daemon's listener was gone would have
//     given two listeners the same intake authority, which the PRD forbids
//     (docs/prds/ui-service-boundary.md:30-33).
func compose(d deps) *webui.Server {
	srv := &webui.Server{
		Store:                 d.Store,
		Log:                   d.Log,
		Token:                 d.Options.Token,
		Authenticate:          d.Authenticate,
		Login:                 d.Login,
		TrustForwardedHeaders: d.Options.trustForwardedHeaders(),
		Health:                d.Health,
		ControlPlane:          d.ControlPlane,
		Identities:            d.Identities,
	}
	if snapshots, ok := d.Store.(storecontract.ConfigSnapshotStore); ok {
		srv.ConfigSource = webui.RemoteConfigView(snapshots)
	}
	// Channel lifecycle follows the same split as the config view: the process
	// hosting the channels publishes, this one reads. Withholding it left
	// /api/channels answering with nothing while channels were running
	// (archie-core-8cda.6.8).
	if channels, ok := d.Store.(storecontract.ChannelStatusStore); ok {
		srv.Channels = webui.RemoteChannelStatus(channels)
	}
	// Mappings and bindings are ratified State Store contracts, and the same
	// client already carries them: withholding them would degrade two pages
	// that have an owner, which is a different thing from the intake surfaces
	// below.
	if applyStatus, ok := d.Store.(storecontract.ApplyStatusStore); ok {
		srv.ApplyStatus = applyStatus
	}
	if mappings, ok := d.Store.(storecontract.MappingStore); ok {
		srv.Mappings = mappings
	}
	if bindings, ok := d.Store.(storecontract.BindingStore); ok {
		srv.Bindings = bindings
	}
	if d.Chat != nil {
		srv.Chat = &webui.ChatService{Contract: d.Chat}
	}
	// The task-log read crosses the State Store contract: the files live in
	// the state directory that process owns, so this one asks for the log
	// rather than opening a path (docs/prds/ui-service-boundary.md). Withholding
	// it would degrade a page that has an owner, and would leave the dashboard
	// claiming task logging was not enabled -- see wireTaskLogs.
	wireTaskLogs(d, srv)
	wireCaptureSurfaces(d, srv)
	return srv
}

// wireTaskLogs attaches the task-log read when the store client carries the
// contract, and logs the absence when it does not: a store without it leaves
// the page reporting that this process cannot read logs, which is true, rather
// than that the attempt has none, which it cannot know.
func wireTaskLogs(d deps, srv *webui.Server) {
	logs, ok := d.Store.(storecontract.TaskLogStore)
	if !ok {
		if d.Store != nil {
			d.Log.Warn("task logs unavailable: state store does not implement TaskLogStore")
		}
		return
	}
	srv.TaskLogs = logs
}

// wireCaptureSurfaces attaches the capture read, the source editor and the
// intake receiver. All resolve from d.Store's contracts: CaptureStore backs
// the inspector's list and the mapping preview's scan window, SourceStore
// resolves the source whose signing setting verifies an event. A store that
// does not implement one degrades that half with a warning rather than
// aborting the process, mirroring the daemon's own adapter selection.
func wireCaptureSurfaces(d deps, srv *webui.Server) {
	captures, hasCaptures := d.Store.(storecontract.CaptureStore)
	sources, hasSources := d.Store.(storecontract.SourceStore)
	if !hasCaptures {
		d.Log.Warn("capture storage unavailable: state store does not implement CaptureStore")
		return
	}
	var resolver captureintake.SourceResolver
	if hasSources {
		srv.Sources = sources
		resolver = sources
	} else {
		d.Log.Warn("source store unavailable: state store does not implement SourceStore; no capture will dispatch")
	}

	capture := d.Options.Capture.withDefaults()
	srv.Captures = captures
	srv.CaptureMaxEvents = capture.MaxEvents
	srv.CaptureIntake = &captureintake.Receiver{
		Captures:     captures,
		Limiter:      webhookguard.NewRateLimiter(capture.RatePerSecond, capture.RateBurst, time.Now),
		Sources:      resolver,
		Retention:    capture.Retention,
		MaxEvents:    capture.MaxEvents,
		MaxBodyBytes: int64(capture.MaxBodyBytes),
		Publish:      captureArrivalPublisher(d),
		Log:          d.Log,
	}
}

// captureArrivalPublisher persists a capture's arrival event through the
// State Store, where the daemon's bus drain persists every other activity
// event; the event pump then delivers it to every connected browser. A nil
// store records captures without announcing them (compose's test-only
// shape); an insert failure is logged, not surfaced -- the capture row is
// already durable, and the inspector refetches on its next view.
func captureArrivalPublisher(d deps) func(context.Context, events.Event) {
	if d.Store == nil {
		return nil
	}
	return func(ctx context.Context, e events.Event) {
		ctx, cancel := context.WithTimeout(ctx, captureEventTimeout)
		defer cancel()
		if _, err := d.Store.InsertEvent(ctx, e); err != nil {
			d.Log.Warn("capture arrival event not published", "err", err)
		}
	}
}

// captureEventTimeout bounds the synchronous announce of one capture
// arrival. A capture POST already paid the rate limiter and a store insert;
// a hung State Store must not hold the sender's webhook past this.
const captureEventTimeout = 5 * time.Second
