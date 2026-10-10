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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/samcharles93/archie-core/internal/app/chattask"
	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/presence"
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
	"github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/infrastructure/kitrun"
	"github.com/samcharles93/archie-core/internal/infrastructure/logrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/messagingrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/webui"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// boot is the mutable wiring state assembled by the run() composition
// phases. Cleanup funcs are registered in creation order and run in
// reverse at shutdown, matching the LIFO ordering of the deferred calls
// they replace.
type boot struct {
	catalog *modelcatalog.Catalog
	cfg     config.Config
	log     *slog.Logger

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
	forgeExt    *forgeExtensions
	token       string

	// stateStore is the State Store contract adapter every daemon and gateway
	// store consumer depends on. It is ALWAYS the remote *staterpc.Client
	// dialed to the standalone archie-state-store gRPC service at
	// [services.state].target. It is set by openStateStoreAdapter, which
	// requires [services.state].target to be set.
	stateStore storecontract.TaskStore
	taskModels daemon.TaskModels
	// accessChain is the daemon's policy engine, built once from the stored
	// policies (openAccessChain); accessProblems is what the readiness
	// surface reports. Both are nil when no policy store is wired.
	accessChain access.Authorizer
	accessLive  *infraaccess.Live
	// stateStoreGrants issues each task's run credential
	// (daemon.RunCredentialIssuer), wrapping the same *staterpc.Client as
	// stateStore, which also verifies a credential for a worktree push.
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
	processName string

	bus *events.Bus
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
	kitLauncher *kitrun.Launcher
	// modelEgress gives native tasks model access through the egress proxy;
	// nil when the proxy cannot start.
	modelEgress daemon.ModelEgress
	// sessionGrants records live setup sessions' granted credential services
	// for the egress proxy's resolver.
	sessionGrants *sessionGrants
	storeBackend  storage.Backend

	// runtimeVersions records the version of every control-plane kind boot's
	// layering applied. Set once at boot before the runtime-resource watches
	// start, which read it as their resume points.
	runtimeVersions map[string]int64

	chatTasks           gateway.TaskCreator
	defaultChatIdentity string
	// messaging delivers outbound chat messages through the Messaging Service.
	messaging *messagingrpc.Client

	startGateways   []func()
	trees           *worktree.Manager
	identityRunners []*daemon.IdentityRunner
	d               *daemon.Daemon
	// schedulingEngine is the cron/scheduling ticker engine (setupScheduling).
	schedulingEngine *scheduling.Engine

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

func (b *boot) cleanup() {
	for _, v := range slices.Backward(b.cleanups) {
		v()
	}
}

func (b *boot) loadConfig(_ context.Context, cfgPath, overlayPath string) error {
	loader, doc, err := servicekit.Resolve(b.log, cfgPath, overlayPath)
	if err != nil {
		return err
	}
	b.loader, b.doc = loader, doc
	b.catalog = modelcatalog.NewCatalog(cfgPath)
	b.cfg = b.doc.Config
	b.cfgHolder = config.NewHolder(b.cfg)
	b.currentProvenance.Store(&b.doc.Provenance)
	b.setupLogging()
	return nil
}

// setupLogging stays on stderr for an offline command (stderrLog): creating,
// appending to or rotating the deployment's log is a side effect a diagnosis
// must not have.
func (b *boot) setupLogging() {
	if b.stderrLog {
		return
	}
	logs := servicekit.Logging(b.cfg, "daemon")
	b.log, b.logFeed, b.taskLogs = logs.Log, logs.Feed, logs.TaskLogs
	b.addCleanup(func() { _ = logs.Closer.Close() })
}

// openStores wires the secret registry and the conversation store. The
// registry starts with the env engine only; extension engines join it once the
// State Store client exists (openStateStoreAdapter), so nothing here resolves a
// reference that may name one.
func (b *boot) openStores(ctx context.Context) error {
	b.secrets = secret.NewRegistry()
	return nil
}

// openForge builds the forge client. It runs after the runtime settings are
// layered, so a token held by an extension engine can resolve.
func (b *boot) openForge(ctx context.Context) {
	if packages, ok := b.stateStore.(extension.Packages); ok {
		b.forgeExt = newForgeExtensions(extension.Source{Query: b.controlPlane, Packages: packages}, b.log)
		b.addCleanup(b.forgeExt.close)
	}
	b.forgeClient, b.token = resolveForge(ctx, b.cfg.Forge, "default", b.secrets, b.forgeExt, b.log)
}

func (b *boot) openStateStoreAdapter(ctx context.Context) error {
	target := strings.TrimSpace(b.cfg.Services.Get(config.ServiceNameState).Target)
	if target == "" {
		return fmt.Errorf("services.state.target is required: archied/archie-gateway no longer own archie.db; the standalone archie-state-store process owns it (docs/prds/state-store-contract.md §12 step 7)")
	}
	client, cleanup, err := servicekit.StateStoreClient(presence.Daemon, b.cfg.Services, b.secrets)
	if err != nil {
		b.log.Error("state store adapter", "err", err)
		return err
	}
	b.stateStore = client
	b.taskModels = client
	b.controlPlaneRPC = client.ControlPlane()
	b.controlPlane = controlplane.NewRPCClient(b.controlPlaneRPC)
	b.applyStatus = applystatus.New(b.processName, client, b.log)
	b.stateStoreGrants = &staterpc.GrantIssuer{Client: client}
	b.stateStoreToken = b.cfg.Services.ResolvedToken(config.ServiceNameState, b.secrets.Getenv)
	b.addCleanup(cleanup)
	b.startSecretEngines(ctx, client)
	return nil
}

func (b *boot) openStateStage(ctx context.Context) error {
	if err := b.openStores(ctx); err != nil {
		return err
	}
	if err := b.openDaemonStateSurfaces(ctx); err != nil {
		return err
	}
	return b.openAccessChain(ctx)
}

func (b *boot) openAccessChain(ctx context.Context) error {
	source, ok := b.stateStore.(access.PolicySource)
	if !ok {
		return nil
	}
	live, err := infraaccess.NewLive(ctx, source.Policies, b.log)
	if err != nil {
		return fmt.Errorf("load stored policies: %w", err)
	}
	b.accessChain, b.accessLive = live, live
	go live.Run(ctx, applystatus.RestampInterval)
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

func (b *boot) connectNATS(ctx context.Context) error { // embedded broker discovery and startup require explicit fallback branches
	cfg, log := b.cfg, b.log
	url := cfg.NATS.URL
	var natsToken string
	if cfg.NATS.Mode == config.NATSModeExternal { //nolint:nestif // embedded broker discovery and startup require explicit fallback branches
		if url == "" {
			return fmt.Errorf("nats.url is required when nats.mode is external")
		}
		var err error
		natsToken, err = servicekit.NATSToken(cfg.NATS, os.Getenv)
		if err != nil {
			log.Error("nats credentials", "err", err)
			return err
		}
	} else {
		endpoint, readErr := servicekit.ReadNATSEndpoint(cfg.StateDir)
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
	// A restarted broker keeps the previous endpoint, so the Gateway and
	// running agents reconnect to it instead of a broker that no longer exists.
	opts := nats.EmbeddedOptions{Host: host, StoreDir: filepath.Join(cfg.StateDir, "nats")}
	if b.stateStoreGrants != nil {
		opts.Tasks = taskBroker{runs: b.stateStoreGrants.Client}
	}
	if previous, err := servicekit.ReadNATSEndpoint(cfg.StateDir); err == nil {
		opts.Token = previous.Token
		if u, err := url.Parse(previous.URL); err == nil {
			opts.Port, _ = strconv.Atoi(u.Port())
		}
	}
	srv, err := nats.StartEmbedded(ctx, opts, log)
	if err != nil && opts.Port != 0 {
		log.Warn("embedded nats: previous port unavailable; taking a new one", "port", opts.Port, "err", err)
		opts.Port = 0
		srv, err = nats.StartEmbedded(ctx, opts, log)
	}
	if err != nil {
		log.Error("embedded nats start failed", "err", err)
		return "", "", err
	}
	b.addCleanup(func() { srv.Shutdown() })
	log.Info("embedded nats started", "url", srv.ClientURL())
	if err := servicekit.WriteNATSEndpoint(cfg.StateDir, srv.ClientURL(), srv.Token()); err != nil {
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

// setupGatewayClient dials the Gateway, which owns every model call. The daemon
// only enqueues the chat tasks scheduled workflows create.
func (b *boot) setupGatewayClient(ctx context.Context) error {
	b.setupChatTasks(b.cfg)
	contract, cleanup, err := composeChatContract(b.cfg.Services, b.secrets)
	if err != nil {
		return err
	}
	b.addCleanup(cleanup)
	b.chat = &webui.ChatService{Contract: contract}
	messaging, closeMessaging, err := composeMessaging(b.cfg.Services, b.secrets)
	if err != nil {
		return err
	}
	b.addCleanup(closeMessaging)
	b.messaging = messaging
	go postOutcomes(ctx, b.stateStore, messaging.Deliver, b.log)
	b.setupReadinessProbes()
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
		idForge, idToken := resolveForge(ctx, idCfg.Forge, idCfg.Name, b.secrets, b.forgeExt, log.With("identity", idCfg.Name))
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
	unsubscribe, err := registerTaskRPCServers(coreConn, b.forgeClient, b.trees, b.identityRunners, b.stateStoreGrants.Client, log)
	if err != nil {
		log.Error("task RPC server registration failed", "err", err)
		return err
	}
	b.addCleanup(unsubscribe)

	stopLogs, err := logrpc.RegisterDaemon(coreConn, b.logFeed, log)
	if err != nil {
		return fmt.Errorf("register daemon logs: %w", err)
	}
	b.addCleanup(stopLogs)
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

func (b *boot) buildDaemon() {
	log := b.log
	b.d = &daemon.Daemon{
		Cfg:                 b.cfgHolder,
		ConnectedNATS:       daemon.NATSEndpoint{URL: b.natsURL, Token: b.natsToken, Scoped: b.cfg.NATS.Mode != config.NATSModeExternal},
		ConnectedStateStore: daemon.StateStoreEndpoint{URL: strings.TrimSpace(b.cfg.Services.Get(config.ServiceNameState).Target), Token: b.stateStoreToken},
		Store:               b.stateStore,
		Bus:                 b.bus,
		Forge:               b.forgeClient,
		Trees:               b.trees,
		Storage:             b.storeBackend,
		Log:                 log,
		Tasks:               b.natsClient,
		Reactions:           b.reactionClient,
		RunCredentials:      b.stateStoreGrants,
		ContainerPool:       b.containerPool,
		KitLauncher:         b.kitLauncher,
		ModelEgress:         b.modelEgress,
		TaskModels:          b.taskModels,
		Identities:          b.identityRunners,
		RootIdentityID:      identity.StableID(servicekit.IdentityNames(b.cfg)[0]),
		TaskLogs:            b.taskLogs,
		AgentStatus:         b.agentStatus,
		WorkflowDefinitions: b.workflowDefinitions,
		WorkflowEnablement:  b.controlPlane,
		Playbooks:           b.livePlaybooks(),
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
	b.d.WithdrawTask = b.taskActions().Withdraw

	b.d.Access = b.accessChain
	if b.accessChain != nil {
		if principals, ok := b.stateStore.(access.PrincipalSource); ok {
			b.d.Principals = principals
		}
		if denials, ok := b.stateStore.(access.DenialRecorder); ok {
			b.d.Denials = denials
		}
	}
	b.d.PlaybookLedger = playbookLedger(b.stateStore, b.log)
	b.setupForgeWebhook()
}

func playbookLedger(source any, log *slog.Logger) storecontract.PlaybookDispatcher {
	if pd, ok := source.(storecontract.PlaybookDispatcher); ok {
		return pd
	}
	log.Warn("eda action playbooks will not run: no dispatch ledger is wired")
	return nil
}

func (b *boot) setupForgeWebhook() {
	cfg, log := b.cfg, b.log
	if cfg.Forge.Intake != config.ForgeIntakeWebhook && cfg.Forge.Intake != config.ForgeIntakeBoth {
		return
	}
	parser, ok := b.forgeClient.(forge.WebhookParser)
	if !ok {
		log.Error("forge webhook disabled: the forge cannot parse webhooks", "forge_type", cfg.Forge.Type)
		return
	}
	secretValue, err := b.secrets.Resolve(cfg.Forge.WebhookSecret)
	if err != nil || secretValue == "" {
		log.Error("forge webhook disabled: secret unavailable",
			"engine", cfg.Forge.WebhookSecret.Engine, "key", cfg.Forge.WebhookSecret.Key, "err", err)
		return
	}

	receiver := forgewebhook.New(secretValue, parser, cfg.Dispatch.Trigger, cfg.Label, cfg.BotUser, b.d.PublishTask, b.d.PublishReaction, b.withdrawIssue, log)
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
	// The daemon owns the published configuration view.
	if b.processName != applystatus.Gateway {
		b.publishConfigSnapshot(ctx)
	}
}

func (b *boot) configViewInput() webui.ConfigViewInput {
	catalog := b.catalogState()
	in := webui.ConfigViewInput{
		Config:  b.cfgHolder.Get(),
		Catalog: catalogView(catalog),
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
}

func (b *boot) startServices(ctx context.Context) error {
	log := b.log
	if err := b.d.Startup(ctx); err != nil {
		log.Error("startup", "err", err)
		return err
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
	// Registered before the Kit launcher so its cleanups (the harness
	// listener and the egress relay) run first: cleanups unwind LIFO, and a
	// session teardown needs the Docker client the pool still owns.
	b.addCleanup(closeContainers)
	err := b.connectNATS(ctx)
	if err == nil {
		b.setupKitLauncher(ctx)
	}
	return err
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
		// A drain pass no longer waits for the tasks it submitted, so a
		// single-cycle invocation waits for them here: exiting while their
		// containers still run would kill work this pass accepted.
		b.d.WaitForTasks()
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

// setupChatTasks wires the task creator scheduled workflows enqueue through.
func (b *boot) setupChatTasks(cfg config.Config) {
	profiles, defaultChatIdentity := chattask.Profiles(cfg)
	if len(profiles) > 0 {
		b.chatTasks = gateway.NewStoreTaskCreatorForProfiles(
			chattask.Writer{Enqueue: b.stateStore.EnqueueChatTask},
			profiles,
		)
	}
	b.defaultChatIdentity = defaultChatIdentity
}

func (b *boot) addCleanup(fn func()) {
	b.cleanups = append(b.cleanups, fn)
}

func (b *boot) catalogState() modelcatalog.Snapshot { return b.catalog.State() }

// loadCatalog layers the model catalog under the file config. A catalog that
// cannot be read leaves the configured providers and models in effect.
func (b *boot) loadCatalog(ctx context.Context) {
	snapshot, err := b.catalog.Fetch(ctx, b.secrets.Getenv, b.cfgHolder.Get().Providers)
	if err != nil {
		b.log.Warn("model catalog unavailable; using configured providers and models", "err", err)
		return
	}
	models := modelcatalog.Apply(&b.cfg, snapshot)
	b.cfgHolder.Set(b.cfg.Clone())
	b.catalog.Set(snapshot)
	b.log.Info("model catalog loaded", "providers", len(snapshot.Providers), "models", len(models))
}

func (b *boot) refreshModelCatalog(ctx context.Context) error {
	snapshot, err := b.catalog.Fetch(ctx, b.secrets.Getenv, b.cfgHolder.Get().Providers)
	if err != nil {
		return err
	}
	base := b.cfgHolder.Get().Clone()
	models := modelcatalog.Apply(&base, snapshot)
	cfg, _, err := b.runtimeConfig(ctx, base)
	if err != nil {
		return err
	}
	b.catalog.Set(snapshot)
	b.publishConfig(ctx, cfg)
	b.log.Info("model catalog refreshed", "providers", len(snapshot.Providers), "models", len(models))
	return nil
}

func (b *boot) startModelCatalogRefresh(ctx context.Context) {
	modelcatalog.Every(ctx, modelcatalog.RefreshInterval, func() {
		if err := b.refreshModelCatalog(ctx); err != nil {
			b.log.Warn("model catalog refresh failed; keeping the loaded catalog", "err", err)
		}
	})
}

func (b *boot) livePlaybooks() *controlplane.LivePlaybooks {
	live := controlplane.NewLivePlaybooks(b.controlPlaneRPC, b.log)
	live.ApplyStatus = b.applyStatus
	return live
}
