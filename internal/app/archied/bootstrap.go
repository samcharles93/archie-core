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
	"sort"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec"
	channelruntime "github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/email"
	"github.com/samcharles93/archie-core/internal/channels/webhook"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	domainembedding "github.com/samcharles93/archie-core/internal/domain/embedding"
	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	forgewebhook "github.com/samcharles93/archie-core/internal/forge/webhook"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/overlay"
	infraembedding "github.com/samcharles93/archie-core/internal/infrastructure/embedding"
	"github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	infraMemory "github.com/samcharles93/archie-core/internal/infrastructure/memory"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/infrastructure/sessioncurator"
	"github.com/samcharles93/archie-core/internal/infrastructure/skillcurator"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/minimax"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	builtintoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/builtin"
	memorytoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/memory"
	"github.com/samcharles93/archie-core/internal/tools/sendfile"
	"github.com/samcharles93/archie-core/internal/tools/webfetch"
	"github.com/samcharles93/archie-core/internal/webhookguard"
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

	loader            *configuration.Loader
	doc               *configuration.Document
	overlayStore      *overlay.Store
	bootOverlayErr    atomic.Pointer[string]
	currentProvenance atomic.Pointer[configuration.Provenance]

	logFeed  *logging.Feed
	taskLogs *logging.TaskRegistry

	secrets     *secret.Registry
	forgeClient forge.Forge
	token       string

	modules *module.ModuleRegistry

	playbooks *playbook.Store

	st               store.TaskStore
	chatSessionStore gateway.SessionStore

	catalog       modelcatalog.Snapshot
	catalogModels []string

	bus             *events.Bus
	restartTelegram func() error
	channelManager  *channelruntime.Manager
	web             *webui.Server

	natsClient *nats.Client
	// natsURL is the endpoint the daemon's own client connected with at
	// startup. For external mode it is cfg.NATS.URL; for embedded mode it is
	// the embedded server's ClientURL(). Recorded so Daemon.ConnectedNATS
	// carries the real endpoint rather than an empty config URL.
	natsURL string
	// natsToken is the credential the daemon's client connected with at
	// startup. Embedded mode generates it; external mode resolves it once.
	natsToken string

	containerPool *container.Pool
	storeBackend  storage.Backend

	llm *runtime.Runtime

	// embeddings is nil unless models["embedding"] and a working provider
	// credential are both configured -- see setupLLMAndChat's "Embeddings
	// capability" section. Consumers must treat a nil embeddings the same
	// as domainembedding.ErrUnavailable: skip, never fail startup or an
	// unrelated call.
	embeddings domainembedding.Client

	toolReg             *tools.Registry
	chatModels          gateway.ModelManager
	personas            *gateway.PersonaRegistry
	chatTasks           gateway.TaskCreator
	chatController      *gateway.StoreTaskController
	defaultChatIdentity string
	updateService       *releaseupdate.Service
	webRouter           *gateway.Router

	startGateways    []func()
	capabilityHost   *plugin.Host
	trees            *worktree.Manager
	worktreeGrants   *worktreerpc.Grants
	identityRunners  []*daemon.IdentityRunner
	memManager       *memory.Manager
	memEngines       *domainmemory.Registry
	curatorRegistry  *curator.Registry
	curatorRuntime   *curator.Runtime
	guardrails       *tools.GuardrailEngine
	providerRegistry *toolprovider.Registry
	d                *daemon.Daemon

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

func (b *boot) addCleanup(fn func()) {
	b.cleanups = append(b.cleanups, fn)
}

func (b *boot) cleanup() {
	for _, v := range slices.Backward(b.cleanups) {
		v()
	}
}

// loadConfig resolves the file config and layers the runtime overlay
// over it. A Resolve failure is reported on stderr because the file log
// destination is itself configuration that has not been read yet.
func (b *boot) loadConfig(ctx context.Context, cfgPath, overlayPath string, noConfigOverlay bool) error {
	loader := configuration.New(b.log)
	b.loader = loader
	skipOverlay := noConfigOverlay || configuration.SkipOverlay()
	if skipOverlay {
		b.log.Info("runtime config overlay disabled (recovery hatch); booting on file config alone")
	}
	doc, err := loader.Resolve(cfgPath, overlayPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	b.doc = doc
	// Runtime config overlay: dashboard-edited overrides layered over the
	// file config from their own SQLite file (own user_version and
	// migrator, no contention with the task store). Skipped under
	// --no-config-overlay / ARCHIE_SKIP_CONFIG_OVERLAY=1, or when the
	// store cannot be opened or its values fail validation -- a broken
	// overlay must not brick the daemon. bootOverlayErr carries the
	// degrade reason into /api/config's reload status so it is visible
	// where the operator is looking, not only in logs.
	if !skipOverlay {
		b.overlayStore, b.doc = bootConfigOverlay(ctx, loader, doc, &b.bootOverlayErr, b.log)
		if b.overlayStore != nil {
			store := b.overlayStore
			b.addCleanup(func() { _ = store.Close() })
		}
	}
	b.cfg = b.doc.Config
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
	logFeed := logging.NewFeed(1000)
	b.logFeed = logFeed
	// Task logs live alongside the store rather than under cfg.Log.File's
	// directory: cfg.Log.File is optional (file logging can be off), while
	// DBPath is required for the daemon to run at all, so it is the more
	// reliable anchor for "where archie keeps its state" on this host.
	taskLogs := logging.NewTaskRegistry(filepath.Join(filepath.Dir(cfg.DBPath), "logs", "tasks"), logFeed, logging.TaskSinkOptions{})
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
	b.forgeClient, b.token = resolveForge(cfg.Forge, secrets, log)

	bindingCipher, err := bindingCipherFromConfig(cfg, secrets)
	if err != nil {
		log.Error("configure bindings cipher", "err", err)
		return err
	}

	st, err := openProductionTaskStore(ctx, taskDBPath(cfg.DBPath), store.WithBindingCipher(bindingCipher))
	if err != nil {
		log.Error("open store", "err", err)
		return err
	}
	b.st = st
	b.addCleanup(func() {
		if err := st.Close(); err != nil {
			log.Error("close store", "err", err)
		}
	})
	chatSessionStore, err := makeTelegramSessionStore(cfg) //nolint:contextcheck // the session store opener takes no context by design; its schema init must complete at boot regardless of cancellation
	if err != nil {
		log.Error("open conversation store", "path", conversationDBPath(cfg.DBPath), "err", err)
		return err
	}
	b.chatSessionStore = chatSessionStore
	b.addCleanup(func() {
		if err := chatSessionStore.Close(); err != nil {
			log.Error("close conversation store", "err", err)
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
	if err := manualRequeueTask(ctx, b.st, requeue); err != nil {
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

// setupObservability builds the event bus, channel manager and dashboard
// server. Every event is logged to SQLite (stamped with its row id) and
// then fanned out to live dashboard connections.
func (b *boot) setupObservability() {
	cfg, log := b.cfg, b.log
	bus := events.NewBus()
	b.bus = bus
	b.addCleanup(func() { bus.Close() })
	telegramDetail := ""
	if (cfg.Chat.Telegram.Token != (secret.SecretRef{}) || cfg.Chat.Telegram.TokenEnv != "") && len(cfg.Chat.Telegram.AllowedUserIDs) == 0 {
		telegramDetail = "Token set, but the allowlist is empty -- the bot answers nobody."
	}
	b.channelManager = channelruntime.NewManager([]channelruntime.Descriptor{
		{ID: "telegram", Name: "Telegram", Configured: cfg.Chat.Telegram.Token != (secret.SecretRef{}) || cfg.Chat.Telegram.TokenEnv != "", ReloadSupported: cfg.Chat.Telegram.Token != (secret.SecretRef{}) || cfg.Chat.Telegram.TokenEnv != "", Detail: telegramDetail},
		{ID: "email", Name: "Email", Configured: cfg.Chat.Email.ListenAddr != ""},
		{ID: "webhook", Name: "Webhook gateway", Configured: cfg.Chat.WebhookAddr != ""},
	})
	configProvenance := make([]webui.ConfigOrigin, 0, len(b.doc.Provenance.Origins))
	for _, origin := range b.doc.Provenance.Origins {
		configProvenance = append(configProvenance, webui.ConfigOrigin{
			Path: origin.Path, Role: string(origin.Role), Layer: string(origin.Layer), Feature: string(origin.Feature),
		})
	}
	b.web = &webui.Server{Store: b.st, Log: log.With("component", "webui"), LogFeed: b.logFeed, TaskLogs: b.taskLogs, Cfg: config.NewHolder(cfg), Channels: b.channelManager, Events: bus}
	b.web.UpdateReportPath = updateReportPath(cfg.WorkDir, "webui")
	if (cfg.Chat.Telegram.Token != (secret.SecretRef{}) || cfg.Chat.Telegram.TokenEnv != "") && len(cfg.Chat.Telegram.AllowedUserIDs) > 0 {
		b.web.TelegramUpdateReportPath = updateReportPath(cfg.WorkDir, cfg.BotUser)
		b.web.TelegramUpdateChatID = cfg.Chat.Telegram.AllowedUserIDs[0]
	}
	agentStatus := b.agentStatus
	b.web.RunningVersions = func() map[string]string { return daemonRunningVersions(agentStatus) }
	b.web.SetProvenance(configProvenance)
	b.web.ReloadChannel = func(ctx context.Context, id string) error {
		if id != "telegram" || b.restartTelegram == nil {
			return fmt.Errorf("channel reload unavailable")
		}
		return b.restartTelegram()
	}
	// The dashboard closes the forge issue behind a rejected task, or the
	// issue is re-polled and the work is done again. It gets only that one
	// method, not the forge client.
	b.web.Issues = b.forgeClient
	b.wireWebStoreSurfaces()
	sink := bus.Subscribe(256)
	go persistAndBroadcastEvents(sink, b.st, b.web, log)
}

// wireWebStoreSurfaces attaches the dashboard's optional storage surfaces.
// openProductionTaskStore's declared return type is the narrow
// store.TaskStore, so each wider surface (all implemented by the same
// *store.Store) needs its own assertion here rather than a direct field
// reuse. A store that does not implement one degrades that dashboard feature
// with a warning instead of aborting bootstrap. The BindingStore/
// BindingDispatcher/BindingTaskCreator split keeps each interface under the
// interfacebloat limit. See docs/prds/event-capture-storage.md,
// docs/prds/payload-field-mapping.md and docs/prds/webhook-intake-security.md.
func (b *boot) wireWebStoreSurfaces() {
	cfg, log := b.cfg, b.log
	if cs, ok := b.st.(store.CaptureStore); ok {
		b.web.Captures = cs
	} else {
		log.Warn("capture storage unavailable: task store does not implement CaptureStore")
	}
	b.web.CaptureRetention = cfg.Capture.Retention.Std()
	b.web.CaptureMaxEvents = cfg.Capture.MaxEvents
	b.web.CaptureMaxBodyBytes = int64(cfg.Capture.MaxBodyBytes)
	b.web.CaptureLimiter = webhookguard.NewRateLimiter(cfg.Capture.RatePerSecond, cfg.Capture.RateBurst, time.Now)
	if ms, ok := b.st.(store.MappingStore); ok {
		b.web.Mappings = ms
	} else {
		log.Warn("mapping storage unavailable: task store does not implement MappingStore")
	}
	if bs, ok := b.st.(store.BindingStore); ok {
		b.web.Bindings = bs
	} else {
		log.Warn("binding storage unavailable: task store does not implement BindingStore")
	}
	if bd, ok := b.st.(store.BindingDispatcher); ok {
		b.web.BindingDispatcher = bd
	} else {
		log.Warn("binding dispatcher unavailable: task store does not implement BindingDispatcher")
	}
}

// connectNATS opens the NATS client. External mode dials cfg.NATS.URL;
// embedded mode starts an in-process nats-server and dials it, so single-
// process deployments get task distribution and reaction delivery without a
// separate server. Broker deployment never changes the worker executor.
func (b *boot) connectNATS(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	url := cfg.NATS.URL
	var natsToken string
	if cfg.NATS.Mode == config.NATSModeExternal {
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
		var err error
		url, natsToken, err = b.startEmbeddedNATS(ctx)
		if err != nil {
			return err
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
	b.natsClient = natsClient
	b.natsURL = url
	b.natsToken = natsToken
	b.addCleanup(func() { natsClient.Close() })
	log.Info("nats connected", "url", url)
	return nil
}

// startEmbeddedNATS starts the in-process nats-server and returns the client
// URL and token to dial it with. Its shutdown is registered BEFORE the caller
// registers the client close, so the client closes first (cleanups run LIFO).
func (b *boot) startEmbeddedNATS(ctx context.Context) (string, string, error) {
	cfg, log := b.cfg, b.log
	host := ""
	if b.containerPool != nil {
		host = b.containerPool.HostGateway()
	}
	srv, err := nats.StartEmbedded(ctx, nats.EmbeddedOptions{
		Host:     host,
		StoreDir: filepath.Join(filepath.Dir(cfg.DBPath), "nats"),
	}, log)
	if err != nil {
		log.Error("embedded nats start failed", "err", err)
		return "", "", err
	}
	b.addCleanup(func() { srv.Shutdown() })
	log.Info("embedded nats started", "url", srv.ClientURL())
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
func (b *boot) setupLLMAndChat(ctx context.Context) {
	cfg, log := b.cfg, b.log

	// ── LLM runtime ──────────────────────────────────────────────────
	// Created before gateways so the LLMResponder can be wired into the
	// Telegram router for non-command message processing.
	providers := executionProviders(cfg)
	b.llm = agentexec.NewRuntime(providers)
	b.toolReg = tools.NewRegistry()
	chatModels := newChatModelManager(cfg.Models, cfg.Chat.Models, b.catalogModels)
	chatModels.ApplyModelCatalog(b.catalog)
	b.chatModels = chatModels

	b.setupEmbeddings(cfg, log)

	// ── Persona registry ─────────────────────────────────────────────
	b.personas = gateway.NewPersonaRegistry(gateway.DefaultPersonas())

	profiles, defaultChatIdentity := chatTaskProfiles(cfg)
	var chatTasks gateway.TaskCreator
	if len(profiles) > 0 {
		chatTasks = gateway.NewStoreTaskCreatorForProfiles(
			chatTaskWriterAdapter{enqueue: b.st.EnqueueChatTask},
			profiles,
		)
	}
	b.chatTasks = chatTasks
	b.defaultChatIdentity = defaultChatIdentity
	chatController := gateway.NewStoreTaskController(chatTaskControllerAdapter{
		taskByID:   b.st.TaskByID,
		requeue:    b.st.Requeue,
		transition: b.st.Transition,
	})
	b.chatController = chatController
	b.updateService = makeUpdateService(telegramSetup{Cfg: config.NewHolder(cfg)})
	b.web.WorkRequests = b.chatTasks

	// The dashboard is another gateway, not a second chat implementation. It
	// shares the router, session history, model selection, personas and LLM
	// responder with Telegram while using a stable browser channel id.
	b.webRouter = gateway.NewRouter(b.st, nil, "web")
	b.webRouter.Version = fmt.Sprintf("Archie\nGateway: %s\nRuntime: %s", gatewayVersion, runtimeVersion)
	if cfg.Chat.Telegram.Token != (secret.SecretRef{}) || cfg.Chat.Telegram.TokenEnv != "" {
		b.webRouter.Restart = func(context.Context) error {
			if b.restartTelegram == nil {
				return fmt.Errorf("telegram gateway is not ready")
			}
			return b.restartTelegram()
		}
	}
	b.webRouter.Models = b.chatModels
	if b.updateService != nil {
		b.webRouter.Updates = b.updateService
	}
	b.webRouter.Personas = b.personas
	b.webRouter.InitSessions(b.chatSessionStore)
	configureTaskCommands(b.webRouter, b.chatTasks, b.chatController, chatTaskListerAdapter{tasks: b.st.Tasks}, b.defaultChatIdentity)
	webSetup := telegramSetup{
		Cfg: config.NewHolder(cfg), St: b.st, LLM: b.llm, ChatModels: b.chatModels, ToolReg: b.toolReg,
		Personas: b.personas, ChatTasks: b.chatTasks, ChatController: b.chatController,
		ChatTaskLister: chatTaskListerAdapter{tasks: b.st.Tasks},
		ChatTaskLogs: chatTaskLogReaderAdapter{
			tasks:    b.st.TaskByID,
			taskLogs: b.taskLogs,
		},
		ChatTaskActor: chatTaskActorAdapter{
			tasks:   b.st.TaskByID,
			handler: b.web.Handler(),
			token:   func() string { return b.web.Token },
		},
		DefaultChatIdentity: b.defaultChatIdentity, SessionStore: b.chatSessionStore,
		Bus: b.bus, Log: log, Secrets: b.secrets,
	}
	b.webRouter.LLM, b.webRouter.LLMStream = makeChatLLMResponder(ctx, "web", webSetup, b.chatSessionStore, b.webRouter)
	b.webRouter.Titles = newChatTitleGenerator(webSetup)
	b.webRouter.Log = log
	b.web.Chat = &webui.ChatService{
		Router: b.webRouter, Sessions: b.chatSessionStore,
		Turns:  gateway.NewTurns(log),
		Models: b.chatModels, Personas: b.personas,
	}
	if b.updateService != nil {
		b.web.Chat.Updates = b.updateService
	}

	// Operator readiness probes: wired once the chat surface exists so the
	// gateway probe can read the real session store and model manager.
	b.setupReadinessProbes()
}

// setupGateways assembles the Telegram, email and webhook gateways. It
// returns false when the Telegram gateway could not start, which the
// caller treats as a fatal boot error.
//
// Multi-agent collaboration PRD phase C (docs/prds/multi-agent-collaboration.md).
func (b *boot) setupGateways(ctx context.Context, cfgPath, overlayPath string) bool {
	cfg, log := b.cfg, b.log
	start, ok := setupTelegramGateway(ctx, telegramSetup{
		Cfg: config.NewHolder(cfg), CfgPath: cfgPath, OverlayPath: overlayPath,
		St: b.st, LLM: b.llm, ChatModels: b.chatModels, ToolReg: b.toolReg,
		Personas: b.personas, ChatTasks: b.chatTasks, ChatController: b.chatController,
		ChatTaskLister: chatTaskListerAdapter{tasks: b.st.Tasks},
		ChatTaskLogs: chatTaskLogReaderAdapter{
			tasks:    b.st.TaskByID,
			taskLogs: b.taskLogs,
		},
		ChatTaskActor: chatTaskActorAdapter{
			tasks:   b.st.TaskByID,
			handler: b.web.Handler(),
			token:   func() string { return b.web.Token },
		},
		DefaultChatIdentity: b.defaultChatIdentity, SessionStore: b.chatSessionStore, Updates: b.updateService,
		Secrets:         b.secrets,
		Bus:             b.bus,
		RegisterRestart: func(request func() error) { b.restartTelegram = request }, Log: log,
		ChannelManager: b.channelManager, AgentStatus: b.agentStatus,
	})
	if !ok {
		return false
	}
	if start != nil {
		b.startGateways = append(b.startGateways, start)
	}

	// ── Email gateway (optional) ───────────────────────────────────
	if cfg.Chat.Email.ListenAddr != "" {
		em := email.New(cfg.Chat.Email.ListenAddr, cfg.Chat.Email.RelayAddr, log)
		emRouter := gateway.NewRouter(b.st, nil, "email")
		configureTaskCommands(emRouter, b.chatTasks, b.chatController, chatTaskListerAdapter{tasks: b.st.Tasks}, b.defaultChatIdentity)
		b.startGateways = append(b.startGateways, func() {
			go func() {
				lifecycle := gateway.Lifecycle{
					Starting: func() { b.channelManager.MarkStarting("email") },
					Running: func() {
						b.channelManager.MarkRunning("email")
						log.Info("email gateway started", "addr", cfg.Chat.Email.ListenAddr)
					},
				}
				if err := em.Start(ctx, emRouter, lifecycle); err != nil && ctx.Err() == nil {
					b.channelManager.MarkFailed("email", err.Error())
					log.Error("email gateway stopped", "err", err)
				}
			}()
		})
	}

	// ── Webhook gateway (optional) ─────────────────────────────────
	// Enabled when chat.webhook is set to a host:port listen address.
	if cfg.Chat.WebhookAddr != "" {
		host, port := parseListenAddr(cfg.Chat.WebhookAddr, "0.0.0.0", 8644)
		wh := webhook.New(
			host, port,
			[]webhook.RouteConfig{{Path: "/webhook"}},
			log,
		)
		whRouter := gateway.NewRouter(b.st, nil, "webhook")
		configureTaskCommands(whRouter, b.chatTasks, b.chatController, chatTaskListerAdapter{tasks: b.st.Tasks}, b.defaultChatIdentity)
		b.startGateways = append(b.startGateways, func() {
			go func() {
				lifecycle := gateway.Lifecycle{
					Starting: func() { b.channelManager.MarkStarting("webhook") },
					Running: func() {
						b.channelManager.MarkRunning("webhook")
						log.Info("webhook gateway started", "addr", fmt.Sprintf("%s:%d", host, port))
					},
				}
				if err := wh.Start(ctx, whRouter, lifecycle); err != nil && ctx.Err() == nil {
					b.channelManager.MarkFailed("webhook", err.Error())
					log.Error("webhook gateway stopped", "err", err)
				}
			}()
		})
	}
	return true
}

// loadWorkflows builds the workflow registry from the skill catalog.
// Plugin-defined workflows override built-ins of the same name;
// built-ins fill gaps.
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

	skillsBase := cfg.SkillsDir
	if skillsBase == "" {
		skillsBase = cfg.WorkDir
	}
	workflowCatalog, err := skillbuild.BuildCatalog(skillsBase)
	if err != nil {
		log.Error("skill registry build failed", "err", err)
		return err
	}
	log.Info("workflow registry built", "workflows", len(workflowCatalog.Registry))
	b.web.Workflows = workflow.DefinitionsWithOrigins(workflowCatalog.Registry, workflowCatalog.Origins)
	if l := cfg.Web.Listen; l != "" && l != "off" {
		b.web.Token = webTokenFor(l, cfg.DBPath, log)
		go func() {
			if err := b.web.Run(ctx, l); err != nil {
				log.Error("web ui failed", "err", err)
			}
		}()
	}
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
// single workflow-kind action with CEL when conditions. Loaded at startup
// with the same reject-at-load rule -- any malformed playbook,
// multi-action playbook, non-workflow position, or when compile failure
// aborts startup, matching the routing-file load pattern (not
// degrade-and-skip). A nonexistent dir is an empty store. Split out of
// loadWorkflows (t2db.16); pure extraction, same log messages as before.
func (b *boot) loadEDAPlaybooks(cfg config.Config, log *slog.Logger) error {
	var err error
	b.playbooks, err = playbook.Load(cfg.EDAPlaybookDir)
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

// buildTreesAndIdentities composes the worktree manager and the
// multi-identity runners. Each configured identity gets its own forge
// client (its own token, possibly its own forge type/host) and its own
// worktree manager (a distinct WorkDir so concurrent identities never
// collide on the same clone). When cfg.Identities is empty,
// Daemon.Identities stays nil and Run() takes the single-identity path
// unchanged.
func (b *boot) buildTreesAndIdentities(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	b.trees = &worktree.Manager{
		WorkDir:  cfg.WorkDir,
		Token:    b.token,
		BotUser:  cfg.BotUser,
		BotEmail: cfg.BotEmail,
		BaseURL:  cfg.Forge.Host,
	}

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
	unsubscribe, err := registerTaskRPCServers(coreConn, b.st, b.forgeClient, b.trees, b.identityRunners, b.worktreeGrants, log)
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
	return nil
}

// setupMemory starts the memory manager. The built-in file-backed
// provider (MEMORY.md + USER.md) lives under the daemon work directory.
// memory.Manager.RegisterExternal exists for an external provider, but
// nothing calls it here: no external MemoryProvider is implemented
// anywhere in the repo to construct and register, and
// configuration.validateMemory now rejects cfg.Memory.Provider being set
// at all rather than silently accepting a value with no effect
// (archie-core-1786637499161-356-e424e40d.1).
func (b *boot) setupMemory() error {
	cfg, log := b.cfg, b.log
	memProvider, memDir := memoryProvider(cfg.WorkDir, log)
	if memProvider == nil {
		log.Error("memory provider init failed", "dir", memDir)
		return fmt.Errorf("memory provider init failed in %s", memDir)
	}
	memManager, err := memory.NewManager(memProvider, nil)
	if err != nil {
		log.Error("memory manager init failed", "err", err)
		return err
	}
	b.memManager = memManager
	// The dashboard is built before memory exists, so it is wired in here
	// rather than at construction.
	b.web.Memory = memManager

	if err := memManager.Initialize("daemon"); err != nil {
		log.Warn("memory manager initialize", "err", err)
	}
	mm := memManager
	b.addCleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := mm.ShutdownContext(shutdownCtx); err != nil {
			log.Error("memory manager shutdown", "err", err)
		}
	})
	log.Info("memory manager started", "dir", memDir)
	return nil
}

// setupMemoryEngine wires the domain/memory engine family
// (archie-core-1786637499161-356-e424e40d), separate from and not yet
// consulted by the legacy memory manager set up in setupMemory above.
// cfg.Memory.Engine is validated at config load
// (configuration.validateMemory) against the same set this switch covers,
// so an unrecognised value cannot reach here -- this only guards against
// the two lists drifting apart.
//
// Rooted at workDir/memory-engine, a directory separate from the legacy
// provider's workDir/memory, so the two paths can never collide on the
// same files while both exist.
func (b *boot) setupMemoryEngine() error {
	cfg, log := b.cfg, b.log
	registry := domainmemory.NewRegistry(domainmemory.Registrar{})

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

// setupMemoryAll runs both memory setup phases -- the legacy manager and
// the new engine family -- as one step. Kept as a single call from Run()
// rather than two: they are always run together and Run() is already at
// its cyclomatic complexity budget.
func (b *boot) setupMemoryAll() error {
	if err := b.setupMemory(); err != nil {
		return err
	}
	return b.setupMemoryEngine()
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
		// b.memEngines (*domainmemory.Registry) satisfies
		// curator.MemoryEngineSource's Get(name) signature directly, no
		// adapter needed. Set by setupMemoryAll, which Run() calls before
		// setupCurators.
		MemoryEngines: b.memEngines,
		// Skills is a shared host service like Events/MemoryEngines --
		// registry.filter narrows it out of the view for any curator that
		// doesn't declare Manifest.Skills, per curator not per instance.
		Skills: skillcurator.NewStore(skillsRoot),
		// Conversations backs the session-memory curator; b.chatSessionStore
		// is set by setupLLMAndChat, which Run() calls before setupCurators.
		Conversations: sessioncurator.NewAdapter(b.chatSessionStore, b.cfg.BotUser),
		LLM:           curatorLLMRunner{rt: b.llm},
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

// registerTools registers the tool providers with a lifecycle: memory,
// workspace file/shell tools and optional MCP servers.
func (b *boot) registerTools() error {
	cfg, log := b.cfg, b.log
	b.providerRegistry = toolprovider.NewRegistry(b.toolReg)
	if err := b.providerRegistry.Register(memorytoolprovider.New(b.memManager)); err != nil {
		log.Error("memory tool provider registration failed", "err", err)
		return err
	}
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

	apiKey, err := cfg.Tools.Minimax.APIKey.Resolve(b.secrets)
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
	cfg, log := b.cfg, b.log
	b.d = &daemon.Daemon{
		Cfg:            config.NewHolder(cfg),
		ConnectedNATS:  daemon.NATSEndpoint{URL: b.natsURL, Token: b.natsToken},
		Store:          b.st,
		Bus:            b.bus,
		Forge:          b.forgeClient,
		Trees:          b.trees,
		CapabilityHost: b.capabilityHost,
		Storage:        b.storeBackend,
		Log:            log,
		Tasks:          b.natsClient,
		WorktreeGrants: b.worktreeGrants,
		ContainerPool:  b.containerPool,
		Guardrails:     b.guardrails,
		ToolRegistry:   b.toolReg,
		Curators:       b.curatorRegistry,
		Identities:     b.identityRunners,
		TaskLogs:       b.taskLogs,
		AgentStatus:    b.agentStatus,
	}
	b.web.TaskStopper = b.d
	// Curator observability (archie-core-1786637489932-6): GET
	// /api/curators reads registered names, health and recent activity
	// straight off the live registry the daemon already holds.
	b.web.Curators = b.curatorRegistry
	// web and the daemon must share ONE Holder: a reload swaps d.Cfg and
	// the dashboard reads the running config from the same snapshot. The
	// webui Holder seeded in the literal above is replaced here; after
	// this point /api/config and the daemon can never disagree about the
	// published config.
	b.web.Cfg = b.d.Cfg
	if ms, ok := b.st.(store.MappingStore); ok {
		b.d.Mappings = ms
	}
	if bs, ok := b.st.(store.BindingStore); ok {
		b.d.Bindings = bs
	}
	if bd, ok := b.st.(store.BindingDispatcher); ok {
		b.d.BindingDispatcher = bd
	}
	if btc, ok := b.st.(store.BindingTaskCreator); ok {
		b.d.BindingTaskCreator = btc
	}
	b.setupForgeWebhook()
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
	secretValue, err := cfg.Forge.WebhookSecret.Resolve(b.secrets)
	if err != nil || secretValue == "" {
		log.Error("forge webhook disabled: secret unavailable",
			"engine", cfg.Forge.WebhookSecret.Engine, "key", cfg.Forge.WebhookSecret.Key, "err", err)
		return
	}
	receiver := forgewebhook.New(secretValue, cfg.Dispatch.Trigger, cfg.Label, cfg.BotUser, b.d.PublishTask, log)
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

// publishConfig publishes a config snapshot and its provenance to both
// the daemon and the dashboard (they share one Holder). Reload and the
// PATCH path both go through it, so the two can never diverge.
// currentProvenance (an atomic) is the last published provenance chain;
// the PATCH path appends the runtime-overlay origin to it.
func (b *boot) publishConfig(cfg config.Config, provenance configuration.Provenance) {
	b.d.Cfg.Set(cfg)
	origins := make([]webui.ConfigOrigin, 0, len(provenance.Origins))
	for _, origin := range provenance.Origins {
		origins = append(origins, webui.ConfigOrigin{
			Path: origin.Path, Role: string(origin.Role), Layer: string(origin.Layer), Feature: string(origin.Feature),
		})
	}
	b.web.SetProvenance(origins)
}

// wireConfigPublishing installs SIGHUP reload and the reload status
// surface. The reload log warns about changed fields that require a
// restart, so an operator never has to read the source to find out
// whether their edit took effect. web.LastReload exposes the outcome to
// /api/config.
func (b *boot) wireConfigPublishing(ctx context.Context, cfgPath, overlayPath string) {
	log := b.log
	reloadController := newReloadController(b.loader, cfgPath, overlayPath, func(doc *configuration.Document) {
		// Boot merges catalog-discovered providers into cfg before the
		// Holders are seeded; publishing the raw reloaded document would
		// drop them from the running config even though the file is
		// unchanged. Re-apply the same merge (idempotent: a catalog that
		// failed to load merges to identity).
		applyModelCatalog(&doc.Config, b.catalog)
		old := b.d.Cfg.Get()
		b.currentProvenance.Store(&doc.Provenance)
		b.publishConfig(doc.Config, doc.Provenance)
		if fields := changedNonReloadableFields(old, doc.Config); len(fields) > 0 {
			log.Warn("config reloaded; some changes require a restart",
				"fields", fields, "paths", doc.Provenance.Paths())
		} else {
			log.Info("config reloaded", "paths", doc.Provenance.Paths())
		}
	})
	if b.overlayStore != nil {
		reloadController.WithOverlay(func() (map[string]any, error) { return b.overlayStore.Snapshot(ctx) })
	}
	// LastReload merges the reload controller's outcome with the boot-time
	// overlay degrade, so /api/config carries both the last reload result
	// and whether the runtime overlay is in effect at all.
	b.web.LastReload = func() config.ReloadStatus {
		st := reloadController.Status()
		if p := b.bootOverlayErr.Load(); p != nil {
			st.OverlayUnavailable = *p
		}
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

// installUpdateConfigHandler installs the dashboard's PATCH /api/config
// handler. It applies updates to a deep copy of the published config,
// validates the materialised result, persists to the overlay, then
// publishes through the same path as reload. It must never call Set
// directly from the handler -- web.Cfg is the daemon's own Holder -- and
// it decodes into a Clone so a failed validation cannot mutate the
// published snapshot's shared maps.
func (b *boot) installUpdateConfigHandler() {
	log := b.log
	b.web.UpdateConfig = func(ctx context.Context, updates map[string]any) error {
		if b.overlayStore == nil {
			return fmt.Errorf("%w: config overlay is disabled (--no-config-overlay or ARCHIE_SKIP_CONFIG_OVERLAY=1)", webui.ErrConfigUpdateUnavailable)
		}
		for key := range updates {
			if reason, denied := overlay.DeniedKeys[key]; denied {
				return fmt.Errorf("%w: config key %s is not runtime-tunable: %s", webui.ErrConfigUpdateInvalid, key, reason)
			}
		}
		next := b.d.Cfg.Get().Clone()
		if err := applyDottedOverlay(&next, updates); err != nil {
			return fmt.Errorf("%w: %w", webui.ErrConfigUpdateInvalid, err)
		}
		if err := configuration.Validate(&next); err != nil {
			return fmt.Errorf("%w: %w", webui.ErrConfigUpdateInvalid, err)
		}
		for key, value := range updates {
			data, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("%w: %w", webui.ErrConfigUpdateInvalid, err)
			}
			if err := b.overlayStore.Set(ctx, key, string(data), "dashboard"); err != nil {
				return err
			}
		}
		// Provenance gets the runtime-overlay origin, replacing any prior
		// one so the chain does not grow unboundedly across edits.
		var kept []configuration.Origin
		for _, origin := range b.currentProvenance.Load().Origins {
			if origin.Path == "config_overlay (runtime)" {
				continue
			}
			kept = append(kept, origin)
		}
		prov := configuration.Provenance{Origins: append(kept,
			configuration.Origin{Path: "config_overlay (runtime)", Role: configuration.RoleMain, Layer: configuration.LayerOverlay})}
		b.currentProvenance.Store(&prov)
		b.publishConfig(next, prov)
		b.bootOverlayErr.Store(nil) // a successful write proves the overlay works again
		log.Info("config updated from dashboard", "keys", len(updates))
		return nil
	}
}

// installUpdateRepoFieldHandler installs the dashboard's PATCH
// /api/config/repos/{owner}/{name} handler (archie-core-b6ew.6). A
// repository is one element of a []config.Repo, not a flat dotted key, so
// this cannot go through UpdateConfig's dotted-key path directly (overlay's
// Nest/ApplyOverlayValues have no notion of "element N of this slice").
// Instead it reads the daemon's own live, untrimmed Repos (never
// webui.RepoView, which omits fields like Preflight/TestGlob and would
// silently drop them on a naive round-trip), changes the one field on the
// matching repo, and republishes the *entire* repository list through
// UpdateConfig -- reusing its validate-persist-publish path unchanged, so
// this handler owns only "which repo, which field," never how an update is
// applied. An edit here therefore overrides the whole repos list in the
// runtime overlay, same granularity every other overlay key already has;
// [[repos]] file edits stay visible for every field this handler never
// touches until an operator resets the "repos" override.
func (b *boot) installUpdateRepoFieldHandler() {
	b.web.UpdateRepoField = func(ctx context.Context, owner, name, field string, value any) error {
		repos, err := applyRepoFieldUpdate(b.d.Cfg.Get().Clone().Repos, owner, name, field, value)
		if err != nil {
			return err
		}
		return b.web.UpdateConfig(ctx, map[string]any{"repos": repos})
	}
}

// installConfigHandlers installs the dashboard's config override
// listing and per-row reset handlers, both going through the same
// publish path as reload.
func (b *boot) installConfigHandlers(cfgPath, overlayPath string) {
	log := b.log
	// ConfigOverrides lists the dotted keys currently overridden by the
	// runtime overlay, so the dashboard can mark those rows and offer a
	// reset. A disabled store reports no overrides.
	b.web.ConfigOverrides = func(ctx context.Context) ([]string, error) {
		if b.overlayStore == nil {
			return nil, nil
		}
		rows, err := b.overlayStore.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(rows))
		for k := range rows {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, nil
	}

	// ResetConfig deletes one overlay row and republishes file + remaining
	// overlay, so a dashboard-created override can be removed without
	// editing SQL -- and so an operator whose file edit is shadowed by an
	// override can recover it with one click. The file is resolved and
	// the target state validated BEFORE the row is deleted, so a broken
	// file cannot leave the store and the running config disagreeing.
	b.web.ResetConfig = func(ctx context.Context, key string) error {
		if b.overlayStore == nil {
			return fmt.Errorf("%w: config overlay is disabled", webui.ErrConfigUpdateUnavailable)
		}
		overrides, err := b.overlayStore.Snapshot(ctx)
		if err != nil {
			return err
		}
		delete(overrides, key) // the target overlay state after the reset
		doc, err := b.loader.Resolve(cfgPath, overlayPath)
		if err != nil {
			return fmt.Errorf("%w: %w", webui.ErrConfigUpdateInvalid, err)
		}
		if len(overrides) > 0 {
			if doc, err = b.loader.ApplyOverlay(doc, overrides); err != nil {
				return fmt.Errorf("%w: %w", webui.ErrConfigUpdateInvalid, err)
			}
		}
		if err := b.overlayStore.Delete(ctx, key); err != nil {
			return err
		}
		applyModelCatalog(&doc.Config, b.catalog)
		b.currentProvenance.Store(&doc.Provenance)
		b.publishConfig(doc.Config, doc.Provenance)
		b.bootOverlayErr.Store(nil)
		log.Info("config key reset to file value", "key", key)
		return nil
	}
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
	if once {
		b.d.Cycle(ctx)
		return nil
	}
	b.log.Info("archied running", "repos", len(b.cfg.Repos), "poll", b.cfg.PollInterval.Std().String(), "label", b.cfg.Label)
	// Drain monitoring only matters while the daemon stays up to be drained;
	// a --once cycle exits immediately, so a drain that arrives mid-cycle is
	// out of scope and the monitor is not started.
	b.startDrainMonitor(ctx)
	if err := b.d.Run(ctx); err != nil && ctx.Err() == nil {
		b.log.Error("daemon exited", "err", err)
		return err
	}
	return nil
}
