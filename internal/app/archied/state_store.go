// state_store.go composes the standalone archie-state-store process. It mirrors
// gateway.go / cmd/archie-gateway: the binary is a thin main that passes
// process inputs into RunStateStore, which owns the single archie.db SQLite
// file, registers every StateStore gRPC handler (the same service .4.2 serves
// in-process on the daemon), and shuts down cleanly on ctx cancellation.
//
// See docs/prds/state-store-contract.md (rev. 2c) -- the single authoritative
// design surface -- and in particular §5 (binary layout), §9 (transport
// security boundary) and §11 (the store service owns its own DB lifecycle).
package archied

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
)

func configuredIdentityNames(cfg config.Config) []string {
	if len(cfg.Identities) == 0 {
		name := cfg.BotUser
		if name == "" {
			name = "archie"
		}
		return []string{name}
	}
	names := make([]string, 0, len(cfg.Identities))
	for _, value := range cfg.Identities {
		names = append(names, value.Name)
	}
	return names
}

// StateStoreOptions contains process inputs for the standalone State Store.
type StateStoreOptions struct {
	Config  string
	Overlay string
	// Listen is the gRPC listen address. Loopback binds (127.0.0.1, ::1,
	// localhost) serve insecure with no token; any non-loopback address
	// requires a non-empty token (or TLS), else the process fails closed
	// (state-store-contract.md §9).
	Listen string
	// Token is the bearer token the State Store server validates for a
	// non-loopback listener. Empty falls back to [services.state].target_token.
	Token string
	// ReadyAddr, when non-empty, starts an HTTP readiness surface on the
	// address: GET /healthz (liveness) and GET /health/detailed (the
	// state_db probe). Empty disables it.
	ReadyAddr string
}

// RunStateStore owns the single archie.db SQLite file, registers every
// StateStore gRPC handler, and serves until ctx is cancelled. It opens only
// the task store -- the gateway's session SQLite is owned by the separate
// archie-gateway process and is deliberately untouched (state-store-contract
// rev. 2c §12 step 8). The store service owns its own DB lifecycle, so
// b.cleanup() is the sole owner closing b.st here (in-process owner).
func RunStateStore(ctx context.Context, options StateStoreOptions) error {
	b := newBootstrap()
	defer b.cleanup()
	if err := b.loadConfig(ctx, options.Config, options.Overlay); err != nil {
		return err
	}
	listen, err := resolveServiceListen("state", options.Listen, b.cfg.Services.Get(config.ServiceNameState).Listen)
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
	legacyNames := configuredIdentityNames(b.cfg)
	if err := identityStore.BootstrapIdentities(ctx, legacyNames); err != nil {
		return fmt.Errorf("bootstrap identities: %w", err)
	}
	resources, ok := b.st.(controlplane.ResourceStore)
	if !ok {
		return fmt.Errorf("state store does not support control-plane resources")
	}
	// The validating side's control plane, built here at the composition root
	// before the first definition is read or replaced.
	control, err := openStateStoreControlPlane(resources)
	if err != nil {
		return err
	}
	versions, skipped, err := control.ImportConfig(ctx, b.cfg)
	if err != nil {
		return fmt.Errorf("import control-plane resources: %w", err)
	}
	b.reportUnseededResources(skipped)
	migrateLegacySchedules(ctx, control, b.cfg.DBPath, versions[controlplane.SchedulesKind], b.log)

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

	if options.ReadyAddr != "" {
		if err := b.startStateStoreReadiness(ctx, options.ReadyAddr); err != nil {
			return err
		}
	}

	deps := b.stateStoreDeps(grants)
	deps.ControlPlane = control
	return serveStateStore(ctx, listener, deps, opts)
}

// reportUnseededResources logs every kind ImportConfig could not seed.
//
// Reported, not fatal: a stale TOML value in a setting the database owns must
// not stop this process from starting (docs/prds/runtime-control-plane.md,
// "Bootstrap, migration, and recovery"). The kind stays ABSENT rather than
// half-written, so the file document's value is the one in effect for every
// reader -- the layering leaves a kind with no stored value alone
// (controlplane.resourceReader) -- and the operator's fix is still the file.
// Whether the value may RUN is decided where it is used: boot.runtimeConfig
// validates the effective document and refuses to start archied or the Gateway
// with one it cannot run. The seed is retried on this process's next start, so
// a corrected config.toml takes effect when archie-state-store restarts.
//
// It is a method rather than a loop in RunStateStore because the boot sequence
// is at its complexity budget: the branch belongs to the report's own policy,
// not to the sequence that triggers it.
func (b *boot) reportUnseededResources(skipped []controlplane.SeedSkip) {
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
//
// It is separate from RunStateStore so that function stays within its
// complexity budget, and step_vocabulary_test.go pins both the call to
// stepVocabulary below and RunStateStore's call to this function.
func openStateStoreControlPlane(resources controlplane.ResourceStore) (*controlplane.Server, error) {
	steps, err := stepVocabulary()
	if err != nil {
		return nil, fmt.Errorf("register workflow step vocabulary: %w", err)
	}
	server, err := controlplane.NewServer(resources, steps)
	if err != nil {
		return nil, fmt.Errorf("build control plane server: %w", err)
	}
	return server, nil
}

// migrateLegacySchedules carries a legacy cronstore file -- the old daemon's
// <state dir>/cron/jobs.json -- into the schedules resource, once, on a fresh
// seed. It moves only the jobs the schedules resource can hold: workflow
// jobs, the one delivery the router maps. Chat and unrecognised kinds were
// already dead deliveries in the old engine (the delivery router maps
// KindWorkflow alone), so they are reported and left in the legacy file
// rather than being allowed to refuse the replace the whole store depends on.
//
// Nothing here is fatal. The migration is a legacy-data courtesy: an
// unreadable or refused sidecar is logged and left in place for the next
// start, the way ImportConfig skips a seed the resource validator refuses --
// a value this process cannot use must not stop the one process every other
// process dials (docs/prds/runtime-control-plane.md, "Bootstrap, migration,
// and recovery"). A successful replace is marked by renaming the file to
// jobs.json.migrated, so the whole legacy list -- the skipped jobs included
// -- stays on disk for the operator; until the rename lands, the replaying
// request ID keeps the replace idempotent across restarts.
func migrateLegacySchedules(ctx context.Context, control *controlplane.Server, dbPath string, version int64, log *slog.Logger) {
	if version != 1 {
		return
	}
	path := filepath.Join(filepath.Dir(dbPath), "cron", "jobs.json")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Warn("legacy cron store not readable; schedules migration skipped and retried on the next start", "path", path, "err", err)
		return
	}
	jobs, err := readLegacyJobs(ctx, path)
	if err != nil {
		log.Error("legacy cron store unreadable; schedules migration skipped and retried on the next start", "path", path, "err", err)
		return
	}
	migratable := make([]cronstore.JobSpec, 0, len(jobs))
	for _, job := range jobs {
		if job.Kind == cronstore.KindWorkflow {
			migratable = append(migratable, job)
			continue
		}
		log.Warn("legacy cron job is not a workflow job; the schedules resource does not carry it and it stays in the legacy file", "id", job.ID, "kind", job.Kind)
	}
	if len(migratable) == 0 {
		log.Info("legacy cron store holds no workflow jobs; nothing to migrate", "path", path, "jobs", len(jobs))
		return
	}
	value, err := json.Marshal(migratable)
	if err != nil {
		log.Error("encode legacy cron jobs; schedules migration skipped and retried on the next start", "path", path, "err", err)
		return
	}
	if _, err := control.Command(ctx, &controlpb.CommandRequest{Kind: controlplane.SchedulesKind, Command: "replace", ValueJson: value, ExpectedVersion: 1, Actor: "system:migration", Source: "legacy-cronstore", RequestId: "import:legacy-schedules"}); err != nil {
		log.Error("legacy cron jobs do not satisfy the schedules resource; migration skipped and retried on the next start", "path", path, "err", err)
		return
	}
	if err := os.Rename(path, path+".migrated"); err != nil {
		log.Error("mark the migrated legacy cron file; migration retried on the next start", "path", path, "err", err)
	}
}

// readLegacyJobs reads the legacy cronstore file and closes its handle,
// reporting whichever failure came first.
func readLegacyJobs(ctx context.Context, path string) ([]cronstore.JobSpec, error) {
	legacy, err := cronstore.Open(path)
	if err != nil {
		return nil, err
	}
	jobs, listErr := legacy.List(ctx)
	closeErr := legacy.Close()
	if listErr != nil {
		return nil, listErr
	}
	return jobs, closeErr
}

// openStateStore opens the single task-store SQLite file exactly once for
// service ownership. It resolves secrets (for the binding cipher) but does
// NOT open gateway chat sessions -- those live in the separate archie-gateway
// process on their own SQLite file and are out of state-store scope.
func (b *boot) openStateStore(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	secrets, err := configuredSecretRegistry(&cfg, log)
	if err != nil {
		log.Error("configure secrets", "err", err)
		return err
	}
	b.secrets = secrets
	bindingCipher, err := bindingCipherFromConfig(cfg, secrets)
	if err != nil {
		log.Error("configure bindings cipher", "err", err)
		return err
	}
	// Claim the database before opening it. This process is the file's owner
	// for as long as it lives, and the claim is what an offline recovery
	// command checks before it rewrites the file: without it, "the State Store
	// is stopped" is a rule the operator is trusted to have followed rather
	// than a fact the command can verify. A second State Store on the same
	// database fails here, which is the single-owner invariant stated instead
	// of merely documented.
	path := taskDBPath(cfg.DBPath)
	ownership, err := store.AcquireOwnership(path)
	if err != nil {
		log.Error("claim state store ownership", "err", err)
		return err
	}
	b.addCleanup(func() {
		if err := ownership.Release(); err != nil {
			log.Error("release state store ownership", "err", err)
		}
	})
	st, err := openProductionTaskStore(ctx, path, store.WithBindingCipher(bindingCipher))
	if err != nil {
		log.Error("open state store", "err", err)
		return err
	}
	b.st = st
	b.addCleanup(func() {
		if err := st.Close(); err != nil {
			log.Error("close state store", "err", err)
		}
	})
	return nil
}

// stateStoreDeps assembles the store surfaces the StateStore service fronts.
// b.st is the narrow storecontract.TaskStore; the wide *store.Store also implements
// the capture/mapping/binding surfaces, so each is asserted here (the same
// pattern the daemon's wireWebStoreSurfaces uses) and a store that lacks one
// degrades that group rather than aborting boot.
func (b *boot) stateStoreDeps(grants *staterpc.TaskGrants) staterpc.Deps {
	deps := staterpc.Deps{Tasks: b.st, Log: b.log, Grants: grants}
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
	// read is served from (docs/prds/ui-service-boundary.md). The reader is
	// the daemon's own registry, built over the same state-directory
	// derivation this process uses for its SQLite file; both resolve from the
	// same configured DBPath, so they agree on where archie keeps its state.
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
	if cs, ok := b.st.(storecontract.CaptureStore); ok {
		deps.Captures = cs
	}
	if ms, ok := b.st.(storecontract.MappingStore); ok {
		deps.Mappings = ms
	}
	if bs, ok := b.st.(storecontract.BindingStore); ok {
		deps.Bindings = bs
	}
	if bd, ok := b.st.(storecontract.BindingDispatcher); ok {
		deps.BindingDispatcher = bd
	}
	if pd, ok := b.st.(storecontract.PlaybookDispatcher); ok {
		deps.PlaybookDispatcher = pd
	}
	if btc, ok := b.st.(storecontract.BindingTaskCreator); ok {
		deps.BindingTaskCreator = btc
	}
	if css, ok := b.st.(storecontract.ConfigSnapshotStore); ok {
		deps.ConfigSnapshots = css
	}
	if as, ok := b.st.(storecontract.ApplyStatusStore); ok {
		deps.ApplyStatus = as
	}
	if cs, ok := b.st.(storecontract.ChannelStatusStore); ok {
		deps.ChannelStatus = cs
	}
	return deps
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
func (b *boot) startStateStoreReadiness(ctx context.Context, readyAddr string) error {
	registry := health.NewRegistry(readiness.NewStoreProbe(b.st))
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

// stateStoreServerOpts applies the transport security boundary (§9): a
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
		return []grpc.ServerOption{keepalive}, true, nil
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
		grpc.ChainUnaryInterceptor(grants.UnaryInterceptor(token)),
		grpc.ChainStreamInterceptor(grants.StreamInterceptor(token)),
	}, false, nil
}
