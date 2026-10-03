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
	"sync"
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
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	domainembedding "github.com/samcharles93/archie-core/internal/domain/embedding"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
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
	"github.com/samcharles93/archie-core/internal/infrastructure/transcription"
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
	// [services.state].target. It is set by openStateStoreAdapter, which
	// requires [services.state].target to be set. The b.st field remains solely
	// for the standalone archie-state-store binary, which serves the task store
	// from Postgres.
	stateStore storecontract.TaskStore
	// accessChain is the daemon's policy engine, built once from the stored
	// policies (openAccessChain); accessProblems is what the readiness
	// surface reports. Both are nil when no policy store is wired.
	accessChain    access.Authorizer
	accessProblems []infraaccess.Problem
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

	// catalog, catalogModels and catalogMu are the model catalog the process
	// last loaded and the model references it contributes. The refresh loop
	// replaces both together while the config paths read them, so they are one
	// value behind one lock rather than two fields that can disagree.
	catalogMu     sync.RWMutex
	catalog       modelcatalog.Snapshot
	catalogModels []string
	// catalogCachePath is where the catalog's last download is cached, beside
	// the config file. Empty when boot had no file path to derive it from.
	catalogCachePath string
	// catalogURL overrides the catalog endpoint. Zero in production:
	// modelcatalog defaults to models.dev. Set only by tests.
	catalogURL string
	// catalogRefreshInterval is how often the running daemon re-reads the
	// catalog. The daemon's refresh loop reads it; zero means the default.
	catalogRefreshInterval time.Duration

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

	// transcriber is the optional voice-transcription capability. It is built
	// beside the chat runtime from this process's own [models]/[providers],
	// which is the model-owning side of the Messaging boundary: a channel
	// frontend carries a voice note's bytes across the inbound wire, and this
	// process turns them into text before the turn is recorded. Nil when no
	// role is configured or the provider credential did not resolve; a turn
	// then keeps the frontend's media note rather than failing.
	transcriber messaging.Transcriber

	toolReg             *tools.Registry
	chatModels          *chatModelManager
	personas            *gateway.PersonaRegistry
	chatTasks           gateway.TaskCreator
	chatController      *gateway.StoreTaskController
	defaultChatIdentity string
	updateService       *releaseupdate.Service

	startGateways  []func()
	capabilityHost *plugin.Host
	// pluginReconciler loads plugin, module and secret-engine files dropped into
	// the running config's directories without a restart. Nil before boot's
	// startPluginReconcile, and in processes that load no directory (the
	// Gateway), where a stored plugin-settings change still layers but loads
	// nothing.
	pluginReconciler *pluginReconciler
	trees            *worktree.Manager
	worktreeGrants   *worktreerpc.Grants
	identityRunners  []*daemon.IdentityRunner
	memEngines       *domainmemory.Registry
	curatorRegistry  *curator.Registry
	curatorRuntime   *curator.Runtime
	providerRegistry *toolprovider.Registry
	// mcpMu guards mcpApplied, which records the MCP servers the provider
	// registry was last built from, keyed by configured name. A live
	// tool-settings change diffs the stored server set against it so an
	// unchanged server is left running and untouched (mcp_reconcile.go); the
	// mutex is held because registerTools runs at boot while the
	// control-plane watch may already be applying an update.
	mcpMu      sync.Mutex
	mcpApplied map[string]appliedMCPServer
	// mcpProvider builds the engine for one configured MCP server. Nil uses
	// configuredMCPProvider; a test injects a fake so the live reconciliation
	// can run without spawning a server process.
	mcpProvider func(config.MCPServer) (toolprovider.Engine, error)
	d           *daemon.Daemon
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
func (b *boot) loadConfig(_ context.Context, cfgPath, overlayPath string) error {
	loader := configuration.New(b.log)
	b.loader = loader
	doc, err := loader.Resolve(cfgPath, overlayPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	b.doc = doc
	// A stray/misspelled key parses and validates cleanly (unknown TOML keys
	// are otherwise silently discarded), so it must be visible somewhere rather
	// than just quietly doing nothing.
	if len(doc.UnknownKeys) > 0 {
		b.log.Warn("config file has unrecognised keys; check for typos", "keys", doc.UnknownKeys)
	}
	b.cfg = b.doc.Config
	b.cfgHolder = config.NewHolder(b.cfg)
	b.currentProvenance.Store(&b.doc.Provenance)
	return b.setupLogging()
}

func (b *boot) setupLogging() error {
	cfg := b.cfg
	if b.stderrLog {
		return nil
	}
	logFeed := logging.NewFeed(1000)
	b.logFeed = logFeed
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

	return b.openChatSessions(ctx)
}

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
	b.controlPlaneRPC = client.ControlPlane()
	b.controlPlane = controlplane.NewRPCClient(b.controlPlaneRPC)
	b.applyStatus = applystatus.New(b.processName, client, b.log)
	b.stateStoreGrants = &staterpc.GrantIssuer{Client: client}
	b.stateStoreToken = b.cfg.Services.ResolvedToken(config.ServiceNameState, b.secrets.Getenv)
	b.addCleanup(cleanup)
	return nil
}

func (b *boot) openStateStage(ctx context.Context) error {
	if err := b.openStores(ctx); err != nil {
		return err
	}
	if err := b.openDaemonStateSurfaces(); err != nil {
		return err
	}
	return b.openAccessChain(ctx)
}

func (b *boot) openAccessChain(ctx context.Context) error {
	source, ok := b.stateStore.(access.PolicySource)
	if !ok {
		return nil
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

// catalogState returns the model catalog the process is running and the model
// references it contributes. Both together, under one lock: the refresh loop
// replaces them as a pair.
func (b *boot) catalogState() (modelcatalog.Snapshot, []string) {
	b.catalogMu.RLock()
	defer b.catalogMu.RUnlock()
	return b.catalog, b.catalogModels
}

// setCatalogState installs a loaded catalog. Called only after everything it
// has to reach has been built, so a read that fails leaves the previous
// snapshot running.
func (b *boot) setCatalogState(snapshot modelcatalog.Snapshot, models []string) {
	b.catalogMu.Lock()
	defer b.catalogMu.Unlock()
	b.catalog = snapshot
	b.catalogModels = models
}

func (b *boot) loadCatalog(ctx context.Context, cfgPath string) {
	b.catalogCachePath = filepath.Join(filepath.Dir(cfgPath), "models.json")
	catalog, err := modelcatalog.Load(ctx, b.catalogOptions(b.catalogCachePath))
	if err != nil {
		b.log.Warn("model catalog unavailable; using configured providers and models", "err", err)
		return
	}
	models := applyModelCatalog(&b.cfg, catalog)
	b.cfgHolder.Set(b.cfg.Clone())
	b.setCatalogState(catalog, models)
	b.log.Info("model catalog loaded", "providers", len(catalog.Providers), "models", len(models))
}

func (b *boot) seedSoul(cfgPath string) {
	result, err := configuration.SeedSoul(cfgPath, agent.ShippedSoul())
	if err != nil {
		b.log.Warn("soul: starter file unavailable", "err", err)
		return
	}
	if result.Action == configuration.SoulCreated {
		b.log.Info("soul: starter file written", "path", result.Path, "action", result.Action)
	}
}

func (b *boot) refreshModelCatalog(ctx context.Context) error {
	catalog, err := modelcatalog.Load(ctx, b.catalogOptions(b.catalogCachePath))
	if err != nil {
		return err
	}
	base := b.cfgHolder.Get().Clone()
	models := applyModelCatalog(&base, catalog)
	cfg, _, err := b.runtimeConfig(ctx, base)
	if err != nil {
		return err
	}
	b.setCatalogState(catalog, models)
	b.publishConfig(ctx, cfg)
	b.rebuildChatModelRuntime(cfg)
	b.log.Info("model catalog refreshed", "providers", len(catalog.Providers), "models", len(models))
	return nil
}

func (b *boot) catalogOptions(cachePath string) modelcatalog.Options {
	opts := modelcatalog.Options{
		URL:       b.catalogURL,
		CachePath: cachePath,
	}
	if b.secrets != nil {
		opts.Getenv = b.secrets.Getenv
	}
	if b.cfgHolder != nil {
		opts.Configured = b.cfgHolder.Get().Providers
	}
	return opts
}

const modelCatalogRefreshInterval = time.Hour

func (b *boot) startModelCatalogRefresh(ctx context.Context) {
	every := b.catalogRefreshInterval
	if every <= 0 {
		every = modelCatalogRefreshInterval
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := b.refreshModelCatalog(ctx); err != nil {
					b.log.Warn("model catalog refresh failed; keeping the loaded catalog", "err", err)
				}
			}
		}
	}()
}

func (b *boot) setupObservability(ctx context.Context) {
	cfg, log := b.cfg, b.log
	bus := events.NewBus()
	b.bus = bus
	b.addCleanup(func() { bus.Close() })
	b.startUpdateRelay(ctx, updateReportPath(cfg.WorkDir, "webui"))
	sink := bus.Subscribe(256)
	go persistEvents(ctx, sink, b.stateStore, log)
}

const reactionStreamMaxAge = 24 * time.Hour

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
	b.addCleanup(func() { reactionClient.Close() })
	b.reactionClient = reactionClient
	log.Info("nats connected", "url", url, "task_stream", nats.DefaultStreamName, "reaction_stream", nats.DefaultReactionStreamName)
	return nil
}

func (b *boot) startEmbeddedNATS(ctx context.Context) (string, string, error) {
	cfg, log := b.cfg, b.log
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

func (b *boot) setupContainers(ctx context.Context) func() {
	containerPool, storeBackend, closeDocker := startContainers(ctx, b.cfgHolder, b.secrets, b.log)
	b.containerPool = containerPool
	b.storeBackend = storeBackend
	return closeDocker
}

func (b *boot) setupEmbeddings(cfg config.Config, log *slog.Logger) {
	if client, ok := infraembedding.New(cfg, infraembedding.Options{}); ok {
		b.embeddings = client
		log.Info("embedding capability enabled", "role", infraembedding.Role)
	} else if cfg.Models[infraembedding.Role] != "" {
		log.Warn("embedding capability configured but unavailable; capability disabled", "role", infraembedding.Role)
	}
}

func (b *boot) setupTranscriber(cfg config.Config, log *slog.Logger) {
	client, ok := transcription.New(cfg.Models, cfg.Providers, transcription.Options{
		ResolveSecret: b.secrets.Resolve,
	})
	if ok {
		b.transcriber = client
		log.Info("voice transcription enabled", "role", transcription.Role)
	} else if cfg.Models[transcription.Role] != "" {
		log.Warn("voice transcription configured but unavailable; capability disabled", "role", transcription.Role)
	}
}

func (b *boot) setupLLMAndChat(ctx context.Context) error {
	cfg, log := b.cfg, b.log

	if err := b.setupChatRuntime(ctx, cfg); err != nil {
		return err
	}

	b.setupEmbeddings(cfg, log)
	contract, cleanup, err := composeChatContract(cfg.Services, b.secrets)
	if err != nil {
		return err
	}
	b.addCleanup(cleanup)
	b.chat = &webui.ChatService{Contract: contract, Updates: b.updateService}
	b.setupReadinessProbes()
	return nil
}

const rateLimiterEvictInterval = time.Minute

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

func (b *boot) loadWorkflows() error {
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
	return nil
}

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

// loadModules builds the daemon's Module registry: operator-trusted,
// in-process, Yaegi-interpreted. A broken module is a startup failure -- the
// daemon does not start with a partial module set, matching the routing-file
// load pattern (not the degrade-and-skip plugin pattern). Kinds whose file is
// not present in the directory are simply not loaded. Split out of
// loadWorkflows; pure extraction, same log messages and error-return order as
// before.
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

// loadEDAPlaybooks loads the EDA playbook documents: trigger + workflow-kind
// actions and module-kind action playbooks with CEL when conditions and args
// values. Loaded at startup with the same reject-at-load rule -- any malformed
// playbook, mixed/unsupported action shape, unknown module kind, when compile
// failure, or args key failure aborts startup, matching the routing-file load
// pattern (not degrade-and-skip). A nonexistent dir is an empty store. Action
// playbooks run through the daemon once buildDaemon wires the dispatch ledger.
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

func (b *boot) buildTreesAndIdentities(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	b.buildWorktreeManager()

	for _, idCfg := range cfg.Identities {
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

	unsubscribeTaskActions, err := taskactions.Register(coreConn, b.taskActions(), log)
	if err != nil {
		log.Error("gateway task action register failed", "err", err)
		return err
	}
	b.addCleanup(unsubscribeTaskActions)
	return nil
}

func (b *boot) setupMemoryEngine() error {
	cfg, log := b.cfg, b.log
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

func (b *boot) memoryStore() gateway.MemoryStore {
	engine, ok := b.activeMemoryEngine()
	if !ok {
		return nil
	}
	return engine
}

func (b *boot) memoryWriter() gateway.MemoryWriteStore {
	engine, ok := b.activeMemoryEngine()
	if !ok {
		return nil
	}
	return engine
}

func (b *boot) setupCurators(ctx context.Context) {
	log := b.log.With("component", "curator")
	skillsRoot := b.cfg.SkillsDir
	if skillsRoot == "" {
		skillsRoot = b.cfg.WorkDir
	}
	b.curatorRegistry = curator.NewRegistry(curator.Registrar{
		Log:           log.With("component", "curator"),
		Events:        curatorEventSink{b.bus},
		Tools:         toolbuilder.New(b.toolReg),
		MemoryEngines: b.memEngines,
		Skills:        skillcurator.NewStore(skillsRoot),
		Conversations: sessioncurator.NewAdapter(b.chatSessionStore, b.cfg.BotUser),
		LLM:           curatorLLMRunner{llm: b.chatLLM, outcomes: b.providerOutcomes},
		Model:         b.chatModels.ActiveModel(),
	})

	if err := b.curatorRegistry.Register(skillcurator.New(skillcurator.DefaultInterval)); err != nil {
		log.Error("skill curator registration failed", "err", err)
	}

	if err := b.curatorRegistry.Register(sessioncurator.New(sessioncurator.DefaultInterval, infraMemory.EngineName)); err != nil {
		log.Error("session-memory curator registration failed", "err", err)
	}

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

	b.curatorRuntime = curator.NewRuntime(b.curatorRegistry, curator.RuntimeConfig{})
	curator.WakeOnPrimaryInput(ctx, b.bus, b.curatorRuntime, events.KindTurnCompleted)
	rt := b.curatorRuntime
	b.addCleanup(shutdownCuratorRuntime(rt, log))
	reg := b.curatorRegistry
	b.addCleanup(shutdownCuratorRegistry(reg, log))
}

func (b *boot) registerTools() error {
	cfg, log := b.cfgHolder.Get(), b.log
	b.providerRegistry = toolprovider.NewRegistry(b.toolReg)
	b.mcpMu.Lock()
	b.mcpApplied = make(map[string]appliedMCPServer, len(cfg.Tools.MCPServers))
	b.mcpMu.Unlock()
	if workspace := cfg.Chat.Workspace; workspace != "" {
		unrestricted := cfg.Chat.UnrestrictedFilesystem
		if err := b.providerRegistry.Register(builtintoolprovider.New(workspace, unrestricted)); err != nil {
			log.Error("workspace tool provider registration failed", "err", err)
			return err
		}

		log.Info("workspace tools enabled",
			"workspace", workspace, "unrestricted_filesystem", unrestricted)
	} else {
		log.Info("workspace tools disabled (chat.workspace is unset)")
	}
	for _, srv := range cfg.Tools.MCPServers {
		provider, err := b.buildMCPProvider(srv)
		if err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}

		if err := b.providerRegistry.RegisterOptional(provider); err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}
		b.mcpMu.Lock()
		b.mcpApplied[strings.TrimSpace(srv.Name)] = appliedMCPServer{server: srv, id: provider.Manifest().ID}
		b.mcpMu.Unlock()
	}
	if err := b.capabilityHost.Register(b.providerRegistry); err != nil {
		log.Error("tool-provider capability registration failed", "err", err)
		return err
	}
	return nil
}

func (b *boot) registerStandaloneTools() {
	cfg, log := b.cfg, b.log

	if catalog, err := skill.CatalogRoots(skill.DefaultRoots(cfg.WorkDir, cfg.SkillsDir)...); err != nil {
		log.Warn("skill catalog load failed", "err", err)
	} else if entry := skill.ActivateTool(cfg.WorkDir, catalog); entry != nil {
		if err := b.toolReg.Register(*entry); err != nil {
			log.Warn("skill_activate registration failed", "err", err)
		} else {
			log.Info("skill catalog registered", "skills", len(catalog))
		}
	}

	if spillDir := cfg.Tools.Policy.SpillDir; spillDir != "" {
		if err := toolLimits(cfg).EnsureSpillDir(); err != nil {
			log.Warn("tool spill directory unavailable; large results will be truncated instead", "err", err)
		} else if ws := cfg.Chat.Workspace; ws != "" && !cfg.Chat.UnrestrictedFilesystem && !isWithin(ws, spillDir) {
			log.Warn("tool spill directory is outside chat.workspace; the model cannot read back what is spilled there",
				"spill_dir", spillDir, "workspace", ws)
		}
	}

	b.registerWebFetchTool(cfg)

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

func (b *boot) registerWebFetchTool(cfg config.Config) {
	log := b.log
	entry := webfetch.Tool(webfetch.Config{
		Enabled:              cfg.Tools.WebFetch.IsEnabled(),
		Timeout:              cfg.Tools.WebFetch.Timeout.Std(),
		MaxBytes:             cfg.Tools.WebFetch.MaxBytes,
		AllowPrivateNetworks: cfg.Tools.WebFetch.AllowPrivateNetworks,
	})
	if entry == nil {
		log.Info("web fetch disabled")
		return
	}
	if err := b.toolReg.Register(*entry); err != nil {
		log.Warn("web_fetch registration failed", "err", err)
		return
	}
	log.Info("web fetch enabled",
		"allow_private_networks", cfg.Tools.WebFetch.AllowPrivateNetworks)
}

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
		Storage:             b.storeBackend,
		Log:                 log,
		Tasks:               b.natsClient,
		Reactions:           b.reactionClient,
		WorktreeGrants:      b.worktreeGrants,
		StateStoreGrants:    b.stateStoreGrants,
		ContainerPool:       b.containerPool,
		KitLauncher:         b.kitLauncher,
		Identities:          b.identityRunners,
		RootIdentityID:      identity.StableID(configuredIdentityNames(b.cfg)[0]),
		TaskLogs:            b.taskLogs,
		AgentStatus:         b.agentStatus,
		KindWorkflows:       b.kindWorkflows,
		LabelWorkflows:      b.labelWorkflows,
		WorkflowDefinitions: b.workflowDefinitions,
		WorkflowEnablement:  b.controlPlane,
		Playbooks:           b.playbooks,
	}

	if identities, ok := b.stateStore.(identity.Repository); ok {
		b.d.IdentityRepository = identities
	}

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

	receiver := forgewebhook.New(secretValue, cfg.Dispatch.Trigger, cfg.Label, cfg.BotUser, b.d.PublishTask, b.d.PublishReaction, log)
	host, port := parseListenAddr(cfg.Forge.WebhookAddr, "0.0.0.0", 8645)
	addr := fmt.Sprintf("%s:%d", host, port)
	srv := &http.Server{Addr: addr, Handler: receiver, ReadHeaderTimeout: 5 * time.Second}

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

func (b *boot) publishConfig(ctx context.Context, cfg config.Config) {
	b.cfgHolder.Set(cfg)
	b.publishConfigSnapshot(ctx)
}

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

func (b *boot) configViewInput() webui.ConfigViewInput {
	catalog, _ := b.catalogState()
	in := webui.ConfigViewInput{
		Config:     b.cfgHolder.Get(),
		Provenance: b.configOrigins(),
		Catalog:    catalogView(catalog),
	}
	if b.lastReload != nil {
		status := b.lastReload()
		in.Reload = &status
	}
	return in
}

func (b *boot) publishConfigSnapshot(ctx context.Context) {
	snapshots, ok := b.stateStore.(storecontract.ConfigSnapshotStore)
	if !ok || b.cfgHolder == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	document, err := json.Marshal(webui.BuildConfigView(b.configViewInput()))
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

	b.chatController.WithRuntime(b.d)
}

func shutdownCapabilityHost(capabilityHost *plugin.Host, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := capabilityHost.Stop(stopCtx); err != nil {
			log.Error("capability host shutdown", "err", err)
		}
	}
}

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

func (b *boot) setupBackends(ctx context.Context) error {
	closeContainers := b.setupContainers(ctx)
	err := b.connectNATS(ctx)
	if err == nil {
		b.setupKitLauncher(ctx)
	}

	b.addCleanup(closeContainers)
	return err
}

func shutdownCuratorRuntime(rt *curator.Runtime, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := rt.Stop(stopCtx); err != nil {
			log.Error("curator runtime shutdown", "err", err)
		}
	}
}

func shutdownCuratorRegistry(reg *curator.Registry, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := reg.Stop(stopCtx); err != nil {
			log.Error("curator registry shutdown", "err", err)
		}
	}
}

func exitCode(err error) int {
	if err != nil {
		return 1
	}
	return 0
}

func (b *boot) runLoop(ctx context.Context, once bool) error {
	b.health.markServing()
	b.announceReady()
	if once {
		b.d.Cycle(ctx)
		return nil
	}
	b.log.Info("archied running", "repos", len(b.cfg.Repos), "poll", b.cfg.PollInterval.Std().String(), "label", b.cfg.Label)
	b.startDrainMonitor(ctx)
	b.startWatchdog(ctx)
	if err := b.d.Run(ctx); err != nil && ctx.Err() == nil {
		b.log.Error("daemon exited", "err", err)
		return err
	}
	return nil
}
