package archieui

import (
	"context"
	"log/slog"
	"time"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/events"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/captureintake"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
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
	// Access evaluates the policy chain; Principals assembles the request
	// principal; Denials records refusals.
	// Wired together or not at all.
	Access     *infraaccess.Live
	Principals access.PrincipalSource
	Denials    access.DenialRecorder
}

// compose builds the dashboard server from contract-backed dependencies.
// Unset fields answer 501/503 or empty.
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
	if packages, ok := d.Store.(storepkg.Installations); ok {
		srv.Packages = packages
	}
	if tokens, ok := d.Store.(identity.PersonalTokens); ok {
		srv.PersonalTokens = tokens
	}
	if steps, ok := d.Store.(storecontract.StepReader); ok {
		srv.Steps = steps
	}
	// Channel lifecycle follows the same split as the config view: the process
	// hosting the channels publishes, this one reads. Withholding it left
	// /api/channels answering with nothing while channels were running
	if channels, ok := d.Store.(storecontract.ChannelStatusStore); ok {
		srv.Channels = webui.RemoteChannelStatus(channels)
	}
	// Mappings and bindings are ratified State Store contracts, and the same
	// client already carries them: withholding them would degrade two pages
	// that have an owner, which is a different thing from the intake surfaces
	// below.
	if policies, ok := d.Store.(access.PolicyStore); ok {
		srv.Policies = policies
	}
	wireOrgSurface(srv, d.Store)
	if presence, ok := d.Store.(storecontract.PresenceStore); ok {
		srv.Presence = presence
	}
	if applyStatus, ok := d.Store.(storecontract.ApplyStatusStore); ok {
		srv.ApplyStatus = applyStatus
	}
	if mappings, ok := d.Store.(storecontract.MappingStore); ok {
		srv.Mappings = mappings
	}
	if bindings, ok := d.Store.(storecontract.BindingStore); ok {
		srv.Bindings = bindings
	}
	// Harness token status comes from the State Store; the setup terminal is not
	// available here.
	if secrets, ok := d.Store.(storecontract.HarnessSecretStore); ok {
		srv.HarnessSecrets = secrets
	}
	if eventTypes, ok := d.Store.(storecontract.EventTypeStore); ok {
		srv.EventTypes = eventTypes
	}
	if d.Chat != nil {
		srv.Chat = &webui.ChatService{Contract: d.Chat}
	}
	wireCatalog(d, srv)
	// The policy chain: wired together or not at all.
	srv.Refusals = refusalStore(d.Store)
	if d.Access != nil {
		srv.Access = d.Access
		srv.ReloadAccess = d.Access.Reload
	}
	srv.Principals = d.Principals
	srv.Denials = d.Denials
	wireTaskLogs(d, srv)
	wireCaptureSurfaces(d, srv)
	return srv
}

// wireOrgSurface attaches the dashboard's org surface when the state client
// carries it.
func wireOrgSurface(srv *webui.Server, store storecontract.TaskStore) {
	if orgs, ok := store.(org.API); ok {
		srv.Orgs = orgs
	}
}

// wireCatalog attaches the Gateway-owned surfaces the dashboard reads over the
// chat client: the skill catalogue and the curator registry. A client that
// cannot answer them leaves those pages empty rather than fabricating a set.
// Channel reload is deliberately left unwired: channels run in the Messaging
// Service, not the Gateway, so no Gateway client can serve one.
func wireCatalog(d deps, srv *webui.Server) {
	catalog, ok := d.Chat.(gatewayrpc.CatalogClient)
	if !ok {
		return
	}
	srv.Skills = skillCatalog{client: catalog}
	srv.Curators = curatorStatus{client: catalog}
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

// wireCaptureSurfaces attaches capture listing, source editing and intake. A
// store missing a contract degrades that part with a warning.
func wireCaptureSurfaces(d deps, srv *webui.Server) {
	if dispatches, ok := d.Store.(storecontract.DispatchLedger); ok {
		srv.Dispatches = dispatches
	}
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
		Delivery:     deliveryAuthorizer(d.Access),
		Refusals:     refusalStore(d.Store),
		Log:          d.Log,
	}
}

// captureArrivalPublisher persists a capture's arrival event. Failures are
// logged.
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

// deliveryAuthorizer keeps a missing chain a nil authorizer, which admits
// every sender.
func deliveryAuthorizer(chain *infraaccess.Live) access.DeliveryAuthorizer {
	if chain == nil {
		return nil
	}
	return chain
}

func refusalStore(store storecontract.TaskStore) storecontract.CaptureRefusalStore {
	refusals, _ := store.(storecontract.CaptureRefusalStore)
	return refusals
}
