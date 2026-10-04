// state_store.go composes the standalone archie-state-store process. It mirrors
// gateway.go / cmd/archie-gateway: the binary is a thin main that passes
// process inputs into Run, which serves the task and event-capture
// stores from Postgres, registers every StateStore gRPC handler (the same service .4.2 serves
// in-process on the daemon), and shuts down cleanly on ctx cancellation.
package statestore

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	registry "github.com/samcharles93/archie-core/internal/infrastructure/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
	"github.com/samcharles93/archie-core/internal/secret"
)

// Options contains process inputs for the standalone State Store.
type Options struct {
	Config  string
	Overlay string
	// Listen is the gRPC listen address. Loopback binds (127.0.0.1, ::1,
	// localhost) serve insecure with no token; any non-loopback address
	// requires a non-empty token (or TLS), else the process fails closed
	Listen string
	// Token is the bearer token the State Store server validates for a
	// non-loopback listener. Empty falls back to [services.state].target_token.
	Token string
	// ReadyAddr, when non-empty, starts an HTTP readiness surface on the
	// address: GET /healthz (liveness) and GET /health/detailed (the
	// state_db probe). Empty disables it.
	ReadyAddr string
}

// Run serves the task and event-capture stores from Postgres,
// registers every StateStore gRPC handler, and serves until ctx is cancelled.
// The conversation store belongs to the separate archie-gateway process. The
// store service owns its own DB lifecycle, so
// b.cleanup() is the sole owner closing b.st here (in-process owner).
func Run(ctx context.Context, options Options) error { //nolint:cyclop // the composition root's setup sequence is deliberately flat and sequential
	b := newServer()
	defer b.cleanup()
	if err := b.loadConfig(ctx, options.Config, options.Overlay); err != nil {
		return err
	}
	listen, err := servicekit.ResolveListen("state", options.Listen, b.cfg.Services.Get(config.ServiceNameState).Listen)
	if err != nil {
		return err
	}
	b.log = b.log.With("component", "state-store", "listen", listen)
	if err := b.openStateStore(ctx); err != nil {
		return err
	}
	identityStore, ok := b.st.(interface {
		BootstrapIdentities(context.Context, []string) error
	})
	if !ok {
		return fmt.Errorf("state store does not support identity bootstrap")
	}
	legacyNames := servicekit.IdentityNames(b.cfg)
	if err := identityStore.BootstrapIdentities(ctx, legacyNames); err != nil {
		return fmt.Errorf("bootstrap identities: %w", err)
	}
	// The resumable default org/workspace upgrade runs before this process
	// serves anything: the State Store refuses scoped calls until its upgrade
	// has finished, rather than serving records with no org.
	// A store
	// without the upgrader is one that owns no tenant boundary yet, so the
	// absence degrades to "no upgrade" rather than aborting boot.
	if upgrader, ok := b.st.(org.Upgrader); ok {
		if err := upgrader.UpgradeDefaultOrg(ctx); err != nil {
			return fmt.Errorf("upgrade default org: %w", err)
		}
	}
	if err := b.seedAndValidatePolicies(ctx); err != nil {
		return err
	}
	// The validating side's control plane, built here at the composition root
	// before the first definition is read or replaced.
	resources, ok := b.st.(controlplane.ResourceStore)
	if !ok {
		return fmt.Errorf("state store does not support control-plane resources")
	}
	control, err := openStateStoreControlPlane(resources)
	if err != nil {
		return err
	}
	_, skipped, err := control.ImportConfig(ctx, b.cfg)
	if err != nil {
		return fmt.Errorf("import control-plane resources: %w", err)
	}
	b.reportUnseededResources(skipped)

	token := options.Token
	if token == "" {
		token = b.cfg.Services.ResolvedToken(config.ServiceNameState, b.secrets.Getenv)
	}
	grants := &staterpc.TaskGrants{}
	//nolint:contextcheck // grpc.StreamServerInterceptor has no context.Context parameter; TaskGrants.StreamInterceptor derives its context from stream.Context() instead
	opts, loopback, err := stateStoreServerOpts(listen, token, grants)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return fmt.Errorf("listen for state store: %w", err)
	}
	defer listener.Close()
	b.log.Info("archie-state-store running", "addr", listener.Addr().String(), "token_required", !loopback)

	if err := b.startOptionalSurfaces(ctx, options); err != nil {
		return err
	}
	// Boot is over and every listener this process serves is bound: the gRPC
	// contract below and, when -ready-addr asked for one, the readiness HTTP
	// surface. systemd's READY=1 asserts the same fact, so the announcement
	// goes out here rather than from a second notion of "started".
	// Not earlier: a readiness surface that fails to bind
	// fails the boot, and READY must not precede it. Not later: Serve begins
	// accepting the moment it is called.
	b.announceReady()

	deps := b.stateStoreDeps(grants)
	deps.ControlPlane = control
	return serveStateStore(ctx, listener, deps, opts)
}

// seedAndValidatePolicies seeds the shipped role policies for every org that
// lacks them, grants the agent identities their org's shipped developer role,
// and re-validates every stored policy this process serves. An invalid org,
// workspace or object policy is logged here and named as a problem by the
// engines the consumers build; an invalid instance policy fails this boot --
// the store stops serving until it is fixed. A store without the access
// surfaces degrades: there is no chain to seed.
func (b *server) seedAndValidatePolicies(ctx context.Context) error {
	policies, ok := b.st.(access.PolicyStore)
	if !ok {
		return nil
	}
	if agents, ok := b.st.(interface {
		EnsureAgentOrgMembership(context.Context) error
	}); ok {
		if err := agents.EnsureAgentOrgMembership(ctx); err != nil {
			return fmt.Errorf("grant agent org membership: %w", err)
		}
	}
	if orgs, ok := b.st.(org.Repository); ok {
		list, err := orgs.ListOrgs(ctx)
		if err != nil {
			return fmt.Errorf("list orgs for policy seeding: %w", err)
		}
		for _, o := range list {
			if err := policies.EnsureShippedOrgPolicies(ctx, o.ID); err != nil {
				return fmt.Errorf("seed shipped org policies for %s: %w", o.ID, err)
			}
		}
	}
	stored, err := policies.ListPolicies(ctx)
	if err != nil {
		return fmt.Errorf("load stored policies: %w", err)
	}
	engine, err := infraaccess.New(stored)
	if err != nil {
		return fmt.Errorf("validate stored policies: %w", err)
	}
	for _, problem := range engine.Problems() {
		b.log.Error("stored access policy is invalid and denies its level",
			"policy", problem.Policy.ID, "level", problem.Policy.Level, "err", problem.Err)
	}
	return nil
}

// reportUnseededResources logs every kind ImportConfig could not seed.
//
// Reported, not fatal: a stale TOML value in a setting the database owns must
// not stop this process from starting. The kind stays ABSENT rather than
// half-written, so the file document's value is the one in effect for every
// reader -- the layering leaves a kind with no stored value alone
// (controlplane.resourceReader) -- and the operator's fix is still the file.
// Whether the value may RUN is decided where it is used: boot.runtimeConfig
// validates the effective document and refuses to start archied or the Gateway
// with one it cannot run. The seed is retried on this process's next start, so
// a corrected config.toml takes effect when archie-state-store restarts.
//
// It is a method rather than a loop in Run because the boot sequence
// is at its complexity budget: the branch belongs to the report's own policy,
// not to the sequence that triggers it.
func (b *server) reportUnseededResources(skipped []controlplane.SeedSkip) {
	for _, skip := range skipped {
		b.log.Error("control-plane resource not seeded; the kind stays absent and the file's value is the one in effect",
			"kind", skip.Kind, "err", skip.Err)
	}
}

// openStateStoreControlPlane builds the State Store's control plane server: the
// validating side of the workflow step vocabulary, registered here at the
// composition root before the first definition is read or replaced. The same
// provider set (internal/infrastructure/workflowsteps) is what archie-agent
// registers before it compiles one, so within one build a definition this
// server admits is compilable there; a skewed deploy is only fixed by a
// matching deploy.
func openStateStoreControlPlane(resources controlplane.ResourceStore) (*controlplane.Server, error) {
	steps, err := workflowsteps.NewManager()
	if err != nil {
		return nil, fmt.Errorf("register workflow step vocabulary: %w", err)
	}
	server, err := controlplane.NewServer(resources, steps)
	if err != nil {
		return nil, fmt.Errorf("build control plane server: %w", err)
	}
	return server, nil
}

// openStateStore composes the State Store process's persistence: it resolves
// secrets (for the binding cipher), opens and migrates the process-scoped
// PostgreSQL pool (fail closed), and serves the task and event-capture stores
// from it.
// It does NOT open gateway chat sessions -- those live in the separate
// archie-gateway process on their own store and are out of state-store scope.
func (b *server) openStateStore(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	// The State Store serves the installed packages extension engines come from,
	// so it resolves its own references through the env engine only.
	secrets := secret.NewRegistry()
	b.secrets = secrets
	bindingCipher, err := bindingCipherFromConfig(cfg, secrets)
	if err != nil {
		log.Error("configure bindings cipher", "err", err)
		return err
	}
	// Open the PostgreSQL pool and apply the schema before anything else: the
	// State Store fails closed without a working database_url.
	if err := b.openStateStorePool(ctx); err != nil {
		return err
	}
	b.openTaskStore()
	b.openEDAStore(bindingCipher)
	return nil
}

// stateStoreDeps assembles the store surfaces the StateStore service fronts.
// b.st is the narrow storecontract.TaskStore; the concrete store also implements
// the capture/mapping/binding surfaces, so each is asserted here (the same
// pattern the daemon's wireWebStoreSurfaces uses) and a store that lacks one
// degrades that group rather than aborting boot.
func (b *server) stateStoreDeps(grants *staterpc.TaskGrants) staterpc.Deps {
	deps := staterpc.Deps{Tasks: b.st, Log: b.log, Grants: grants}
	if b.pg != nil {
		packages := storepkg.Service{
			Registry: registry.OCIRegistry{},
			Store:    postgres.NewInstalledPackages(b.pg),
		}
		// A projection failure degrades the org resources a package would have
		// contributed to, the way a group above degrades: the State Store boots
		// and reports, rather than refusing to serve the task tables.
		projections, err := newPackageProjections(b.pg)
		if err != nil {
			b.log.Error("build package projections", "err", err)
		} else {
			packages.Projections = projections
		}
		deps.Packages = packages
	}
	if identities, ok := b.st.(identity.Repository); ok {
		deps.Identities = identities
	}
	// Subject bindings are asserted separately from the repository facade: only
	// the authenticating path resolves a verified subject, and a store that
	// cannot is one where the dashboard cannot name its caller rather than one
	// that fails to boot.
	if bindings, ok := b.st.(identity.SubjectBinding); ok {
		deps.SubjectBindings = bindings
	}
	// Task logs live in the state directory, which this process owns, and the
	// dashboard process owns no such directory -- so this is where a task-log
	// read is served from. The reader is
	// the daemon's own registry over the configured state_dir.
	//
	// The nil check is on the registry, not on the interface it is assigned
	// to: a nil *logging.TaskRegistry stored in this field produces a non-nil
	// interface holding a nil pointer, which the server's own
	// `deps.TaskLogs == nil` guard would not catch. Leaving the field unset is
	// what makes the RPC answer Unavailable -- the honest "this process cannot
	// read logs" -- instead of depending on nil-receiver behaviour to notice.
	if b.taskLogs != nil {
		deps.TaskLogs = b.taskLogs
	}
	// The event-capture contracts are served by the event-capture store, not
	// the task store. BindingTaskCreator stays below on the
	// task store, because creating a task is the one thing it still does.
	if b.eda != nil {
		deps.Captures = b.eda
		deps.Refusals = b.eda
		deps.Mappings = b.eda
		deps.Bindings = b.eda
		deps.BindingDispatcher = b.eda
		deps.PlaybookDispatcher = b.eda
		deps.EventTypes = b.eda
		deps.Sources = b.eda
		deps.MappingMatches = b.eda
		// HarnessSecrets rides the same encrypted-at-rest store as source
		// webhook secrets, under its
		// own domain separator (bindingcipher.HarnessSecretDomain).
		deps.HarnessSecrets = b.eda
	}
	// tool_call events project into the tool_calls collection on the same
	// event-capture store: this process legitimately owns both, so the
	// projection rides on the task-store surface it decorates. With no
	// event-capture store (or no task store) there is nothing to project
	// through, and Tasks passes through unwrapped rather than decorating a
	// nil operand.
	if b.eda != nil && b.st != nil {
		deps.Tasks = newToolCallProjectingTaskStore(b.st, b.eda, b.log)
	}
	if btc, ok := b.st.(storecontract.BindingTaskCreator); ok {
		deps.BindingTaskCreator = btc
	}
	b.executionDeps(&deps)
	b.accessDeps(&deps)
	if css, ok := b.st.(storecontract.ConfigSnapshotStore); ok {
		deps.ConfigSnapshots = css
	}
	if ps, ok := b.st.(storecontract.PresenceStore); ok {
		deps.Presence = ps
	}
	if as, ok := b.st.(storecontract.ApplyStatusStore); ok {
		deps.ApplyStatus = as
	}
	if cs, ok := b.st.(storecontract.ChannelStatusStore); ok {
		deps.ChannelStatus = cs
	}
	return deps
}

// executionDeps wires the execution-tree surfaces (workflow calls, step
// recording/reading, cancellation) from the same store, degrading each one
// independently the store lacks it rather than failing the boot.
func (b *server) executionDeps(deps *staterpc.Deps) {
	if wc, ok := b.st.(storecontract.WorkflowCaller); ok {
		deps.WorkflowCalls = wc
	}
	if sr, ok := b.st.(storecontract.StepRecorder); ok {
		deps.Steps = sr
	}
	if ec, ok := b.st.(storecontract.ExecutionCanceller); ok {
		deps.Canceller = ec
	}
	if sr, ok := b.st.(storecontract.StepReader); ok {
		deps.StepReader = sr
	}
}

// accessDeps wires the policy chain and its denial records from the same
// store. A store without them -- one that owns
// no tenant boundary yet -- degrades the access RPCs rather than failing the
// boot, the same pattern the other optional surfaces use.
func (b *server) accessDeps(deps *staterpc.Deps) {
	if ps, ok := b.st.(access.PrincipalSource); ok {
		deps.Principals = ps
	}
	if ps, ok := b.st.(access.PolicyStore); ok {
		deps.Policies = ps
		if owners, ok := b.st.(ownerDirectory); ok {
			deps.Policies = guardedPolicies{PolicyStore: ps, owners: owners}
		}
	}
	if ds, ok := b.st.(access.DenialStore); ok {
		deps.Denials = ds
	}
}

// serveStateStore registers the StateStore gRPC service and serves it until
// ctx ends, draining in-flight calls before returning (mirroring serveGateway
// in gateway.go, minus the gateway's session-store argument).
func serveStateStore(ctx context.Context, listener net.Listener, deps staterpc.Deps, opts []grpc.ServerOption) error {
	server := grpc.NewServer(opts...)
	staterpc.RegisterServer(server, deps)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		timer := time.AfterFunc(10*time.Second, server.Stop)
		server.GracefulStop()
		timer.Stop()
		return nil
	case err := <-serveErr:
		return err
	}
}

// startStateStoreReadiness starts a minimal HTTP readiness surface on
// readyAddr: GET /healthz and /health answer 200 as a liveness probe (the
// process is up), while GET /health/detailed runs the state_db readiness probe
// and answers 503 when the store is degraded. It is the standalone process's
// counterpart to the daemon's /healthz + /health/detailed (internal/webui).
func (b *server) startStateStoreReadiness(ctx context.Context, readyAddr string) error {
	registry := b.readinessRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /health/detailed", func(w http.ResponseWriter, r *http.Request) {
		report := registry.Run(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if report.Status != health.StatusOK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(report)
	})
	return b.serveHealth(ctx, readyAddr, mux, "state store readiness")
}

func (b *server) readinessRegistry() *health.Registry {
	return health.NewRegistry(readiness.NewStoreProbe(b.st))
}

// startOptionalSurfaces starts the HTTP surfaces this process serves beside
// the gRPC contract, each enabled only by its own flag.
//
// They are gathered here rather than branched inline because Run is
// at its complexity budget: a surface that is off by default belongs to its
// own decision, not to the boot sequence that triggers it -- the same reason
// reportUnseededResources is a method.
func (b *server) startOptionalSurfaces(ctx context.Context, options Options) error {
	if ps, ok := b.st.(storecontract.PresenceStore); ok {
		go presence.Run(ctx, presence.StateStore, servicekit.Build(), ps, b.readinessRegistry(), b.log)
	}
	if options.ReadyAddr != "" {
		if err := b.startStateStoreReadiness(ctx, options.ReadyAddr); err != nil {
			return err
		}
	}
	return nil
}

// stateStoreServerOpts applies the transport security boundary: a
// loopback listener is served insecure with no token; any non-loopback
// address requires a bearer token, else the process fails closed. It cannot
// enforce TLS/mTLS (an operator decision), so it narrows the non-loopback
// path to token auth -- the same confinement philosophy as the gateway's
// loopback-only --listen rule.
//
// The keepalive enforcement policy is not part of that boundary and is
// installed on both listeners: a loopback State Store serves the same dialers
// as a remote one (staterpc.Dial installs the client keepalive on every target,
// loopback included), and grpc's default policy would answer their idle
// watches with GOAWAY too_many_pings. See staterpc.ServerKeepaliveOption.
func stateStoreServerOpts(listen, token string, grants *staterpc.TaskGrants) (opts []grpc.ServerOption, loopback bool, err error) {
	loopback, err = staterpc.TargetIsLoopback(listen)
	if err != nil {
		return nil, false, err
	}
	keepalive := staterpc.ServerKeepaliveOption()
	if loopback {
		return []grpc.ServerOption{keepalive, grpc.ChainUnaryInterceptor(staterpc.UnaryActorInterceptor())}, true, nil
	}
	if token == "" {
		return nil, false, fmt.Errorf(
			"state store listen address %q is non-loopback; non-loopback exposure requires a bearer token (--token / [services.state].target_token) or TLS, mirroring the gateway's --listen confinement (docs/prds/state-store-contract.md §9)",
			listen,
		)
	}
	// grants.UnaryInterceptor/StreamInterceptor separate the daemon's own
	// administrative token (full access) from a container's task-scoped
	// grant (Update/Transition/InsertEvent on its own task ID only), rather
	// than the single all-or-nothing token check this replaced.
	return []grpc.ServerOption{
		keepalive,
		// The actor interceptor runs first so a task grant's own attribution
		// replaces whatever service name the container claimed.
		grpc.ChainUnaryInterceptor(staterpc.UnaryActorInterceptor(), grants.UnaryInterceptor(token)),
		grpc.ChainStreamInterceptor(grants.StreamInterceptor(token)),
	}, false, nil
}

// newPackageProjections builds the family projectors an installed package's
// contributions apply through: workflows, playbooks and agent profiles become
// the org resources the runtime reads live. The step vocabulary
// (stepVocabulary) is the same one this process's control plane validates
// dashboard edits with, so a definition the projector admits is compilable
// where it dispatches (openStateStoreControlPlane for the pairing).
func newPackageProjections(pool *pgxpool.Pool) (map[string]storepkg.FamilyProjector, error) {
	steps, err := workflowsteps.NewManager()
	if err != nil {
		return nil, fmt.Errorf("register workflow step vocabulary: %w", err)
	}
	workflows, err := controlplane.NewWorkflowPackageProjector(
		postgres.NewResources(pool), postgres.NewPackageContributions(pool), steps)
	if err != nil {
		return nil, fmt.Errorf("build workflow package projector: %w", err)
	}
	resources, ledger := postgres.NewResources(pool), postgres.NewPackageContributions(pool)
	playbooks, err := controlplane.NewPlaybookPackageProjector(resources, ledger)
	if err != nil {
		return nil, fmt.Errorf("build playbook package projector: %w", err)
	}
	profiles, err := controlplane.NewProfilePackageProjector(resources, ledger)
	if err != nil {
		return nil, fmt.Errorf("build profile package projector: %w", err)
	}
	return map[string]storepkg.FamilyProjector{
		storepkg.FamilyWorkflows: workflows,
		storepkg.FamilyPlaybooks: playbooks,
		storepkg.FamilyProfiles:  profiles,
	}, nil
}
