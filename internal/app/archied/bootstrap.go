// bootstrap.go owns the composition of the daemon: run() is the entry
// point and driver, and each phase of wiring lives in a method on boot.
// The struct exists because the composition root assembles ~30 pieces of
// state that every phase touches; passing them as parameters would make
// each phase function longer than the code it contains.
package archied

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	natsio "github.com/nats-io/nats.go"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	domainembedding "github.com/samcharles93/archie-core/internal/domain/embedding"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	forgewebhook "github.com/samcharles93/archie-core/internal/forge/webhook"
	"github.com/samcharles93/archie-core/internal/gateway"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	infraembedding "github.com/samcharles93/archie-core/internal/infrastructure/embedding"
	"github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	infraMemory "github.com/samcharles93/archie-core/internal/infrastructure/memory"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/sessioncurator"
	"github.com/samcharles93/archie-core/internal/infrastructure/skillcurator"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
	"github.com/samcharles93/archie-core/internal/infrastructure/toolbuilder"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
	"github.com/samcharles93/archie-core/internal/ratelimit"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/minimax"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	builtintoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/builtin"
	"github.com/samcharles93/archie-core/internal/tools/sendfile"
	"github.com/samcharles93/archie-core/internal/tools/webfetch"
	"github.com/samcharles93/archie-core/internal/webui"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

// boot is the mutable wiring state assembled by the run() composition
// phases. Cleanup funcs are registered in creation order and run in
// reverse at shutdown, matching the LIFO ordering of the deferred calls
// they replace.
type boot struct {
	cfg config.Config
	log *slog.Logger

	// stderrLog keeps setupLogging on stderr, never touching cfg.Log.File. An
	// offline command reads the bootstrap config but is not the daemon: creating
	// the deployment's log, appending a line stamped component="daemon", or
	// rotating a log the daemon is writing are side effects a diagnosis must not
	// have, and it needs no durable copy of its own.
	stderrLog bool

	loader            *configuration.Loader
	doc               *configuration.Document
	currentProvenance atomic.Pointer[configuration.Provenance]

	health   *healthSurface
	logFeed  *logging.Feed
	taskLogs *logging.TaskRegistry

	secrets     *secret.Registry
	forgeClient forge.Forge
	token       string

	modules *module.ModuleRegistry

	playbooks *playbook.Store

	st  storecontract.TaskStore
	eda eventCaptureStore
	// pg is the State Store process's process-scoped PostgreSQL pool, opened
	// and migrated by openStateStorePool. It is the one connection the
	// standalone archie-state-store binary holds: do not open a second pool
	// per subsystem. Nil for every other process: the daemon and Gateway reach
	// the State Store over gRPC and hold only the conversation store's pool.
	pg *pgxpool.Pool
	// stateStore is the State Store contract adapter every daemon and gateway
	// store consumer depends on. It is ALWAYS the remote *staterpc.Client
	// dialed to the standalone archie-state-store gRPC service at
	// [services.state].target (the daemon and gateway no longer own archie.db
	// in-process, per docs/prds/state-store-contract.md §12 step 7). It is set
	// by openStateStoreAdapter, which requires [services.state].target to be
	// set. The b.st field remains solely for the standalone archie-state-store
	// binary, which serves the task store from Postgres.
	stateStore storecontract.TaskStore
	// accessChain is the daemon's policy engine, built once from the stored
	// policies (openAccessChain); accessProblems is what the readiness
	// surface reports. Both are nil when no policy store is wired.
	accessChain      access.Authorizer
	accessPrincipals access.PrincipalSource
	accessDenials    access.DenialStore
	accessProblems   []infraaccess.Problem
	// stateStoreGrants issues per-task, scoped State Store credentials for
	// agent containers (daemon.StateStoreGrantIssuer), wrapping the same
	// *staterpc.Client as stateStore. Nil when the State Store adapter isn't
	// the remote gRPC client (never true in production; state_store.go's
	// standalone-only compose leaves no other case).
	stateStoreGrants *staterpc.GrantIssuer
	stateStoreToken  string
	controlPlane     *controlplane.Client
	// controlPlaneRPC is the State Store's control-plane transport, kept so the
	// daemon root can build its workflow-definitions client from it
	// (openDaemonWorkflowDefinitions). The gateway root never builds that client:
	// it resolves no workflow step type.
	controlPlaneRPC controlpb.ControlPlaneServiceClient
	// workflowDefinitions is the one control-plane surface that resolves
	// workflow step types. It is separate from controlPlane because it is the
	// only surface that needs the process's step vocabulary.
	workflowDefinitions *controlplane.WorkflowDefinitionsClient
	// executionSettings is the last workflow execution settings the control
	// plane published. They arrive on a watch rather than in the file
	// document, so reloadConfig re-applies them from here.
	executionSettings atomic.Pointer[workflow.ExecutionSettings]
	// applyStatus reports which control-plane resource version this process
	// is running. Nil until the State Store adapter is open, and nil-safe,
	// so apply points call it without a guard.
	applyStatus *applystatus.Reporter
	// processName is which binary this composition is running as, and is the
	// name apply-status records carry. Set by each entry point, because one
	// boot builds both archied and archie-gateway.
	processName      string
	chatSessionStore gateway.SessionStore
	// chatPool is the conversation store's pool, which the store owns and
	// closes; the Gateway also holds its serve claim on it.
	chatPool *pgxpool.Pool

	catalog       modelcatalog.Snapshot
	catalogModels []string

	bus *events.Bus
	// taskActionsConn is the standalone Gateway process's own NATS connection:
	// it dials the broker directly (the Gateway does not build the
	// consumer/stream client the daemon does) and uses it for task actions.
	// Held so /status can report broker connectivity from the connection this
	// process actually uses instead of dialling a fresh one. Nil in the daemon.
	taskActionsConn *natsio.Conn
	// providerOutcomes records the last-known outcome of every chat-model call
	// this process makes, read back by /status (newStatusHealth). Built before
	// the chat runtime, which carries it into each turn runner.
	providerOutcomes *providerOutcomeRecorder
	// statusHealth is the /status health source this process serves. It
	// carries the broker connection and the chat-model outcomes; see
	// newStatusHealth for the facts it deliberately leaves out.
	statusHealth gateway.HealthSource
	// rateLimiter is the shared per-(channel, sender) inbound budget every
	// chat Router is given. Nil when [chat.rate_limit] is not configured,
	// which leaves rate limiting off.
	rateLimiter *ratelimit.Limiter
	// cfgHolder is the daemon's one configuration Holder. Boot owns it and
	// the daemon reads through it, so a reload swaps one snapshot and every
	// reader sees it: there is no second holder to keep in step.
	cfgHolder *config.Holder
	// chat is the dashboard-shaped chat surface. It survives the UI
	// extraction because the readiness gateway probe and the Telegram task
	// actor both read the same contract.
	chat *webui.ChatService
	// healthRegistry backs /health/detailed on the daemon's own listener.
	healthRegistry *health.Registry
	// lastReload reports the most recent reload outcome for the published
	// configuration projection. Nil until reload wiring installs it.
	lastReload func() config.ReloadStatus

	natsClient *nats.Client
	// reactionClient pulls the ARCHIE_REACTIONS fan-out stream; the daemon
	// drains it each cycle to turn review reactions into remediate runs.
	reactionClient *nats.Client
	// natsURL is the endpoint the daemon's own client connected with at
	// startup. For external mode it is cfg.NATS.URL; for embedded mode it is
	// the embedded server's ClientURL(). Recorded so Daemon.ConnectedNATS
	// carries the real endpoint rather than an empty config URL.
	natsURL string
	// natsToken is the credential the daemon's client connected with at
	// startup. Embedded mode generates it; external mode resolves it once.
	natsToken string

	containerPool *container.Pool
	// kitLauncher starts Kit profile tasks; nil when the egress path cannot
	// be built on this host.
	kitLauncher  daemon.KitLauncher
	storeBackend storage.Backend

	// llm is the chat runtime's provider runtime, held in a pointer the chat
	// surfaces read through chatLLM at each use. A live provider-settings or
	// model-role-assignments update swaps it wholesale: ai-sdk's Runtime
	// caches the provider instances it built, so a changed provider set needs
	// a new Runtime rather than a mutated one.
	llm atomic.Pointer[runtime.Runtime]
	// runtimeVersions records the version of every control-plane kind boot's
	// layering applied. Set once at boot before the runtime-resource watches
	// start, which read it as their resume points.
	runtimeVersions map[string]int64

	// embeddings is nil unless models["embedding"] and a working provider
	// credential are both configured -- see setupLLMAndChat's "Embeddings
	// capability" section. Consumers must treat a nil embeddings the same
	// as domainembedding.ErrUnavailable: skip, never fail startup or an
	// unrelated call.
	embeddings domainembedding.Client

	toolReg        *tools.Registry
	chatModels     *chatModelManager
	personas       *gateway.PersonaRegistry
	chatTasks      gateway.TaskCreator
	chatController *gateway.StoreTaskController
	// chatPRReviewer is the operator-triggered PR review capability, built
	// once and shared by every channel so a review in flight on one channel
	// is deduplicated against the same request on another (single-flight).
	chatPRReviewer      gateway.ChatPRReviewer
	defaultChatIdentity string
	updateService       *releaseupdate.Service

	startGateways    []func()
	capabilityHost   *plugin.Host
	trees            *worktree.Manager
	worktreeGrants   *worktreerpc.Grants
	identityRunners  []*daemon.IdentityRunner
	memEngines       *domainmemory.Registry
	curatorRegistry  *curator.Registry
	curatorRuntime   *curator.Runtime
	guardrails       *tools.GuardrailEngine
	providerRegistry *toolprovider.Registry
	d                *daemon.Daemon
	// schedulingEngine is the cron/scheduling ticker engine (setupScheduling).
	// Nil when no chat task creator is configured, in which case startServices
	// leaves it unstarted rather than running with no reachable job kind.
	schedulingEngine *scheduling.Engine

	// kindWorkflows/labelWorkflows are the resolved kind/label -> workflow
	// routing bindings loaded by loadWorkflowRouting. They are handed to the
	// daemon so runViaAgent can carry them in taskrun.Request: workflow.Route
	// runs in the archie-agent process, which never sees the daemon's
	// package-level workflow state.
	kindWorkflows  workflow.KindWorkflows
	labelWorkflows workflow.LabelWorkflows

	// agentStatus records the most recent version/install-type an
	// archie-agent worker reported about itself. Allocated up front, before
	// any gateway goroutine (Telegram, webui) that might read it via
	// RunningVersions starts, so those closures never see a nil pointer or
	// race its assignment -- only its own internal mutex guards concurrent
	// Observe/Snapshot calls once tasks start completing.
	agentStatus *daemon.AgentStatus

	// shutdown cancels the daemon's root context, the exact path a SIGTERM
	// takes to graceful shutdown. The drain monitor invokes it when it honours
	// an external drain request, so a marker-driven drain reuses the operator
	// signal path rather than inventing a new one.
	shutdown context.CancelFunc

	cleanups []func()
}

func newBootstrap() *boot {
	return &boot{log: slog.New(slog.NewJSONHandler(os.Stderr, nil)), agentStatus: &daemon.AgentStatus{}}
}

// chatLLM is the provider runtime the chat surfaces read at each use: a turn
// resolves it when it starts, so a runtime swapped by a live model-settings
// update is the one the turn after it runs on.
func (b *boot) chatLLM() *runtime.Runtime { return b.llm.Load() }

// setLLM replaces the chat runtime. A nil runtime is a legal state: a
// deployment whose every provider credential was disabled by a live update
// leaves chat turns refused until another update restores one.
func (b *boot) setLLM(rt *runtime.Runtime) { b.llm.Store(rt) }

func (b *boot) addCleanup(fn func()) {
	b.cleanups = append(b.cleanups, fn)
}

func (b *boot) cleanup() {
	for _, v := range slices.Backward(b.cleanups) {
		v()
	}
}

// loadConfig resolves the file config. A Resolve failure is reported on
// stderr because the file log destination is itself configuration that has
// not been read yet.
func (b *boot) loadConfig(ctx context.Context, cfgPath, overlayPath string) error {
	loader := configuration.New(b.log)
	b.loader = loader
	doc, err := loader.Resolve(cfgPath, overlayPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	b.doc = doc
	// A stray/misspelled key parses and validates cleanly (unknown TOML
	// keys are otherwise silently discarded), so it must be visible
	// somewhere rather than just quietly doing nothing -- see
	// docs/architecture/configuration.md's startup policy: an invalid
	// config is fatal, but a typo like this is not that, only a warning.
	if len(doc.UnknownKeys) > 0 {
		b.log.Warn("config file has unrecognised keys; check for typos", "keys", doc.UnknownKeys)
	}
	b.cfg = b.doc.Config
	b.cfgHolder = config.NewHolder(b.cfg)
	b.currentProvenance.Store(&b.doc.Provenance)
	return b.setupLogging()
}

// setupLogging re-creates the logger now the config is known. Everything
// before this point logs to stderr only, which is unavoidable: the log
// destination is itself configuration. A file that cannot be opened is
// reported and the daemon continues on stderr -- losing the durable copy
// must not take the daemon down with it.
func (b *boot) setupLogging() error {
	cfg := b.cfg
	if b.stderrLog {
		// The stderr logger newBootstrap installed is the whole destination.
		// Nothing below this line is set up: the feed, the task-log registry and
		// the rotating file all belong to a running daemon.
		return nil
	}
	logFeed := logging.NewFeed(1000)
	b.logFeed = logFeed
	// Task logs live in the state directory rather than under cfg.Log.File's
	// directory: cfg.Log.File is optional (file logging can be off), while
	// state_dir always resolves.
	taskLogs := logging.NewTaskRegistry(filepath.Join(cfg.StateDir, "logs", "tasks"), logFeed, logging.TaskSinkOptions{})
	b.taskLogs = taskLogs
	fileLog, logCloser, logErr := logging.New(logging.Options{
		File:      cfg.Log.File,
		MaxSizeMB: cfg.Log.MaxSizeMB,
		Keep:      cfg.Log.Keep,
		Level:     cfg.Log.Level,
		Stderr:    !cfg.Log.Quiet,
		Feed:      logFeed,
	})
	// All daemon diagnostics carry an explicit component so the dashboard's
	// component filter does not have to infer ownership from message text.
	b.log = fileLog.With("component", "daemon")
	b.addCleanup(func() { _ = logCloser.Close() })
	if logErr != nil {
		b.log.Error("file logging disabled", "err", logErr)
	} else if cfg.Log.File != "" {
		b.log.Info("logging to file", "path", cfg.Log.File)
	}
	return nil
}

// openStores wires the secret registry, forge client and both stores.
func (b *boot) openStores(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	secrets, err := configuredSecretRegistry(&cfg, log)
	if err != nil {
		log.Error("configure secrets", "err", err)
		return err
	}
	b.secrets = secrets
	if err := resolveProviderSecrets(&b.cfg, secrets, log); err != nil {
		log.Error("resolve provider secrets", "err", err)
		return err
	}
	b.forgeClient, b.token = resolveForge(cfg.Forge, secrets, log)

	// The daemon and gateway do not serve the State Store's tables: the
	// standalone archie-state-store process does, and both consumers dial its gRPC State Store contract. openStateStoreAdapter
	// (called by Run and RunGateway after openStores) resolves b.stateStore as
	// the remote *staterpc.Client. There is no local store to open here
	// (docs/prds/state-store-contract.md §12 step 7, no dual-store ownership).
	return b.openChatSessions(ctx)
}

// openStateStoreAdapter resolves the State Store contract adapter as the
// remote *staterpc.Client dialed to [services.state].target, so every daemon
// and gateway store consumer uses one endpoint -- the standalone
// archie-state-store service. It requires [services.state].target to be set:
// after .4.6 the daemon and gateway no longer own archie.db in-process, so the
// local default of earlier phases is removed (no dual-store ownership;
// docs/prds/state-store-contract.md §12 step 7). The token the daemon also
// injects into agent containers (STATE_STORE_TOKEN) is the same one this
// client authenticates with, resolved from [services.state].target_token or
// the STATE_STORE_TOKEN secret (§10). It runs after openStores has resolved
// b.secrets, and before setupObservability / buildDaemon wire consumers.
func (b *boot) openStateStoreAdapter() error {
	target := strings.TrimSpace(b.cfg.Services.Get(config.ServiceNameState).Target)
	if target == "" {
		return fmt.Errorf("services.state.target is required: archied/archie-gateway no longer own archie.db; the standalone archie-state-store process owns it (docs/prds/state-store-contract.md §12 step 7)")
	}
	client, cleanup, err := composeStateStoreClient(b.cfg.Services, b.secrets)
	if err != nil {
		b.log.Error("state store adapter", "err", err)
		return err
	}
	b.stateStore = client
	// The daemon reads workflow definitions through the workflow-definitions
	// client, built by the daemon root (openDaemonWorkflowDefinitions) on this
	// process's step vocabulary, so the daemon resolves the same vocabulary the
	// State Store server validates against. It is not built here because the
	// gateway root shares this adapter and resolves no step type. That agreement
	// holds within one build: the State Store is a separate binary, and a skewed
	// deploy is only fixed by a matching deploy.
	b.controlPlaneRPC = client.ControlPlane()
	b.controlPlane = controlplane.NewRPCClient(b.controlPlaneRPC)
	b.applyStatus = applystatus.New(b.processName, client, b.log)
	b.stateStoreGrants = &staterpc.GrantIssuer{Client: client}
	b.stateStoreToken = b.cfg.Services.ResolvedToken(config.ServiceNameState, b.secrets.Getenv)
	b.addCleanup(cleanup)
	return nil
}

// openAccessChain builds the policy chain the daemon dispatches through
// (docs/prds/orgs-and-access.md, "Where it lives"): the stored policies over
// the wire, compiled once at boot. An invalid instance policy fails the boot;
// an invalid org, workspace or object policy is retained as a health problem
// and its level denies everything.
func (b *boot) openAccessChain(ctx context.Context) error {
	source, ok := b.stateStore.(access.PolicySource)
	if !ok {
		return nil // no policy store wired: the chain is not built
	}
	stored, err := source.Policies(ctx)
	if err != nil {
		return fmt.Errorf("load stored policies: %w", err)
	}
	engine, err := infraaccess.New(stored)
	if err != nil {
		return fmt.Errorf("validate stored policies: %w", err)
	}
	b.accessChain = engine
	b.accessProblems = engine.Problems()
	for _, problem := range b.accessProblems {
		b.log.Error("stored access policy is invalid and denies its level",
			"policy", problem.Policy.ID, "level", problem.Policy.Level, "err", problem.Err)
	}
	return nil
}

// openChatSessions opens the conversation store on its own pool from
// database_url. The daemon reads it (session curator) and the Gateway serves
// it; only the Gateway claims serve ownership (claimGatewayOwnership).
func (b *boot) openChatSessions(ctx context.Context) error {
	pool, err := openServicePool(ctx, b.cfg.DatabaseURL, "the conversation store")
	if err != nil {
		return fmt.Errorf("open conversation store: %w", err)
	}
	chatSessionStore := gateway.NewPostgresSessionStore(pool)
	b.chatPool = pool
	b.chatSessionStore = chatSessionStore
	b.addCleanup(func() {
		if err := chatSessionStore.Close(); err != nil {
			b.log.Error("close conversation store", "err", err)
		}
	})
	return nil
}

// claimGatewayOwnership holds the Gateway's serve claim for the process's
// life, so a second Gateway (whose turn recovery would fail this one's
// in-flight turns) refuses to start. It runs after openChatSessions, so its
// cleanup releases the claim before the conversation store closes the pool.
func (b *boot) claimGatewayOwnership(ctx context.Context) error {
	ownership, err := postgres.AcquireOwnership(ctx, b.chatPool, postgres.OwnerGateway)
	if err != nil {
		return fmt.Errorf("claim gateway ownership: %w", err)
	}
	releaseCtx := context.WithoutCancel(ctx)
	b.addCleanup(func() {
		if err := ownership.Release(releaseCtx); err != nil {
			b.log.Error("release gateway ownership", "err", err)
		}
	})
	return nil
}

// handleRequeue replays a parked/waiting task by id, then signals an
// early success exit unless -once keeps the daemon running.
func (b *boot) handleRequeue(ctx context.Context, requeue int64, once bool) (bool, error) {
	if requeue <= 0 {
		return false, nil
	}
	if err := manualRequeueTask(ctx, b.stateStore, requeue); err != nil {
		b.log.Error("requeue failed", "task", requeue, "err", err)
		return false, err
	}
	b.log.Info("task requeued", "task", requeue)
	return !once, nil
}

func (b *boot) loadCatalog(ctx context.Context, cfgPath string) {
	catalog, err := modelcatalog.Load(ctx, modelcatalog.Options{
		CachePath:  filepath.Join(filepath.Dir(cfgPath), "models.json"),
		Getenv:     b.secrets.Getenv,
		Configured: b.cfg.Providers,
	})
	b.catalog = catalog
	if err != nil {
		b.log.Warn("model catalog unavailable; using configured providers and models", "err", err)
		return
	}
	b.catalogModels = applyModelCatalog(&b.cfg, catalog)
	b.log.Info("model catalog loaded", "providers", len(catalog.Providers), "models", len(b.catalogModels))
}

// setupObservability builds the event bus and dashboard server. Every event is persisted (stamped with its row id) and
// then fanned out to live dashboard connections.
func (b *boot) setupObservability(ctx context.Context) {
	cfg, log := b.cfg, b.log
	bus := events.NewBus()
	b.bus = bus
	b.addCleanup(func() { bus.Close() })
	// The watchdog leaves its verdict in a file on this host, so the daemon
	// reads it and publishes the outcome as an event; the dashboard renders
	// what it receives, wherever it runs (archie-core-8cda.5.4).
	b.startUpdateRelay(ctx, updateReportPath(cfg.WorkDir, "webui"))
	sink := bus.Subscribe(256)
	go persistEvents(ctx, sink, b.stateStore, log)
}

// reactionStreamMaxAge bounds how long a reaction survives in the fan-out
// stream before JetStream discards it. Reactions are producer-only wake
// events (docs/prds/event-sources-and-reactions.md): a dropped reaction only
// delays work until the next poll, never loses it, because the authoritative
// state is re-read at pass time. One day therefore tolerates a consumer that
// is down or lagging for a full maintenance window without letting
// acknowledged reactions accumulate without bound.
const reactionStreamMaxAge = 24 * time.Hour

// connectNATS opens the NATS client. External mode dials cfg.NATS.URL;
// embedded mode starts an in-process nats-server and dials it, so single-
// process deployments get task distribution and reaction delivery without a
// separate server. Broker deployment never changes the worker executor.
func (b *boot) connectNATS(ctx context.Context) error { //nolint:nestif // embedded broker discovery and startup require explicit fallback branches
	cfg, log := b.cfg, b.log
	url := cfg.NATS.URL
	var natsToken string
	if cfg.NATS.Mode == config.NATSModeExternal { //nolint:nestif // embedded broker discovery and startup require explicit fallback branches
		if url == "" {
			return fmt.Errorf("nats.url is required when nats.mode is external")
		}
		var err error
		natsToken, err = configuredNATSToken(cfg.NATS, os.Getenv)
		if err != nil {
			log.Error("nats credentials", "err", err)
			return err
		}
	} else {
		endpoint, readErr := readEmbeddedNATSEndpoint(cfg.StateDir)
		if readErr == nil {
			url, natsToken = endpoint.URL, endpoint.Token
			if probe, err := nats.Connect(ctx, nats.Config{URL: url, Token: natsToken, Subjects: []string{workintake.SubjectTaskWildcard}, FilterSubject: workintake.SubjectTaskWildcard}, log); err == nil {
				probe.Close()
				// The endpoint is live; the connection is recreated below with the
				// same subject configuration and becomes the daemon's owner.
				url, natsToken = endpoint.URL, endpoint.Token
			} else {
				url, natsToken, err = b.startEmbeddedNATS(ctx)
				if err != nil {
					return err
				}
			}
		} else {
			var err error
			url, natsToken, err = b.startEmbeddedNATS(ctx)
			if err != nil {
				return err
			}
		}
	}

	// Composition owns the subject list: the bus must not know which
	// subjects belong to which domain.
	natsClient, err := nats.Connect(ctx, nats.Config{
		URL:           url,
		Token:         natsToken,
		Subjects:      []string{workintake.SubjectTaskWildcard},
		FilterSubject: workintake.SubjectTaskWildcard,
	}, log)
	if err != nil {
		log.Error("nats connect failed", "err", err)
		return err
	}

	// Reactions are producer-only fan-out events: every interested consumer
	// must see each one, so they get their own stream under LimitsPolicy
	// rather than the work-queue policy ARCHIE_TASKS uses (which lets a
	// second consumer on an overlapping filter silently receive nothing).
	// LimitsPolicy retains every message until a limit is reached, so without
	// a finite limit acknowledged reactions would accumulate forever; the
	// MaxAge cap bounds that by time.
	reactionMaxAge := reactionStreamMaxAge
	reactionClient, err := nats.Connect(ctx, nats.Config{
		URL:           url,
		Token:         natsToken,
		StreamName:    nats.DefaultReactionStreamName,
		Subjects:      []string{workintake.SubjectReactionWildcard},
		FilterSubject: workintake.SubjectReactionWildcard,
		Retention:     nats.FanOutRetention(),
		MaxAge:        &reactionMaxAge,
	}, log)
	if err != nil {
		log.Error("nats reaction stream connect failed", "err", err)
		natsClient.Close()
		return err
	}

	b.natsClient = natsClient
	b.natsURL = url
	b.natsToken = natsToken
	b.addCleanup(func() { natsClient.Close() })
	// The reaction client is kept only so its connection stays open for the
	// daemon's lifetime and is closed at shutdown; the stream it provisions
	// is the deliverable. A future producer (bead archie-core-8li9.3) will
	// need its own publisher surface, wired when that step lands.
	b.addCleanup(func() { reactionClient.Close() })
	b.reactionClient = reactionClient
	log.Info("nats connected", "url", url, "task_stream", nats.DefaultStreamName, "reaction_stream", nats.DefaultReactionStreamName)
	return nil
}

// startEmbeddedNATS starts the in-process nats-server and returns the client
// URL and token to dial it with. Its shutdown is registered BEFORE the caller
// registers the client close, so the client closes first (cleanups run LIFO).
func (b *boot) startEmbeddedNATS(ctx context.Context) (string, string, error) {
	cfg, log := b.cfg, b.log
	// An empty state_dir would put the store and its endpoint file in the
	// process's working directory. Defaults always set it, so empty means a
	// caller skipped them.
	if cfg.StateDir == "" {
		return "", "", errors.New("embedded nats: state_dir is required")
	}
	host := ""
	if b.containerPool != nil {
		host = b.containerPool.HostGateway()
	}
	srv, err := nats.StartEmbedded(ctx, nats.EmbeddedOptions{
		Host:     host,
		StoreDir: filepath.Join(cfg.StateDir, "nats"),
	}, log)
	if err != nil {
		log.Error("embedded nats start failed", "err", err)
		return "", "", err
	}
	b.addCleanup(func() { srv.Shutdown() })
	log.Info("embedded nats started", "url", srv.ClientURL())
	if err := writeEmbeddedNATSEndpoint(cfg.StateDir, srv.ClientURL(), srv.Token()); err != nil {
		srv.Shutdown()
		return "", "", err
	}
	return srv.ClientURL(), srv.Token(), nil
}

// setupContainers builds the managed autonomous-worker pool. Failure degrades
// rather than aborting because the native interactive agent and dashboard do
// not require Docker; repository tasks park instead of falling back to a host
// model loop.
func (b *boot) setupContainers(ctx context.Context) func() {
	containerPool, storeBackend, closeDocker := startContainers(ctx, b.cfg, b.log)
	b.containerPool = containerPool
	b.storeBackend = storeBackend
	return closeDocker
}

// setupEmbeddings builds the optional embedding capability. A client is
// wired only when models["embedding"] names a provider/model and that
// provider's credential resolves -- the same credential-missing-degrades-
// not-fatal rule registerMinimaxTool follows for generate_video. No
// warning when the role was simply never configured; a warning when it was
// configured but couldn't be made to work, so a broken setup doesn't go
// unnoticed the way AGENTS.md already warns an unnoticed optional-provider
// degradation can.
func (b *boot) setupEmbeddings(cfg config.Config, log *slog.Logger) {
	if client, ok := infraembedding.New(cfg, infraembedding.Options{}); ok {
		b.embeddings = client
		log.Info("embedding capability enabled", "role", infraembedding.Role)
	} else if cfg.Models[infraembedding.Role] != "" {
		log.Warn("embedding capability configured but unavailable; capability disabled", "role", infraembedding.Role)
	}
}

// setupLLMAndChat wires the runtime, tool registry, model management,
// personas and the dashboard's chat service.
func (b *boot) setupLLMAndChat(ctx context.Context) error {
	cfg, log := b.cfg, b.log

	// Telegram/email keep their own in-process routers rather than going
	// through ChatContract: their streaming responder is a callback, and
	// ChatContract's wire-safe interface can't carry one.
	if err := b.setupChatRuntime(ctx, cfg); err != nil {
		return err
	}

	b.setupEmbeddings(cfg, log)
	contract, cleanup, err := composeChatContract(cfg.Services, b.secrets)
	if err != nil {
		return err
	}
	b.addCleanup(cleanup)
	// Channel routers execute turns locally and need the conversation store's TurnLedger.
	// The remote contract serves web chat; it must not replace their store.
	b.chat = &webui.ChatService{Contract: contract, Updates: b.updateService}
	b.setupReadinessProbes()
	return nil
}

// rateLimiterEvictInterval is how often an active Limiter sweeps entries
// whose hits have all aged out of the window, per internal/ratelimit's own
// documented ticker contract.
const rateLimiterEvictInterval = time.Minute

// startRateLimiter constructs b.rateLimiter from cfg when configured, and
// drives its documented EvictStale ticker for the life of ctx. Leaves
// b.rateLimiter nil (rate limiting off) when cfg is not enabled. Only the
// Gateway process calls it: it owns the sole Router, so it is the one place
// an inbound budget can be applied to every channel's turns.
func (b *boot) startRateLimiter(ctx context.Context, cfg config.RateLimitConfig) {
	if !cfg.Enabled() {
		return
	}
	b.rateLimiter = ratelimit.New(cfg.Window, cfg.MaxRequests)
	limiter := b.rateLimiter
	go func() {
		ticker := time.NewTicker(rateLimiterEvictInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				limiter.EvictStale()
			}
		}
	}()
}

// loadWorkflows loads routing inputs. Executable definitions are supplied by
// the State Store control plane and pinned before each dispatch.
func (b *boot) loadWorkflows(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	if err := b.loadWorkflowRouting(cfg, log); err != nil {
		return err
	}
	if err := b.loadModules(cfg, log); err != nil {
		return err
	}
	if err := b.loadEDAPlaybooks(cfg, log); err != nil {
		return err
	}

	log.Info("workflow step registry built", "shipped_workflows", len(workflow.ShippedDefinitions().Definitions))
	// The dashboard is served by the archie-ui process from the cutover
	// change (archie-core-8cda.5.4, PRD gate 7): the daemon runs no webui
	// listener, and [web].listen is the archie-ui process's bind address.
	// The daemon still renders the configuration snapshot via the
	// standalone webui.BuildConfigView, not through a Server.
	return nil
}

// loadWorkflowRouting loads the kind/label -> workflow-name bindings from
// the single-file fields and the playbook directories, merges them, and
// installs the result via workflow.SetKindWorkflows/SetLabelWorkflows.
// Split out of loadWorkflows (t2db.16) to keep the top-level function's
// complexity within the lint gate -- pure extraction, same log messages
// and error-return order as before.
func (b *boot) loadWorkflowRouting(cfg config.Config, log *slog.Logger) error {
	kindWorkflows, err := workflow.LoadKindWorkflowsYAML(cfg.WorkflowRoutingFile)
	if err != nil {
		log.Error("workflow routing file load failed", "path", cfg.WorkflowRoutingFile, "err", err)
		return err
	}

	labelWorkflows, err := workflow.LoadLabelWorkflowsYAML(cfg.WorkflowLabelsFile)
	if err != nil {
		log.Error("workflow labels file load failed", "path", cfg.WorkflowLabelsFile, "err", err)
		return err
	}

	dirKindWorkflows, dirLabelWorkflows, err := workflow.LoadPlaybookDirs(cfg.PlaybookDirs)
	if err != nil {
		log.Error("playbook dirs load failed", "dirs", cfg.PlaybookDirs, "err", err)
		return err
	}
	// The directory is an additional input to the single-file fields, not a
	// replacement (t2db.11). A key bound by both a single-file field and the
	// directory is the same collision the loader enforces inside a directory:
	// reported, never silently arbitrated by source precedence.
	kindWorkflows, err = workflow.MergeKindWorkflows(kindWorkflows, dirKindWorkflows)
	if err != nil {
		log.Error("workflow binding collision between routing file and playbook dir", "err", err)
		return err
	}
	labelWorkflows, err = workflow.MergeLabelWorkflows(labelWorkflows, dirLabelWorkflows)
	if err != nil {
		log.Error("workflow binding collision between labels file and playbook dir", "err", err)
		return err
	}

	workflow.SetKindWorkflows(kindWorkflows)
	workflow.SetLabelWorkflows(labelWorkflows)
	b.kindWorkflows = kindWorkflows
	b.labelWorkflows = labelWorkflows
	return nil
}

// loadModules builds the daemon's Module registry (EDA playbook Module
// position, t2db.13): operator-trusted, in-process, Yaegi-interpreted. A
// broken module is a startup failure -- the daemon does not start with a
// partial module set, matching the routing-file load pattern (not the
// degrade-and-skip plugin pattern). Kinds whose file is not present in the
// directory are simply not loaded. Split out of loadWorkflows (t2db.16);
// pure extraction, same log messages and error-return order as before.
func (b *boot) loadModules(cfg config.Config, log *slog.Logger) error {
	b.modules = module.New()
	if cfg.ModuleDir == "" {
		return nil
	}
	for _, kind := range module.Kinds() {
		path := filepath.Join(cfg.ModuleDir, kind+".go")
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // kind not installed in this module dir
			}
			log.Error("module stat failed", "kind", kind, "path", path, "err", err)
			return err
		}
		if err := b.modules.Register(kind, cfg.ModuleDir); err != nil {
			log.Error("module load failed", "kind", kind, "dir", cfg.ModuleDir, "err", err)
			return err
		}
	}
	log.Info("module registry built", "dir", cfg.ModuleDir, "kinds", b.modules.Len())
	return nil
}

// loadEDAPlaybooks loads the EDA playbook documents (t2db.15): trigger +
// workflow-kind actions and module-kind action playbooks with CEL when
// conditions and args values. Loaded at startup with the same reject-at-load
// rule -- any malformed playbook, mixed/unsupported action shape, unknown
// module kind, when compile failure, or args key failure aborts startup,
// matching the routing-file load pattern (not degrade-and-skip). A nonexistent
// dir is an empty store. Action playbooks run through the daemon once
// buildDaemon wires the dispatch ledger (docs/prds/action-playbook-run.md).
func (b *boot) loadEDAPlaybooks(cfg config.Config, log *slog.Logger) error {
	var err error
	b.playbooks, err = playbook.Load(cfg.EDAPlaybookDir, b.modules)
	if err != nil {
		log.Error("eda playbook load failed", "dir", cfg.EDAPlaybookDir, "err", err)
		return err
	}
	log.Info("eda playbooks loaded", "dir", cfg.EDAPlaybookDir, "playbooks", len(b.playbooks.Playbooks))
	return nil
}

// loadPlugins loads daemon plugins from the configured plugin directory
// (Layer 2). Failed plugins are skipped  --  the daemon starts with the
// remaining set.
func (b *boot) loadPlugins() error {
	cfg, log := b.cfg, b.log
	b.capabilityHost = plugin.NewHost()
	if cfg.PluginDir == "" {
		return nil
	}
	plugins, err := plugin.LoadDir(cfg.PluginDir, pluginextract.Symbols)
	if err != nil {
		log.Error("plugin load failed", "dir", cfg.PluginDir, "err", err)
		return err
	}
	for _, p := range plugins {
		name, version := safePluginInfo(p)
		module, err := plugin.AdaptLegacy(p)
		if err != nil {
			log.Warn("daemon plugin capability registration skipped", "name", name, "version", version, "err", err)
			continue
		}
		if err := b.capabilityHost.Register(module); err != nil {
			log.Warn("daemon plugin capability registration skipped", "name", name, "version", version, "err", err)
			continue
		}
		log.Info("daemon plugin loaded", "name", name, "version", version)
	}
	return nil
}

// buildWorktreeManager composes the process-wide worktree manager from the
// resolved primary forge credential and the loaded config. Both process roots
// own one: the daemon works tasks in it, and the Gateway materialises a pull
// request head in it when an operator asks for a review (boot.prReviewer
// refuses to build a reviewer without one, so a Gateway that skips this step
// advertises no review_pr). It reads only, so it has nothing to report.
//
// Identity managers are not built here: buildTreesAndIdentities builds one per
// identity, each in its own WorkDir, because the Gateway must not own daemon
// identity runners.
func (b *boot) buildWorktreeManager() {
	cfg := b.cfg
	b.trees = &worktree.Manager{
		WorkDir:  cfg.WorkDir,
		Token:    b.token,
		BotUser:  cfg.BotUser,
		BotEmail: cfg.BotEmail,
		BaseURL:  cfg.Forge.Host,
	}
}

// buildTreesAndIdentities composes the worktree manager and the
// multi-identity runners. Each configured identity gets its own forge
// client (its own token, possibly its own forge type/host) and its own
// worktree manager (a distinct WorkDir so concurrent identities never
// collide on the same clone). When cfg.Identities is empty,
// Daemon.Identities stays nil and Run() takes the single-identity path
// unchanged.
func (b *boot) buildTreesAndIdentities(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	b.buildWorktreeManager()

	for _, idCfg := range cfg.Identities {
		// Same reasoning as the primary forge: one identity whose credential is
		// missing must not deny every other identity, and every other
		// subsystem, the ability to run.
		idForge, idToken := resolveForge(idCfg.Forge, b.secrets, log.With("identity", idCfg.Name))
		idTrees := &worktree.Manager{
			WorkDir:  filepath.Join(cfg.WorkDir, "identity-"+idCfg.Name),
			Token:    idToken,
			BotUser:  idCfg.BotUser,
			BotEmail: idCfg.BotEmail,
			BaseURL:  idCfg.Forge.Host,
		}
		runner, err := daemon.NewIdentityRunner(ctx, idCfg, idForge, idTrees, log)
		if err != nil {
			log.Error("identity construction failed", "identity", idCfg.Name, "err", err)
			return err
		}
		b.identityRunners = append(b.identityRunners, runner)
		log.Info("identity configured", "identity", idCfg.Name, "bot_user", idCfg.BotUser, "repos", len(idCfg.Repos))
	}
	return nil
}

// registerNATSRPC lets archie-agent containers (which hold no DB
// connection, forge token, or push credential) proxy Store/Forge/
// worktree operations back to archied over NATS.
func (b *boot) registerNATSRPC() error {
	log := b.log
	if b.natsClient == nil {
		return nil
	}
	coreConn, err := b.natsClient.CoreConn()
	if err != nil {
		log.Error("nats connection unavailable for task RPC", "err", err)
		return err
	}
	b.worktreeGrants = worktreerpc.NewGrants()
	unsubscribe, err := registerTaskRPCServers(coreConn, b.forgeClient, b.trees, b.identityRunners, b.worktreeGrants, log)
	if err != nil {
		log.Error("task RPC server registration failed", "err", err)
		return err
	}
	b.addCleanup(unsubscribe)

	unsubscribeSystemLogs, err := subscribeSystemLogs(coreConn, b.taskLogs, log)
	if err != nil {
		log.Error("system log subscribe failed", "err", err)
		return err
	}
	b.addCleanup(unsubscribeSystemLogs)

	unsubscribeAgentEvents, err := subscribeAgentEvents(coreConn, b.bus, log)
	if err != nil {
		log.Error("agent event subscribe failed", "err", err)
		return err
	}
	b.addCleanup(unsubscribeAgentEvents)

	// The standalone Gateway forwards operator task actions to the daemon
	// over NATS. The daemon owns execution cancellation, retry policy,
	// forge closure and event persistence; the responder applies the same
	// taskactions.Service the dashboard uses.
	unsubscribeTaskActions, err := taskactions.Register(coreConn, b.taskActions(), log)
	if err != nil {
		log.Error("gateway task action register failed", "err", err)
		return err
	}
	b.addCleanup(unsubscribeTaskActions)
	return nil
}

// setupMemoryEngine wires the domain/memory engine family
// (archie-core-1786637499161-356-e424e40d), the only memory engine per
// docs/prds/memory-engine-unification.md. cfg.Memory.Engine is validated at
// config load (configuration.validateMemory) against the same set this
// switch covers, so an unrecognised value cannot reach here -- this only
// guards against the two lists drifting apart.
//
// Rooted at workDir/memory-engine.
func (b *boot) setupMemoryEngine() error {
	cfg, log := b.cfg, b.log
	// The logger is what carries an engine's scanner warning: warn allows
	// the write, so the log line is the whole audit trail for it.
	registry := domainmemory.NewRegistry(domainmemory.Registrar{Log: log})

	switch cfg.Memory.Engine {
	case infraMemory.EngineName, "":
		root := filepath.Join(cfg.WorkDir, "memory-engine")
		if err := registry.Register(infraMemory.NewBuiltinEngine(root, 0)); err != nil {
			return fmt.Errorf("register memory engine %q: %w", infraMemory.EngineName, err)
		}
	default:
		return fmt.Errorf("memory.engine %q has no registered implementation", cfg.Memory.Engine)
	}

	// Not b's own ctx: matches setupMemory's identical choice just above,
	// and Start here is a synchronous startup step, not a subscription
	// that needs to live and later be cancelled with the daemon's context
	// the way setupCurators' WakeOnPrimaryInput does.
	if err := registry.Start(context.Background()); err != nil {
		return fmt.Errorf("start memory engine registry: %w", err)
	}
	b.memEngines = registry
	b.addCleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := registry.Stop(shutdownCtx); err != nil {
			log.Error("memory engine registry shutdown", "err", err)
		}
	})
	log.Info("memory engine started", "engine", cfg.Memory.Engine)
	return nil
}

// activeMemoryEngine resolves the engine setupMemoryEngine registered under
// cfg.Memory.Engine. Both memoryStore and memoryWriter narrow this same
// engine to the read or write surface their caller needs; ok is false when
// the registry was never set up or the configured engine is not registered.
func (b *boot) activeMemoryEngine() (domainmemory.MemoryEngine, bool) {
	if b.memEngines == nil {
		return nil, false
	}
	name := b.cfg.Memory.Engine
	if name == "" {
		name = infraMemory.EngineName
	}
	return b.memEngines.Get(name)
}

// memoryStore resolves the active memory engine as the narrow read surface
// a chat turn runner needs. Nil when activeMemoryEngine has none -- the
// turn runner already treats that as "no memory block"
// (gateway.renderMemory), so a chat turn degrades instead of failing.
func (b *boot) memoryStore() gateway.MemoryStore {
	engine, ok := b.activeMemoryEngine()
	if !ok {
		return nil
	}
	return engine
}

// memoryWriter resolves the active memory engine as the narrow write
// surface the per-turn memory tool needs (docs/prds/
// memory-engine-unification.md §5). Nil when activeMemoryEngine has none --
// MemoryTools already treats that as "no memory tools", so a chat turn
// simply has no memory_create/update/delete/list tools rather than failing.
func (b *boot) memoryWriter() gateway.MemoryWriteStore {
	engine, ok := b.activeMemoryEngine()
	if !ok {
		return nil
	}
	return engine
}

// setupCurators wires the curator engine family. The registry owns
// curator registration, lifecycle and shutdown ordering. The runtime
// loop (archie-core-89x) and the reference curators (archie-core-i7i,
// gs8) are wired by their issues; until then the registry is empty and
// Stop is a no-op. The event sink rides the in-process bus, whose
// bounded dropping per-subscriber buffers guarantee curator activity can
// never backpressure the daemon or a chat turn.
func (b *boot) setupCurators(ctx context.Context) {
	log := b.log.With("component", "curator")
	// Skills root matches loadWorkflows' own resolution of skillsBase --
	// the skill curator maintains the same local skills this daemon
	// already treats as authoritative, not the full multi-root catalog a
	// chat turn reads. See docs/prds/skill-curator.md.
	skillsRoot := b.cfg.SkillsDir
	if skillsRoot == "" {
		skillsRoot = b.cfg.WorkDir
	}
	b.curatorRegistry = curator.NewRegistry(curator.Registrar{
		Log:    log.With("component", "curator"),
		Events: curatorEventSink{b.bus},
		// Tools resolves declared curator tool names from the same
		// process-wide registry a chat turn is given (b.toolReg): one
		// catalogue, resolved down to the declared set by the builder.
		Tools: toolbuilder.New(b.toolReg),
		// b.memEngines (*domainmemory.Registry) satisfies
		// curator.MemoryEngineSource's Get(name) signature directly, no
		// adapter needed. Set by setupMemoryEngine, which Run() calls before
		// setupCurators.
		MemoryEngines: b.memEngines,
		// Skills is a shared host service like Events/MemoryEngines --
		// registry.filter narrows it out of the view for any curator that
		// doesn't declare Manifest.Skills, per curator not per instance.
		Skills: skillcurator.NewStore(skillsRoot),
		// Conversations backs the session-memory curator; b.chatSessionStore
		// is opened by openChatSessions before setupCurators. The agent every
		// curator write is addressed to is the deployment's bot user: the same
		// value the turn runner reads agent-scope memory with
		// (TurnRunnerConfig.BotUser), so a fact written here is a fact a later
		// turn reaches.
		Conversations: sessioncurator.NewAdapter(b.chatSessionStore, b.cfg.BotUser),
		LLM:           curatorLLMRunner{llm: b.chatLLM, outcomes: b.providerOutcomes},
		// b.chatModels.ActiveModel() is the same source sendChatTurn uses
		// for a real chat turn (telegram_setup.go) -- not
		// b.defaultChatIdentity, which names a task-routing identity, not
		// a model reference.
		Model: b.chatModels.ActiveModel(),
	})
	if err := b.curatorRegistry.Register(skillcurator.New(skillcurator.DefaultInterval)); err != nil {
		log.Error("skill curator registration failed", "err", err)
	}
	if err := b.curatorRegistry.Register(sessioncurator.New(sessioncurator.DefaultInterval, infraMemory.EngineName)); err != nil {
		log.Error("session-memory curator registration failed", "err", err)
	}
	// Config definitions are seed data: each enabled [[curators]] entry is
	// registered through the one generic definition-driven engine. Code
	// registrations above win for their own name (a duplicate is refused
	// and logged, not silently replaced).
	for _, def := range b.cfg.Curators {
		if !def.Enabled {
			continue
		}
		engine := curator.NewDefinitionEngine(curator.Definition{
			Name:         def.Name,
			Enabled:      def.Enabled,
			Instructions: def.Instructions,
			Manifest: curator.Manifest{
				Interval:      def.Interval.Std(),
				Cooldown:      def.Cooldown.Std(),
				OnInput:       def.OnInput,
				Tools:         def.Tools,
				Skills:        def.Skills,
				MemoryEngine:  def.MemoryEngine,
				Conversations: def.Conversations,
				Model:         def.Model,
			},
		})
		if err := b.curatorRegistry.Register(engine); err != nil {
			log.Error("config curator registration failed", "curator", def.Name, "err", err)
		}
	}
	// The runtime owns the per-curator loops (archie-core-89x): one
	// goroutine per curator, wake nudges, per-pass budgets, panic
	// recovery, bounded shutdown. Stop order at shutdown: runtime first
	// (cancels in-flight passes, then stops curator lifecycle), then the
	// registry's own Stop below, which is a no-op by then.
	b.curatorRuntime = curator.NewRuntime(b.curatorRegistry, curator.RuntimeConfig{})
	// Primary chat turns wake input-driven curators (archie-core-035):
	// the forwarder consumes only primary-input kinds, and curator
	// output never produces them, so derived work cannot feed its own
	// trigger. The subscriber buffer is bounded and dropping — a slow
	// curator can never backpressure the chat publisher.
	curator.WakeOnPrimaryInput(ctx, b.bus, b.curatorRuntime, events.KindTurnCompleted)
	rt := b.curatorRuntime
	b.addCleanup(shutdownCuratorRuntime(rt, log))
	reg := b.curatorRegistry
	b.addCleanup(shutdownCuratorRegistry(reg, log))
}

func (b *boot) setupGuardrails() {
	log := b.log
	gc := tools.DefaultGuardrailConfig()
	b.guardrails = tools.NewGuardrailEngine(gc)
	log.Info(
		"guardrail engine enabled",
		"exact_failure_warn", gc.ExactFailureWarnAfter,
		"same_tool_failure_warn", gc.SameToolFailureWarnAfter,
		"no_progress_warn", gc.NoProgressWarnAfter,
	)
}

// registerTools registers the tool providers with a lifecycle: workspace
// file/shell tools and optional MCP servers. Memory tools
// (memory_create/update/delete/list) are not registered here -- they are
// built per turn onto the resolved Subject's scopes
// (internal/gateway/turn_memory_tool.go), not once at boot.
func (b *boot) registerTools(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	b.providerRegistry = toolprovider.NewRegistry(b.toolReg)
	// Workspace file and shell tools. Registered only when a workspace is
	// configured: these read, write and execute, so the directory is a
	// deliberate choice rather than a default.
	if workspace := cfg.Chat.Workspace; workspace != "" {
		unrestricted := cfg.Chat.UnrestrictedFilesystem
		if err := b.providerRegistry.Register(builtintoolprovider.New(workspace, unrestricted)); err != nil {
			log.Error("workspace tool provider registration failed", "err", err)
			return err
		}
		// Logged at the same level either way: which of the two postures is
		// running is the first thing worth knowing when a file tool refuses a
		// path, or reaches one it should not have.
		log.Info("workspace tools enabled",
			"workspace", workspace, "unrestricted_filesystem", unrestricted)
	} else {
		log.Info("workspace tools disabled (chat.workspace is unset)")
	}
	for _, srv := range cfg.Tools.MCPServers {
		provider, err := configuredMCPProvider(srv, cfg.WorkDir)
		if err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}
		// Optional: an MCP server is a third-party process pulled in at
		// runtime, and it is exactly the category that is allowed to be
		// absent. Registering it as required meant one failing npm package
		// unregistered every builtin tool and exited the daemon, which under
		// Restart=on-failure is a crash loop that takes chat and the gateway
		// with it.
		if err := b.providerRegistry.RegisterOptional(provider); err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}
	}
	if err := b.capabilityHost.Register(b.providerRegistry); err != nil {
		log.Error("tool-provider capability registration failed", "err", err)
		return err
	}
	return nil
}

// registerStandaloneTools registers the tools with no provider
// lifecycle: the skill catalog activator, the spill directory check and
// web_fetch.
func (b *boot) registerStandaloneTools() {
	cfg, log := b.cfg, b.log
	// Skill catalog → skill_activate tool (progressive disclosure: the
	// catalog's name+description is always in the tool schema; the full
	// SKILL.md body loads only when the model activates one).
	if catalog, err := skill.CatalogRoots(skill.DefaultRoots(cfg.WorkDir, cfg.SkillsDir)...); err != nil {
		log.Warn("skill catalog load failed", "err", err)
	} else if entry := skill.ActivateTool(cfg.WorkDir, catalog); entry != nil {
		if err := b.toolReg.Register(*entry); err != nil {
			log.Warn("skill_activate registration failed", "err", err)
		} else {
			log.Info("skill catalog registered", "skills", len(catalog))
		}
	}

	// Tool results too large to inline are written here. Created once, at
	// startup: the write failure inside a turn is silent, so without this
	// every oversized result would quietly fall back to truncation and
	// spilling would look configured while doing nothing.
	if spillDir := cfg.Tools.Policy.SpillDir; spillDir != "" {
		if err := toolLimits(cfg).EnsureSpillDir(); err != nil {
			log.Warn("tool spill directory unavailable; large results will be truncated instead", "err", err)
		} else if ws := cfg.Chat.Workspace; ws != "" && !cfg.Chat.UnrestrictedFilesystem && !isWithin(ws, spillDir) {
			// A spill hands the model a path to read back, and a confined read
			// tool refuses anything outside the workspace. Outside it, the
			// spill reference is a dead end and the result is lost rather than
			// displaced -- worse than truncating in the first place.
			//
			// Not a concern when the filesystem is unrestricted: the read tool
			// can reach the spill wherever it lives.
			log.Warn("tool spill directory is outside chat.workspace; the model cannot read back what is spilled there",
				"spill_dir", spillDir, "workspace", ws)
		}
	}

	// web_fetch. Registered directly rather than as a tool provider: it has
	// no process to start or stop, so the provider lifecycle would buy
	// nothing. Disabled by configuration returns nil and advertises nothing.
	if entry := webfetch.Tool(webfetch.Config{
		Enabled:              cfg.Tools.WebFetch.IsEnabled(),
		Timeout:              cfg.Tools.WebFetch.Timeout.Std(),
		MaxBytes:             cfg.Tools.WebFetch.MaxBytes,
		AllowPrivateNetworks: cfg.Tools.WebFetch.AllowPrivateNetworks,
	}); entry != nil {
		if err := b.toolReg.Register(*entry); err != nil {
			log.Warn("web_fetch registration failed", "err", err)
		} else {
			log.Info("web fetch enabled",
				"allow_private_networks", cfg.Tools.WebFetch.AllowPrivateNetworks)
		}
	} else {
		log.Info("web fetch disabled")
	}

	// send_file. Rooted at the same workspace as the file tools and gated
	// by the same confinement, because it hands a host file to an outbound
	// message: a send that could reach paths the read tool refuses would
	// be a way around whatever confinement is configured.
	if entry := sendfile.Tool(cfg.Chat.Workspace); entry != nil {
		if err := b.toolReg.Register(*entry); err != nil {
			log.Warn("send_file registration failed", "err", err)
		} else {
			log.Info("file sending enabled", "workspace", cfg.Chat.Workspace)
		}
	} else {
		log.Info("file sending disabled (chat.workspace is unset)")
	}

	b.registerMinimaxTool(cfg, log)
}

// registerMinimaxTool registers generate_video. Off by default -- see
// MinimaxConfig's doc comment, it spends real API credits per call -- and,
// unlike web_fetch, needs a resolved credential before it can be
// registered at all: a tool advertised with no working key would fail
// every call, the same "don't advertise a broken capability" reasoning as
// disabled-by-config. Split out of registerStandaloneTools to keep that
// function's branching flat rather than nesting this tool's three failure
// modes (resolve error, empty key, registration error) inside it.
func (b *boot) registerMinimaxTool(cfg config.Config, log *slog.Logger) {
	if !cfg.Tools.Minimax.IsEnabled() {
		log.Info("minimax video generation disabled")
		return
	}

	apiKey, err := b.secrets.Resolve(cfg.Tools.Minimax.APIKey)
	if err != nil {
		log.Warn("minimax video generation enabled but the API key failed to resolve; tool not registered", "err", err)
		return
	}
	if apiKey == "" {
		log.Warn("minimax video generation enabled but no API key is configured; tool not registered")
		return
	}

	entry := minimax.Tool(minimax.Config{Enabled: true, APIKey: apiKey, BaseURL: cfg.Tools.Minimax.BaseURL})
	if entry == nil {
		return
	}
	if err := b.toolReg.Register(*entry); err != nil {
		log.Warn("generate_video registration failed", "err", err)
		return
	}
	log.Info("minimax video generation enabled")
}

// buildDaemon constructs the daemon and hands the dashboard the handles
// it shares with it.
func (b *boot) buildDaemon() {
	log := b.log
	b.d = &daemon.Daemon{
		Cfg:                 b.cfgHolder,
		ConnectedNATS:       daemon.NATSEndpoint{URL: b.natsURL, Token: b.natsToken},
		ConnectedStateStore: daemon.StateStoreEndpoint{URL: strings.TrimSpace(b.cfg.Services.Get(config.ServiceNameState).Target), Token: b.stateStoreToken},
		Store:               b.stateStore,
		Bus:                 b.bus,
		Forge:               b.forgeClient,
		Trees:               b.trees,
		CapabilityHost:      b.capabilityHost,
		Storage:             b.storeBackend,
		Log:                 log,
		Tasks:               b.natsClient,
		Reactions:           b.reactionClient,
		WorktreeGrants:      b.worktreeGrants,
		StateStoreGrants:    b.stateStoreGrants,
		ContainerPool:       b.containerPool,
		KitLauncher:         b.kitLauncher,
		Guardrails:          b.guardrails,
		ToolRegistry:        b.toolReg,
		Identities:          b.identityRunners,
		RootIdentityID:      identity.StableID(configuredIdentityNames(b.cfg)[0]),
		TaskLogs:            b.taskLogs,
		AgentStatus:         b.agentStatus,
		KindWorkflows:       b.kindWorkflows,
		LabelWorkflows:      b.labelWorkflows,
		WorkflowDefinitions: b.workflowDefinitions,
		Playbooks:           b.playbooks,
	}
	if identities, ok := b.stateStore.(identity.Repository); ok {
		b.d.IdentityRepository = identities
	}
	// Consumer mapping/binding surfaces resolve from b.stateStore (the State
	// Store contract adapter): local by default, remote *staterpc.Client when
	// [services.state].target is set. See docs/prds/state-store-contract.md §10.
	if ms, ok := b.stateStore.(storecontract.MappingStore); ok {
		b.d.Mappings = ms
	}
	if bs, ok := b.stateStore.(storecontract.BindingStore); ok {
		b.d.Bindings = bs
	}
	if bd, ok := b.stateStore.(storecontract.BindingDispatcher); ok {
		b.d.BindingDispatcher = bd
	}
	if mm, ok := b.stateStore.(storecontract.MappingMatchRecorder); ok {
		b.d.MappingMatches = mm
	}
	if btc, ok := b.stateStore.(storecontract.BindingTaskCreator); ok {
		b.d.BindingTaskCreator = btc
	}
	// Dispatch is the second Authorizer call site: the chain built by
	// openAccessChain, the principal assembly over the wire, and the denial
	// record -- wired together or not at all (docs/prds/orgs-and-access.md).
	b.d.Access = b.accessChain
	if b.accessChain != nil {
		if principals, ok := b.stateStore.(access.PrincipalSource); ok {
			b.d.Principals = principals
		}
		if denials, ok := b.stateStore.(access.DenialStore); ok {
			b.d.Denials = denials
		}
	}
	b.d.PlaybookLedger = playbookLedger(b.stateStore, b.playbooks, b.log)
	b.setupForgeWebhook()
}

// playbookLedger returns source's dispatch ledger, or nil with a warning
// naming each action playbook that therefore will not run: an action playbook
// never fires without its at-most-once gate.
func playbookLedger(source any, playbooks *playbook.Store, log *slog.Logger) storecontract.PlaybookDispatcher {
	if pd, ok := source.(storecontract.PlaybookDispatcher); ok {
		return pd
	}
	for _, pb := range playbooks.Playbooks {
		if pb.IsActionPlaybook() {
			log.Warn("eda action playbook will not run: no dispatch ledger is wired", "playbook", pb.ID)
		}
	}
	return nil
}

// setupForgeWebhook starts the forge webhook receiver when intake is "webhook"
// or "both". It decodes GitHub issue events into task envelopes and publishes
// them through the same path the poller uses, so a labelled or assigned issue
// becomes work immediately instead of on the next poll. Called from
// buildDaemon, since it needs the freshly built (*daemon.Daemon).PublishTask.
//
// A secret that fails to resolve degrades rather than aborts boot, same
// reasoning as resolveForge: under intake="both" the poll is the deployment's
// explicit backstop for exactly this kind of misconfiguration (CLAUDE.md's
// "polling has to remain a first-class option, not a legacy fallback"), so a
// typo'd env var name must not take the whole daemon down and silently stop
// polling too. Config validation already requires webhook_secret to be a
// configured reference before intake="webhook"/"both" is accepted; this
// handles the reference resolving to nothing at runtime.
//
// Single-identity only: the receiver has one dispatch config (root
// Dispatch.Trigger/Label/BotUser), so a multi-identity deployment's
// per-identity trigger/label/bot user cannot be matched correctly. Rather
// than run with the wrong identity's rules and silently misclassify or drop
// events for identity-scoped repos, webhook intake refuses to start when
// [[identities]] is configured; those repos keep working via their own
// per-identity poll loop, unaffected. Extending this to multi-identity is a
// separate feature, not a bug in this one.
func (b *boot) setupForgeWebhook() {
	cfg, log := b.cfg, b.log
	if cfg.Forge.Intake != config.ForgeIntakeWebhook && cfg.Forge.Intake != config.ForgeIntakeBoth {
		return
	}
	if len(cfg.Identities) > 0 {
		log.Error("forge webhook disabled: multi-identity deployments are not supported yet (each identity's own poll loop is unaffected)")
		return
	}
	secretValue, err := b.secrets.Resolve(cfg.Forge.WebhookSecret)
	if err != nil || secretValue == "" {
		log.Error("forge webhook disabled: secret unavailable",
			"engine", cfg.Forge.WebhookSecret.Engine, "key", cfg.Forge.WebhookSecret.Key, "err", err)
		return
	}
	// The reaction publisher is the consumer's supply side: the same typed
	// reaction the poll produces, keyed source-independently, so webhook and
	// poll deliveries of one review dedup (pr-review-remediation.md
	// decision 2).
	publishReaction := func(ctx context.Context, reaction workintake.ReviewCommentEnvelope) error {
		payload, err := reaction.Encode()
		if err != nil {
			return err
		}
		return b.d.Tasks.PublishUnique(ctx, reaction.Subject(), reaction.IdempotencyKey(), payload)
	}
	receiver := forgewebhook.New(secretValue, cfg.Dispatch.Trigger, cfg.Label, cfg.BotUser, b.d.PublishTask, publishReaction, log)
	host, port := parseListenAddr(cfg.Forge.WebhookAddr, "0.0.0.0", 8645)
	addr := fmt.Sprintf("%s:%d", host, port)
	srv := &http.Server{Addr: addr, Handler: receiver}

	b.startGateways = append(b.startGateways, func() {
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("forge webhook server stopped", "err", err)
			}
		}()
		log.Info("forge webhook listening", "addr", addr)
	})
	b.addCleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	})
}

// publishConfig makes a new configuration the running one and republishes
// the dashboard's projection from it. Every path that changes configuration
// goes through here, so the running config and the published page can never
// diverge. The provenance chain it renders is b.currentProvenance, which the
// caller stores before publishing.
func (b *boot) publishConfig(ctx context.Context, cfg config.Config) {
	b.cfgHolder.Set(cfg)
	b.publishConfigSnapshot(ctx)
}

// configOrigins projects the current provenance chain into the dashboard's
// view type.
func (b *boot) configOrigins() []webui.ConfigOrigin {
	provenance := b.currentProvenance.Load()
	if provenance == nil {
		return nil
	}
	origins := make([]webui.ConfigOrigin, 0, len(provenance.Origins))
	for _, origin := range provenance.Origins {
		origins = append(origins, webui.ConfigOrigin{
			Path: origin.Path, Role: string(origin.Role), Layer: string(origin.Layer), Feature: string(origin.Feature),
		})
	}
	return origins
}

// configViewInput assembles the dashboard's configuration projection from
// the daemon's own configuration state. The daemon is the configuration
// owner, so it is the process that renders this view (archie-core-ml30).
func (b *boot) configViewInput(ctx context.Context) webui.ConfigViewInput {
	in := webui.ConfigViewInput{
		Config:     b.cfgHolder.Get(),
		Provenance: b.configOrigins(),
		Catalog:    catalogView(b.catalog),
	}
	if b.lastReload != nil {
		status := b.lastReload()
		in.Reload = &status
	}
	return in
}

// publishConfigSnapshot sends the dashboard's projection to the State Store,
// where a UI process reads it. This is the only crossing: the configuration
// owner publishes what it already renders, and no other process holds the
// daemon's config.Holder (archie-core-ymut).
//
// A failed publish degrades to a stale configuration page and is logged, not
// propagated: it must never fail the reload or dashboard write that produced
// the new configuration, both of which have already taken effect.
func (b *boot) publishConfigSnapshot(ctx context.Context) {
	snapshots, ok := b.stateStore.(storecontract.ConfigSnapshotStore)
	if !ok || b.cfgHolder == nil {
		return
	}
	// Detached from the caller: a dashboard edit's request context is
	// cancelled the moment the browser has its answer, and the write it
	// describes has already taken effect either way.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	document, err := json.Marshal(webui.BuildConfigView(b.configViewInput(ctx)))
	if err != nil {
		b.log.Warn("config snapshot not published", "err", err)
		return
	}
	err = snapshots.PutConfigSnapshot(ctx, storecontract.ConfigSnapshot{
		Schema:      webui.ConfigViewSchema,
		Document:    document,
		PublishedAt: time.Now().UTC(),
	})
	if err != nil {
		b.log.Warn("config snapshot not published", "err", err)
	}
}

// wireConfigPublishing installs SIGHUP reload and the reload status
// surface. The reload log warns about changed fields that require a
// restart, so an operator never has to read the source to find out
// whether their edit took effect. web.LastReload exposes the outcome to
// /api/config.
func (b *boot) wireConfigPublishing(ctx context.Context, cfgPath, overlayPath string) {
	log := b.log
	reloadController := newReloadController(b.loader, cfgPath, overlayPath, b.reloadConfig)
	b.lastReload = func() config.ReloadStatus {
		st := reloadController.Status()
		return st
	}

	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)
	b.addCleanup(func() { signal.Stop(reloadCh) })
	go reloadLoop(ctx, reloadCh, reloadController, log)

	// Give /cancel and /stop a handle on work already in flight. The
	// controller is built before the daemon exists, so the runtime is
	// attached here; gateways start further down, after d.Startup, so no
	// command can arrive before this is wired.
	b.chatController.WithRuntime(b.d)
}

// shutdownCapabilityHost returns a cleanup that stops the capability
// host; same Background reasoning as shutdownCuratorRuntime. Defined
// before startServices so the Stop-before-Start ordering stays visible
// in the source layout the contract test reads.
//
//nolint:contextcheck
func shutdownCapabilityHost(capabilityHost *plugin.Host, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := capabilityHost.Stop(stopCtx); err != nil {
			log.Error("capability host shutdown", "err", err)
		}
	}
}

// startServices starts the capability host, daemon and curator runtime,
// then launches the gateways. Optional providers that could not start
// are excluded rather than fatal, so the only way an operator learns
// about one is this loop's log line.
func (b *boot) startServices(ctx context.Context) error {
	log := b.log
	capabilityHost := b.capabilityHost
	b.addCleanup(shutdownCapabilityHost(capabilityHost, log))
	if err := b.capabilityHost.Start(ctx); err != nil {
		log.Error("capability host startup", "err", err)
		return err
	}
	for _, skipped := range b.providerRegistry.Skipped() {
		log.Error("tool provider unavailable; archie is running without its tools",
			"provider", skipped.ID, "err", skipped.Err)
	}

	if err := b.d.Startup(ctx); err != nil {
		log.Error("startup", "err", err)
		return err
	}
	if err := b.curatorRuntime.Start(ctx); err != nil {
		log.Error("curator runtime startup", "err", err)
	}
	if b.schedulingEngine != nil {
		if err := b.schedulingEngine.Start(ctx); err != nil {
			log.Error("scheduling engine startup", "err", err)
		}
	}
	for _, startGateway := range b.startGateways {
		startGateway()
	}
	return nil
}

// setupBackends wires the autonomous-workflow backends. Container setup may
// degrade, but NATS startup must succeed because there is no other task handoff.
func (b *boot) setupBackends(ctx context.Context) error {
	closeContainers := b.setupContainers(ctx)
	err := b.connectNATS(ctx)
	if err == nil {
		b.setupKitLauncher(ctx)
	}
	// Container discovery must precede embedded NATS so it can bind the
	// worker bridge gateway, but cleanup registration comes afterwards.
	// cleanups run LIFO: workers stop first, then the daemon client closes,
	// then the embedded server shuts down.
	b.addCleanup(closeContainers)
	return err
}

// shutdownCuratorRuntime returns a cleanup that stops the curator
// runtime. The stop path derives its own timeout budget from Background
// rather than the boot context, which is already cancelled by the time
// shutdown runs.
//
//nolint:contextcheck
func shutdownCuratorRuntime(rt *curator.Runtime, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := rt.Stop(stopCtx); err != nil {
			log.Error("curator runtime shutdown", "err", err)
		}
	}
}

// shutdownCuratorRegistry returns a cleanup that stops the curator
// registry; same Background reasoning as shutdownCuratorRuntime.
//
//nolint:contextcheck
func shutdownCuratorRegistry(reg *curator.Registry, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := reg.Stop(stopCtx); err != nil {
			log.Error("curator registry shutdown", "err", err)
		}
	}
}

// exitCode maps a bootstrap error to the process exit code.
func exitCode(err error) int {
	if err != nil {
		return 1
	}
	return 0
}

func (b *boot) runLoop(ctx context.Context, once bool) error {
	// Boot is done: the liveness surface stops answering 503, which is what
	// the update watchdog is waiting to see after a restart.
	b.health.markServing()
	// systemd's READY=1 asserts the same fact this line has just recorded, so
	// it is sent here rather than from a second notion of "started". Not
	// earlier: the stores, NATS, gateways and tool providers are all up by now.
	// Not later, after the first pass: a pass polls every repo and drains the
	// queue, and a unit whose TimeoutStartSec expires mid-pass would be
	// restarted while the daemon was working.
	b.announceReady()
	if once {
		b.d.Cycle(ctx)
		return nil
	}
	b.log.Info("archied running", "repos", len(b.cfg.Repos), "poll", b.cfg.PollInterval.Std().String(), "label", b.cfg.Label)
	// Drain monitoring only matters while the daemon stays up to be drained;
	// a --once cycle exits immediately, so a drain that arrives mid-cycle is
	// out of scope and the monitor is not started.
	b.startDrainMonitor(ctx)
	// Armed here, not at boot: the loop it reports on starts on the next line,
	// and a --once invocation has no loop to watchdog.
	b.startWatchdog(ctx)
	if err := b.d.Run(ctx); err != nil && ctx.Err() == nil {
		b.log.Error("daemon exited", "err", err)
		return err
	}
	return nil
}
