// Command archied is the archie orchestrator daemon: it watches GitHub
// for issues labelled for archie, works each one in an isolated
// worktree through its routed workflow, and opens pull requests for
// human review.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	natsio "github.com/nats-io/nats.go"
	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/agentexec"
	channelruntime "github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/email"
	"github.com/samcharles93/archie-core/internal/channels/webhook"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/domain/workflow/wfeval"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/overlay"
	"github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/memory/builtin"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	builtintoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/builtin"
	mcptoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/mcp"
	memorytoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/memory"
	"github.com/samcharles93/archie-core/internal/tools/webfetch"
	"github.com/samcharles93/archie-core/internal/webui"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

func main() {
	// The codesearch helper is dispatched before the daemon's flags are
	// parsed: it is this same binary re-invoked as a short-lived child by
	// internal/indexing, and it must not touch config, the store or NATS.
	if args := os.Args[1:]; isCodesearchHelperArgs(args) {
		os.Exit(runCodesearchHelper(args[1:], os.Stdout))
	}
	os.Exit(run())
}

const (
	// defaultChatMaxSteps bounds the model/tool round-trips in one chat
	// turn when [config.ChatConfig.MaxSteps] is unset.
	defaultChatMaxSteps          = 100
	packagedGatewayChangelogPath = "/usr/share/archie/CHANGELOG.archied.md"
	packagedRuntimeChangelogPath = "/usr/share/archie/CHANGELOG.archie.md"
)

// Component versions are injected from their independent release tags.
// Components without a release tag remain "dev" and never generate upgrade
// notifications.
var (
	gatewayVersion = "dev"
	runtimeVersion = "dev"
)

// chatGenerateOptions builds one chat turn's request.
func chatGenerateOptions(
	ctx context.Context,
	messages []chat.Message,
	registry *tools.Registry,
	maxSteps int,
	limits agentexec.ToolLimits,
	extra []tools.ToolEntry,
) (core.GenerateOptions, error) {
	toolOpts := limits.Options()
	if approval := gateway.ApprovalFromContext(ctx); approval != nil {
		toolOpts.Approval = approval
	}
	toolSet, err := agentexec.BuildToolSet(registry, toolOpts)
	if err != nil {
		return core.GenerateOptions{}, err
	}
	extraSet, err := agentexec.BuildToolSetFrom(extra, toolOpts)
	if err != nil {
		return core.GenerateOptions{}, err
	}
	maps.Copy(toolSet, extraSet)
	if maxSteps <= 0 {
		maxSteps = defaultChatMaxSteps
	}
	return core.GenerateOptions{
		Messages: messages,
		Tools:    toolSet,
		MaxSteps: maxSteps,
	}, nil
}

// isWithin reports whether target sits inside base.
func isWithin(base, target string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// toolLimits reads the configured per-turn result limits.
func toolLimits(cfg config.Config) agentexec.ToolLimits {
	return agentexec.ToolLimits{
		MaxResultChars:  cfg.Tools.Policy.MaxResultChars,
		TurnBudgetChars: cfg.Tools.Policy.TurnBudgetChars,
		SpillDir:        cfg.Tools.Policy.SpillDir,
	}
}

// taskListOverRead multiplies the requested limit when reading rows that are
// filtered by identity afterwards.
const taskListOverRead = 5

// chatTaskListerAdapter gives the gateway a read view of one identity's tasks.
// gateway deliberately does not import internal/store, so the projection
// happens here, as it does for the writer and controller adapters below.
type chatTaskListerAdapter struct {
	tasks func(context.Context, int) ([]store.Task, error)
}

func (a chatTaskListerAdapter) ListChatTasks(ctx context.Context, identity string, limit int) ([]gateway.ChatTaskSummary, error) {
	// Over-read before filtering: Tasks applies its limit across the whole
	// table, so asking for exactly `limit` would return fewer than that for
	// this identity whenever another identity's work is more recent.
	rows, err := a.tasks(ctx, limit*taskListOverRead)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.ChatTaskSummary, 0, limit)
	for _, task := range rows {
		if task.Identity != identity {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, gateway.ChatTaskSummary{
			ID:       task.ID,
			Repo:     task.Owner + "/" + task.Repo,
			Title:    task.Title,
			Status:   task.Status,
			Workflow: task.Workflow,
			PRNumber: task.PRNumber,
		})
	}
	return out, nil
}

func configuredMCPProvider(server config.MCPServer) (toolprovider.Engine, error) {
	name := strings.TrimSpace(server.Name)
	if name == "" {
		return nil, fmt.Errorf("MCP server name is required")
	}
	transportType := strings.ToLower(strings.TrimSpace(server.Transport))
	if transportType == "" {
		transportType = "stdio"
	}

	switch transportType {
	case "stdio":
		command := strings.TrimSpace(server.Command)
		if command == "" {
			return nil, fmt.Errorf("MCP stdio server %q requires a command", name)
		}
		transport := mcp.NewStdioTransport(mcp.StdioTransportConfig{
			Command: command,
			Args:    append([]string(nil), server.Args...),
			Dir:     server.WorkDir,
		})
		return mcptoolprovider.New(name, transport), nil

	case "http", "streamablehttp":
		url := strings.TrimSpace(server.URL)
		if url == "" {
			return nil, fmt.Errorf("MCP http server %q requires a url", name)
		}
		transport := mcp.NewHTTPTransport(mcp.HTTPTransportConfig{
			Endpoint: url,
			Headers:  server.Headers,
		})
		return mcptoolprovider.New(name, transport), nil

	case "sse":
		sseEndpoint := strings.TrimSpace(server.SSEEndpoint)
		if sseEndpoint == "" {
			return nil, fmt.Errorf("MCP sse server %q requires an sse_endpoint", name)
		}
		transport := mcp.NewSSETransport(mcp.SSETransportConfig{
			SSEEndpoint:     sseEndpoint,
			MessageEndpoint: strings.TrimSpace(server.MessageEndpoint),
			Headers:         server.Headers,
		})
		return mcptoolprovider.New(name, transport), nil

	default:
		return nil, fmt.Errorf("MCP transport %q is not supported", transportType)
	}
}

// resolveForge builds the forge client for one forge configuration, returning
// the resolved token alongside it for the worktree manager.
//
// A missing or unusable credential disables the forge; it does not stop the
// daemon. The forge is one feature among many, and killing the process denies
// the operator chat, the gateway and every other subsystem over a capability
// they may not use at all -- under a systemd unit with Restart=on-failure that
// becomes a crash loop where the error scrolls past unread. A malformed
// configuration is a different matter and still fails fast in validation, well
// before this point.
func resolveForge(cfg config.Forge, secrets *secret.Registry, log *slog.Logger) (forge.Forge, string) {
	if isForgeDisabled(cfg.Type) {
		return forge.NewNoop(log), ""
	}
	token, err := cfg.Token.Resolve(secrets)
	if err != nil || token == "" {
		log.Warn("forge disabled: token unavailable",
			"forge_type", cfg.Type,
			"engine", cfg.Token.Engine,
			"key", cfg.Token.Key,
			"err", err)
		return forge.NewNoop(log), ""
	}
	client, err := forge.New(cfg.Type, token, cfg.Host, log)
	if err != nil {
		log.Warn("forge disabled: client construction failed",
			"forge_type", cfg.Type, "err", err)
		return forge.NewNoop(log), ""
	}
	return client, token
}

// isForgeDisabled reports whether a forge type explicitly opts out of forge
// integration. It mirrors the identically named check in the configuration
// package, which is unexported there.
func isForgeDisabled(forgeType string) bool {
	switch forgeType {
	case "none", "off", "disabled":
		return true
	default:
		return false
	}
}

func openProductionTaskStore(ctx context.Context, path string) (store.TaskStore, error) {
	return store.Open(ctx, path)
}

func taskDBPath(configuredPath string) string {
	return configuredPath + "-tasks.sqlite"
}

// configDBPath resolves the runtime config overlay file. It is a sibling
// of the task and conversation stores (same configured path, own suffix)
// so each file owns its user_version and its migrator with no
// contention; recovery from a broken overlay is rm this file plus the
// --no-config-overlay boot flag.
func configDBPath(configuredPath string) string {
	return configuredPath + "-config.sqlite"
}

func manualRequeueTask(ctx context.Context, st store.TaskStore, taskID int64) error {
	task, err := st.TaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	switch task.Status {
	case store.StatusParked, store.StatusWaitingHuman:
		return st.Requeue(ctx, taskID, task.Status, "")
	default:
		return fmt.Errorf("task %d has status %q; only parked or waiting_human tasks can be requeued", taskID, task.Status)
	}
}

func run() int {
	defaultCfg := filepath.Join(configHome(), "archie", "config.toml")
	cfgPath := flag.String("config", defaultCfg, "path to a TOML/YAML config file or configuration directory")
	overlayPath := flag.String("config-overlay", "", "path to a TOML/YAML overlay file or configuration directory applied on top of -config")
	noConfigOverlay := flag.Bool("no-config-overlay", false, "skip the runtime config overlay (recovery hatch for the DB overlay; the -config-overlay file overlay still applies)")
	once := flag.Bool("once", false, "run a single poll+process cycle and exit (systemd timer / testing)")
	requeue := flag.Int64("requeue", 0, "requeue a parked/waiting task by id (keeps its workflow), then exit unless -once is also set")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	loader := configuration.New(log)
	skipOverlay := *noConfigOverlay || configuration.SkipOverlay()
	if skipOverlay {
		log.Info("runtime config overlay disabled (recovery hatch); booting on file config alone")
	}
	doc, err := loader.Resolve(*cfgPath, *overlayPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Runtime config overlay: dashboard-edited overrides layered over the
	// file config from their own SQLite file (own user_version and
	// migrator, no contention with the task store). Skipped under
	// --no-config-overlay / ARCHIE_SKIP_CONFIG_OVERLAY=1, or when the
	// store cannot be opened or its values fail validation -- a broken
	// overlay must not brick the daemon. bootOverlayErr carries the
	// degrade reason into /api/config's reload status so it is visible
	// where the operator is looking, not only in logs.
	var overlayStore *overlay.Store
	var bootOverlayErr atomic.Pointer[string]
	if !skipOverlay {
		overlayStore, err = overlay.Open(ctx, configDBPath(doc.Config.DBPath))
		if err != nil {
			reason := err.Error()
			bootOverlayErr.Store(&reason)
			log.Error("config overlay unavailable; booting on file config alone", "path", configDBPath(doc.Config.DBPath), "err", err)
		} else {
			defer overlayStore.Close()
			if overrides, err := overlayStore.Snapshot(ctx); err != nil {
				reason := err.Error()
				bootOverlayErr.Store(&reason)
				log.Error("config overlay unreadable; booting on file config alone", "err", err)
			} else if len(overrides) > 0 {
				applied, err := loader.ApplyOverlay(doc, overrides)
				if err != nil {
					reason := err.Error()
					bootOverlayErr.Store(&reason)
					log.Error("config overlay rejected by validation; booting on file config alone", "err", err)
				} else {
					doc = applied
				}
			}
		}
	}
	cfg := doc.Config
	var currentProvenance atomic.Pointer[configuration.Provenance]
	currentProvenance.Store(&doc.Provenance)

	// Re-create the logger now the config is known. Everything above this
	// point logs to stderr only, which is unavoidable: the log destination is
	// itself configuration. A file that cannot be opened is reported and the
	// daemon continues on stderr -- losing the durable copy must not take the
	// daemon down with it.
	logFeed := logging.NewFeed(1000)
	// Task logs live alongside the store rather than under cfg.Log.File's
	// directory: cfg.Log.File is optional (file logging can be off), while
	// DBPath is required for the daemon to run at all, so it is the more
	// reliable anchor for "where archie keeps its state" on this host.
	taskLogs := logging.NewTaskRegistry(filepath.Join(filepath.Dir(cfg.DBPath), "logs", "tasks"), logFeed, logging.TaskSinkOptions{})
	fileLog, logCloser, logErr := logging.New(logging.Options{
		File:      cfg.Log.File,
		MaxSizeMB: cfg.Log.MaxSizeMB,
		Keep:      cfg.Log.Keep,
		Level:     cfg.Log.Level,
		Stderr:    !cfg.Log.Quiet,
		Feed:      logFeed,
	})
	log = fileLog
	defer func() { _ = logCloser.Close() }()
	if logErr != nil {
		log.Error("file logging disabled", "err", logErr)
	} else if cfg.Log.File != "" {
		log.Info("logging to file", "path", cfg.Log.File)
	}

	secrets, err := configuredSecretRegistry(&cfg, log)
	if err != nil {
		log.Error("configure secrets", "err", err)
		return 1
	}
	forgeClient, token := resolveForge(cfg.Forge, secrets, log)

	st, err := openProductionTaskStore(ctx, taskDBPath(cfg.DBPath))
	if err != nil {
		log.Error("open store", "err", err)
		return 1
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("close store", "err", err)
		}
	}()
	chatSessionStore, err := makeTelegramSessionStore(cfg)
	if err != nil {
		log.Error("open conversation store", "path", conversationDBPath(cfg.DBPath), "err", err)
		return 1
	}
	defer func() {
		if err := chatSessionStore.Close(); err != nil {
			log.Error("close conversation store", "err", err)
		}
	}()

	if *requeue > 0 {
		if err := manualRequeueTask(ctx, st, *requeue); err != nil {
			log.Error("requeue failed", "task", *requeue, "err", err)
			return 1
		}
		log.Info("task requeued", "task", *requeue)
		if !*once {
			return 0
		}
	}

	catalog, err := modelcatalog.Load(ctx, modelcatalog.Options{
		CachePath:  filepath.Join(filepath.Dir(*cfgPath), "models.json"),
		Getenv:     secrets.Getenv,
		Configured: cfg.Providers,
	})
	var catalogModels []string
	if err != nil {
		log.Warn("model catalog unavailable; using configured providers and models", "err", err)
	} else {
		catalogModels = applyModelCatalog(&cfg, catalog)
		log.Info("model catalog loaded", "providers", len(catalog.Providers), "models", len(catalogModels))
	}

	// Observability: every event is logged to SQLite (stamped with its
	// row id) and then fanned out to live dashboard connections.
	bus := events.NewBus()
	defer bus.Close()
	var restartTelegram func() error
	telegramDetail := ""
	if cfg.Chat.Telegram.TokenEnv != "" && len(cfg.Chat.Telegram.AllowedUserIDs) == 0 {
		telegramDetail = "Token set, but the allowlist is empty -- the bot answers nobody."
	}
	channelManager := channelruntime.NewManager([]channelruntime.Descriptor{
		{ID: "telegram", Name: "Telegram", Configured: cfg.Chat.Telegram.TokenEnv != "", ReloadSupported: cfg.Chat.Telegram.TokenEnv != "", Detail: telegramDetail},
		{ID: "email", Name: "Email", Configured: cfg.Chat.Email.ListenAddr != ""},
		{ID: "webhook", Name: "Webhook gateway", Configured: cfg.Chat.WebhookAddr != ""},
	})
	configProvenance := make([]webui.ConfigOrigin, 0, len(doc.Provenance.Origins))
	for _, origin := range doc.Provenance.Origins {
		configProvenance = append(configProvenance, webui.ConfigOrigin{
			Path: origin.Path, Role: string(origin.Role), Layer: string(origin.Layer), Feature: string(origin.Feature),
		})
	}
	web := &webui.Server{Store: st, Log: log, LogFeed: logFeed, TaskLogs: taskLogs, Cfg: config.NewHolder(cfg), Channels: channelManager, Events: bus}
	web.SetProvenance(configProvenance)
	web.ReloadChannel = func(ctx context.Context, id string) error {
		if id != "telegram" || restartTelegram == nil {
			return fmt.Errorf("channel reload unavailable")
		}
		return restartTelegram()
	}
	// The dashboard closes the forge issue behind a rejected task, or the
	// issue is re-polled and the work is done again. It gets only that one
	// method, not the forge client.
	web.Issues = forgeClient
	sink := bus.Subscribe(256)
	go persistAndBroadcastEvents(sink, st, web, log)
	// NATS client (optional  --  SQLite flow unchanged when [nats] is absent).
	var natsClient *nats.Client
	if cfg.NATS.URL != "" {
		natsToken, err := configuredNATSToken(cfg.NATS, os.Getenv)
		if err != nil {
			log.Error("nats credentials", "err", err)
			return 1
		}
		// Composition owns the subject list: the bus must not know which
		// subjects belong to which domain.
		natsClient, err = nats.Connect(ctx, nats.Config{
			URL:           cfg.NATS.URL,
			Token:         natsToken,
			Subjects:      []string{workintake.SubjectTaskWildcard, agentexec.SubjectAgentWildcard},
			FilterSubject: workintake.SubjectTaskWildcard,
		}, log)
		if err != nil {
			log.Error("nats connect failed", "err", err)
			return 1
		}
		defer natsClient.Close()
		log.Info("nats connected", "url", cfg.NATS.URL)
	}

	// Container pool (optional  --  no containers when [containers] is absent).
	//
	// Container setup degrades rather than aborting, for the same reason
	// resolveForge does: containers are one capability among many, and
	// refusing to start denies the operator chat, the dashboard and every
	// other subsystem over a feature they may not be exercising right now.
	// Under a systemd unit with Restart=on-failure it also turns a recoverable
	// problem into a crash loop with the real error scrolling past unread.
	containerPool, storeBackend, closeDocker := startContainers(ctx, cfg, log)
	defer closeDocker()
	// Sandboxing that was asked for and could not start must not silently
	// become no sandboxing: the daemon stays up, but tasks park rather than
	// running the agent loop on the host.
	sandboxRequired := cfg.Containers.Enabled && containerPool == nil

	// ── LLM runtime ──────────────────────────────────────────────────
	// Created before gateways so the LLMResponder can be wired into the
	// Telegram router for non-command message processing.
	providers := executionProviders(cfg)
	llm := agentexec.NewRuntime(providers)
	toolReg := tools.NewRegistry()
	chatModels := newChatModelManager(cfg.Models, cfg.Chat.Models, catalogModels)
	chatModels.ApplyModelCatalog(catalog)

	// ── Persona registry ─────────────────────────────────────────────
	personas := gateway.NewPersonaRegistry(gateway.DefaultPersonas())

	profiles, defaultChatIdentity := chatTaskProfiles(cfg)
	var chatTasks gateway.TaskCreator
	if len(profiles) > 0 {
		chatTasks = gateway.NewStoreTaskCreatorForProfiles(
			chatTaskWriterAdapter{enqueue: st.EnqueueChatTask},
			profiles,
		)
	}
	chatController := gateway.NewStoreTaskController(chatTaskControllerAdapter{
		taskByID:   st.TaskByID,
		requeue:    st.Requeue,
		transition: st.Transition,
	})
	updateService := makeUpdateService(telegramSetup{Cfg: config.NewHolder(cfg)})
	web.WorkRequests = chatTasks

	// The dashboard is another gateway, not a second chat implementation. It
	// shares the router, session history, model selection, personas and LLM
	// responder with Telegram while using a stable browser channel id.
	webRouter := gateway.NewRouter(st, nil, "web")
	webRouter.Version = fmt.Sprintf("Archie\nGateway: %s\nRuntime: %s", gatewayVersion, runtimeVersion)
	if cfg.Chat.Telegram.TokenEnv != "" {
		webRouter.Restart = func(context.Context) error {
			if restartTelegram == nil {
				return fmt.Errorf("telegram gateway is not ready")
			}
			return restartTelegram()
		}
	}
	webRouter.Models = chatModels
	if updateService != nil {
		webRouter.Updates = updateService
	}
	webRouter.Personas = personas
	webRouter.InitSessions(chatSessionStore)
	configureTaskCommands(webRouter, chatTasks, chatController, defaultChatIdentity)
	webSetup := telegramSetup{
		Cfg: config.NewHolder(cfg), St: st, LLM: llm, ChatModels: chatModels, ToolReg: toolReg,
		Personas: personas, ChatTasks: chatTasks, ChatController: chatController,
		ChatTaskLister: chatTaskListerAdapter{tasks: st.Tasks},
		ChatTaskLogs: chatTaskLogReaderAdapter{
			tasks:    st.TaskByID,
			taskLogs: taskLogs,
		},
		DefaultChatIdentity: defaultChatIdentity, SessionStore: chatSessionStore,
		Bus: bus, Log: log,
	}
	webRouter.LLM, webRouter.LLMStream = makeChatLLMResponder("web", webSetup, chatSessionStore, webRouter)
	webRouter.Titles = newChatTitleGenerator(webSetup)
	webRouter.Log = log
	web.Chat = &webui.ChatService{
		Router: webRouter, Sessions: chatSessionStore,
		Turns:  gateway.NewTurns(log),
		Models: chatModels, Personas: personas,
	}
	if updateService != nil {
		web.Chat.Updates = updateService
	}
	// ── Gateways ──────────────────────────────────────────────────────
	// Multi-agent collaboration PRD phase C (docs/prds/multi-agent-collaboration.md).
	var startGateways []func()
	start, ok := setupTelegramGateway(ctx, telegramSetup{
		Cfg: config.NewHolder(cfg), CfgPath: *cfgPath, OverlayPath: *overlayPath,
		St: st, LLM: llm, ChatModels: chatModels, ToolReg: toolReg,
		Personas: personas, ChatTasks: chatTasks, ChatController: chatController,
		ChatTaskLister: chatTaskListerAdapter{tasks: st.Tasks},
		ChatTaskLogs: chatTaskLogReaderAdapter{
			tasks:    st.TaskByID,
			taskLogs: taskLogs,
		},
		DefaultChatIdentity: defaultChatIdentity, SessionStore: chatSessionStore, Updates: updateService,
		Bus:             bus,
		RegisterRestart: func(request func() error) { restartTelegram = request }, Log: log,
		ChannelManager: channelManager,
	})
	if !ok {
		return 1
	}
	if start != nil {
		startGateways = append(startGateways, start)
	}

	// ── Email gateway (optional) ───────────────────────────────────
	if cfg.Chat.Email.ListenAddr != "" {
		em := email.New(cfg.Chat.Email.ListenAddr, cfg.Chat.Email.RelayAddr, log)
		emRouter := gateway.NewRouter(st, nil, "email")
		configureTaskCommands(emRouter, chatTasks, chatController, defaultChatIdentity)
		startGateways = append(startGateways, func() {
			channelManager.MarkStarting("email")
			go func() {
				if err := em.Start(ctx, emRouter); err != nil && ctx.Err() == nil {
					channelManager.MarkFailed("email", err.Error())
					log.Error("email gateway stopped", "err", err)
				}
			}()
			log.Info("email gateway started", "addr", cfg.Chat.Email.ListenAddr)
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
		whRouter := gateway.NewRouter(st, nil, "webhook")
		configureTaskCommands(whRouter, chatTasks, chatController, defaultChatIdentity)
		startGateways = append(startGateways, func() {
			channelManager.MarkStarting("webhook")
			go func() {
				if err := wh.Start(ctx, whRouter); err != nil && ctx.Err() == nil {
					channelManager.MarkFailed("webhook", err.Error())
					log.Error("webhook gateway stopped", "err", err)
				}
			}()
			log.Info("webhook gateway started", "addr", fmt.Sprintf("%s:%d", host, port))
		})
	}

	var agentRunner agentexec.Runner
	if llm != nil {
		switch cfg.Agent.Mode {
		case "subprocess":
			agentRunner = &agentexec.SubprocessRunner{
				Command:       cfg.Agent.Command,
				Environ:       os.Environ(),
				AdditionalEnv: cfg.Agent.Env,
				Diagnostics:   os.Stderr,
				Providers:     providers,
			}
		case "inprocess":
			inproc := agentexec.NewInProcessRunner(llm, log, toolReg)
			inproc.Limits = toolLimits(cfg)
			agentRunner = inproc
		case "nats":
			if natsClient == nil {
				log.Error("agent.mode is nats but [nats] is not configured")
				return 1
			}
			agentRunner = &agentexec.NATSRunner{
				Bus:        natsClient,
				Providers:  providers,
				MCPServers: cfg.Tools.MCPServers,
				Log:        log,
			}
		}
	}
	// Build the workflow registry from the skill catalog. Plugin-defined
	// workflows override built-ins of the same name; built-ins fill gaps.
	skillsBase := cfg.SkillsDir
	if skillsBase == "" {
		skillsBase = cfg.WorkDir
	}
	workflowCatalog, err := skillbuild.BuildCatalog(skillsBase)
	if err != nil {
		log.Error("skill registry build failed", "err", err)
		return 1
	}
	registry := workflowCatalog.Registry
	log.Info("workflow registry built", "workflows", len(registry))
	web.Workflows = workflow.DefinitionsWithOrigins(registry, workflowCatalog.Origins)
	if l := cfg.Web.Listen; l != "" && l != "off" {
		web.Token = webTokenFor(l, cfg.DBPath, log)
		go func() {
			if err := web.Run(ctx, l); err != nil {
				log.Error("web ui failed", "err", err)
			}
		}()
	}

	// Load daemon plugins from the configured plugin directory (Layer 2).
	// Failed plugins are skipped  --  the daemon starts with the remaining set.
	capabilityHost := plugin.NewHost()
	if cfg.PluginDir != "" {
		plugins, err := plugin.LoadDir(cfg.PluginDir, pluginextract.Symbols)
		if err != nil {
			log.Error("plugin load failed", "dir", cfg.PluginDir, "err", err)
			return 1
		}
		for _, p := range plugins {
			name, version := safePluginInfo(p)
			module, err := plugin.AdaptLegacy(p)
			if err != nil {
				log.Warn("daemon plugin capability registration skipped", "name", name, "version", version, "err", err)
				continue
			}
			if err := capabilityHost.Register(module); err != nil {
				log.Warn("daemon plugin capability registration skipped", "name", name, "version", version, "err", err)
				continue
			}
			log.Info("daemon plugin loaded", "name", name, "version", version)
		}
	}

	trees := &worktree.Manager{
		WorkDir:  cfg.WorkDir,
		Token:    token,
		BotUser:  cfg.BotUser,
		BotEmail: cfg.BotEmail,
		BaseURL:  cfg.Forge.Host,
	}

	// ── Multi-identity composition ──────────────────────────────────────
	// Each configured identity gets its own forge client (its own token,
	// possibly its own forge type/host) and its own worktree manager (a
	// distinct WorkDir so concurrent identities never collide on the same
	// clone). When cfg.Identities is empty, Daemon.Identities stays nil
	// and Run() takes the single-identity path unchanged.
	var identityRunners []*daemon.IdentityRunner
	for _, idCfg := range cfg.Identities {
		// Same reasoning as the primary forge: one identity whose credential is
		// missing must not deny every other identity, and every other
		// subsystem, the ability to run.
		idForge, idToken := resolveForge(idCfg.Forge, secrets, log.With("identity", idCfg.Name))
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
			return 1
		}
		idProviders := agentexec.ProvidersFromConfig(idCfg.Providers)
		idRuntime := agentexec.NewRuntime(idProviders)
		if idRuntime != nil {
			switch cfg.Agent.Mode {
			case "subprocess":
				runner.Agent = &agentexec.SubprocessRunner{
					Command:       cfg.Agent.Command,
					Environ:       os.Environ(),
					AdditionalEnv: cfg.Agent.Env,
					Diagnostics:   os.Stderr,
					Providers:     idProviders,
				}
			case "inprocess":
				idRunner := agentexec.NewInProcessRunner(idRuntime, log.With("identity", idCfg.Name), toolReg)
				idRunner.Limits = toolLimits(cfg)
				runner.Agent = idRunner
			case "nats":
				runner.Agent = &agentexec.NATSRunner{
					Bus: natsClient, Providers: idProviders,
					MCPServers: cfg.Tools.MCPServers, Log: log.With("identity", idCfg.Name),
				}
			}
		}
		identityRunners = append(identityRunners, runner)
		log.Info("identity configured", "identity", idCfg.Name, "bot_user", idCfg.BotUser, "repos", len(idCfg.Repos))
	}

	// Let archie-agent containers (which hold no DB connection, forge
	// token, or push credential) proxy Store/Forge/worktree operations
	// back to archied over NATS.
	if natsClient != nil {
		coreConn, err := natsClient.CoreConn()
		if err != nil {
			log.Error("nats connection unavailable for task RPC", "err", err)
			return 1
		}
		unsubscribe, err := registerTaskRPCServers(coreConn, st, forgeClient, trees, identityRunners, log)
		if err != nil {
			log.Error("task RPC server registration failed", "err", err)
			return 1
		}
		defer unsubscribe()

		unsubscribeSystemLogs, err := subscribeSystemLogs(coreConn, taskLogs, log)
		if err != nil {
			log.Error("system log subscribe failed", "err", err)
			return 1
		}
		defer unsubscribeSystemLogs()
	}

	// ── Memory manager ────────────────────────────────────────────────
	// Built-in file-backed provider (MEMORY.md + USER.md) under the
	// daemon work directory. External providers from config are added
	// via RegisterExternal when cfg.Memory.Provider is set.
	var memManager *memory.Manager
	memProvider, memDir := memoryProvider(cfg.WorkDir, log)
	if memProvider == nil {
		log.Error("memory provider init failed", "dir", memDir)
		return 1
	}
	memManager, err = memory.NewManager(memProvider, nil)
	if err != nil {
		log.Error("memory manager init failed", "err", err)
		return 1
	}
	// The dashboard is built before memory exists, so it is wired in here
	// rather than at construction.
	web.Memory = memManager

	if err := memManager.Initialize("daemon"); err != nil {
		log.Warn("memory manager initialize", "err", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := memManager.ShutdownContext(shutdownCtx); err != nil {
			log.Error("memory manager shutdown", "err", err)
		}
	}()
	log.Info("memory manager started", "dir", memDir)

	// ── Curator engine family ────────────────────────────────────────
	// The registry owns curator registration, lifecycle and shutdown
	// ordering. The runtime loop (archie-core-89x) and the reference
	// curators (archie-core-i7i, gs8) are wired by their issues; until
	// then the registry is empty and Stop is a no-op. The event sink
	// rides the in-process bus, whose bounded dropping per-subscriber
	// buffers guarantee curator activity can never backpressure the
	// daemon or a chat turn.
	curatorRegistry := curator.NewRegistry(curator.Registrar{
		Events: curatorEventSink{bus},
	})
	// The runtime owns the per-curator loops (archie-core-89x): one
	// goroutine per curator, wake nudges, per-pass budgets, panic
	// recovery, bounded shutdown. Stop order at shutdown: runtime first
	// (cancels in-flight passes, then stops curator lifecycle), then the
	// registry's own Stop below, which is a no-op by then.
	curatorRuntime := curator.NewRuntime(curatorRegistry, curator.RuntimeConfig{})
	// Primary chat turns wake input-driven curators (archie-core-035):
	// the forwarder consumes only primary-input kinds, and curator
	// output never produces them, so derived work cannot feed its own
	// trigger. The subscriber buffer is bounded and dropping — a slow
	// curator can never backpressure the chat publisher.
	curator.WakeOnPrimaryInput(ctx, bus, curatorRuntime, events.KindTurnCompleted)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := curatorRuntime.Stop(stopCtx); err != nil {
			log.Error("curator runtime shutdown", "err", err)
		}
	}()
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := curatorRegistry.Stop(stopCtx); err != nil {
			log.Error("curator registry shutdown", "err", err)
		}
	}()

	// ── Guardrail engine ───────────────────────────────────────────────
	gc := tools.DefaultGuardrailConfig()
	guardrails := tools.NewGuardrailEngine(gc)
	log.Info("guardrail engine enabled",
		"exact_failure_warn", gc.ExactFailureWarnAfter,
		"same_tool_failure_warn", gc.SameToolFailureWarnAfter,
		"no_progress_warn", gc.NoProgressWarnAfter,
	)

	// ── Tool registry ──────────────────────────────────────────────────
	providerRegistry := toolprovider.NewRegistry(toolReg)
	if err := providerRegistry.Register(memorytoolprovider.New(memManager)); err != nil {
		log.Error("memory tool provider registration failed", "err", err)
		return 1
	}
	// Workspace file and shell tools. Registered only when a workspace is
	// configured: these read, write and execute, so the directory is a
	// deliberate choice rather than a default.
	if workspace := cfg.Chat.Workspace; workspace != "" {
		unrestricted := cfg.Chat.UnrestrictedFilesystem
		if err := providerRegistry.Register(builtintoolprovider.New(workspace, unrestricted)); err != nil {
			log.Error("workspace tool provider registration failed", "err", err)
			return 1
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
		provider, err := configuredMCPProvider(srv)
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
		if err := providerRegistry.RegisterOptional(provider); err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}
	}
	if err := capabilityHost.Register(providerRegistry); err != nil {
		log.Error("tool-provider capability registration failed", "err", err)
		return 1
	}

	// Skill catalog → skill_activate tool (progressive disclosure: the
	// catalog's name+description is always in the tool schema; the full
	// SKILL.md body loads only when the model activates one).
	if catalog, err := skill.CatalogRoots(skill.DefaultRoots(cfg.WorkDir, cfg.SkillsDir)...); err != nil {
		log.Warn("skill catalog load failed", "err", err)
	} else if entry := skill.ActivateTool(cfg.WorkDir, catalog); entry != nil {
		if err := toolReg.Register(*entry); err != nil {
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
		if err := toolReg.Register(*entry); err != nil {
			log.Warn("web_fetch registration failed", "err", err)
		} else {
			log.Info("web fetch enabled",
				"allow_private_networks", cfg.Tools.WebFetch.AllowPrivateNetworks)
		}
	} else {
		log.Info("web fetch disabled")
	}

	// Built-in tools are registered at startup via init(). Tool discovery
	// via Yaegi is deferred to the container/agent runtime.

	d := &daemon.Daemon{
		Cfg:             config.NewHolder(cfg),
		ConnectedNATS:   cfg.NATS,
		Store:           st,
		Bus:             bus,
		Forge:           forgeClient,
		Trees:           trees,
		Agent:           agentRunner,
		Workflows:       registry,
		CapabilityHost:  capabilityHost,
		Storage:         storeBackend,
		Log:             log,
		CustomStages:    wfeval.Discover,
		Tasks:           natsClient,
		ContainerPool:   containerPool,
		SandboxRequired: sandboxRequired,
		Memory:          memManager,
		Guardrails:      guardrails,
		ToolRegistry:    toolReg,
		Curators:        curatorRegistry,
		Identities:      identityRunners,
		TaskLogs:        taskLogs,
	}
	web.TaskStopper = d
	// web and the daemon must share ONE Holder: a reload swaps d.Cfg and
	// the dashboard reads the running config from the same snapshot. The
	// webui Holder seeded in the literal above is replaced here; after
	// this point /api/config and the daemon can never disagree about the
	// published config.
	web.Cfg = d.Cfg

	// publishConfig publishes a config snapshot and its provenance to
	// both the daemon and the dashboard (they share one Holder). Reload
	// and the PATCH path both go through it, so the two can never
	// diverge. currentProvenance (an atomic) is the last published
	// provenance chain; the PATCH path appends the runtime-overlay
	// origin to it.
	publishConfig := func(cfg config.Config, provenance configuration.Provenance) {
		d.Cfg.Set(cfg)
		origins := make([]webui.ConfigOrigin, 0, len(provenance.Origins))
		for _, origin := range provenance.Origins {
			origins = append(origins, webui.ConfigOrigin{
				Path: origin.Path, Role: string(origin.Role), Layer: string(origin.Layer), Feature: string(origin.Feature),
			})
		}
		web.SetProvenance(origins)
	}

	// SIGHUP re-loads the config from disk, layers the runtime overlay,
	// and republishes it. The reload log warns about changed fields that
	// require a restart, so an operator never has to read the source to
	// find out whether their edit took effect. web.LastReload exposes
	// the outcome to /api/config.
	reloadController := newReloadController(loader, *cfgPath, *overlayPath, func(doc *configuration.Document) {
		// Boot merges catalog-discovered providers into cfg before the
		// Holders are seeded; publishing the raw reloaded document would
		// drop them from the running config even though the file is
		// unchanged. Re-apply the same merge (idempotent: a catalog that
		// failed to load merges to identity).
		applyModelCatalog(&doc.Config, catalog)
		old := d.Cfg.Get()
		currentProvenance.Store(&doc.Provenance)
		publishConfig(doc.Config, doc.Provenance)
		if fields := changedNonReloadableFields(old, doc.Config); len(fields) > 0 {
			log.Warn("config reloaded; some changes require a restart",
				"fields", fields, "paths", doc.Provenance.Paths())
		} else {
			log.Info("config reloaded", "paths", doc.Provenance.Paths())
		}
	})
	if overlayStore != nil {
		reloadController.WithOverlay(func() (map[string]any, error) { return overlayStore.Snapshot(ctx) })
	}
	// LastReload merges the reload controller's outcome with the boot-time
	// overlay degrade, so /api/config carries both the last reload result
	// and whether the runtime overlay is in effect at all.
	web.LastReload = func() config.ReloadStatus {
		st := reloadController.Status()
		if p := bootOverlayErr.Load(); p != nil {
			st.OverlayUnavailable = *p
		}
		return st
	}

	// PATCH /api/config goes through the same publish path as reload:
	// apply to a deep copy of the published config, validate the
	// materialised result, persist to the overlay, then publish. It must
	// never call Set directly from the handler -- web.Cfg is the daemon's
	// own Holder -- and it decodes into a Clone so a failed validation
	// cannot mutate the published snapshot's shared maps.
	web.UpdateConfig = func(ctx context.Context, updates map[string]any) error {
		if overlayStore == nil {
			return fmt.Errorf("%w: config overlay is disabled (--no-config-overlay or ARCHIE_SKIP_CONFIG_OVERLAY=1)", webui.ErrConfigUpdateUnavailable)
		}
		for key := range updates {
			if reason, denied := overlay.DeniedKeys[key]; denied {
				return fmt.Errorf("%w: config key %s is not runtime-tunable: %s", webui.ErrConfigUpdateInvalid, key, reason)
			}
		}
		next := d.Cfg.Get().Clone()
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
			if err := overlayStore.Set(ctx, key, string(data), "dashboard"); err != nil {
				return err
			}
		}
		// Provenance gets the runtime-overlay origin, replacing any prior
		// one so the chain does not grow unboundedly across edits.
		var kept []configuration.Origin
		for _, origin := range currentProvenance.Load().Origins {
			if origin.Path == "config_overlay (runtime)" {
				continue
			}
			kept = append(kept, origin)
		}
		prov := configuration.Provenance{Origins: append(kept,
			configuration.Origin{Path: "config_overlay (runtime)", Role: configuration.RoleMain, Layer: configuration.LayerOverlay})}
		currentProvenance.Store(&prov)
		publishConfig(next, prov)
		bootOverlayErr.Store(nil) // a successful write proves the overlay works again
		log.Info("config updated from dashboard", "keys", len(updates))
		return nil
	}

	// ConfigOverrides lists the dotted keys currently overridden by the
	// runtime overlay, so the dashboard can mark those rows and offer a
	// reset. A disabled store reports no overrides.
	web.ConfigOverrides = func(ctx context.Context) ([]string, error) {
		if overlayStore == nil {
			return nil, nil
		}
		rows, err := overlayStore.Snapshot(ctx)
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
	web.ResetConfig = func(ctx context.Context, key string) error {
		if overlayStore == nil {
			return fmt.Errorf("%w: config overlay is disabled", webui.ErrConfigUpdateUnavailable)
		}
		overrides, err := overlayStore.Snapshot(ctx)
		if err != nil {
			return err
		}
		delete(overrides, key) // the target overlay state after the reset
		doc, err := loader.Resolve(*cfgPath, *overlayPath)
		if err != nil {
			return fmt.Errorf("%w: %w", webui.ErrConfigUpdateInvalid, err)
		}
		if len(overrides) > 0 {
			if doc, err = loader.ApplyOverlay(doc, overrides); err != nil {
				return fmt.Errorf("%w: %w", webui.ErrConfigUpdateInvalid, err)
			}
		}
		if err := overlayStore.Delete(ctx, key); err != nil {
			return err
		}
		applyModelCatalog(&doc.Config, catalog)
		currentProvenance.Store(&doc.Provenance)
		publishConfig(doc.Config, doc.Provenance)
		bootOverlayErr.Store(nil)
		log.Info("config key reset to file value", "key", key)
		return nil
	}

	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)
	defer signal.Stop(reloadCh)
	go reloadLoop(ctx, reloadCh, reloadController, log)

	// Give /cancel and /stop a handle on work already in flight. The
	// controller is built before the daemon exists, so the runtime is
	// attached here; gateways start further down, after d.Startup, so no
	// command can arrive before this is wired.
	chatController.WithRuntime(d)

	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := capabilityHost.Stop(stopCtx); err != nil {
			log.Error("capability host shutdown", "err", err)
		}
	}()
	if err := capabilityHost.Start(ctx); err != nil {
		log.Error("capability host startup", "err", err)
		return 1
	}
	// Optional providers that could not start are excluded rather than fatal,
	// so the only way an operator learns about one is this line.
	for _, skipped := range providerRegistry.Skipped() {
		log.Error("tool provider unavailable; archie is running without its tools",
			"provider", skipped.ID, "err", skipped.Err)
	}

	if err := d.Startup(ctx); err != nil {
		log.Error("startup", "err", err)
		return 1
	}
	if err := curatorRuntime.Start(ctx); err != nil {
		log.Error("curator runtime startup", "err", err)
	}
	for _, startGateway := range startGateways {
		startGateway()
	}

	if *once {
		d.Cycle(ctx)
		return 0
	}
	log.Info("archied running", "repos", len(cfg.Repos), "poll", cfg.PollInterval.Std().String(), "label", cfg.Label)
	if err := d.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("daemon exited", "err", err)
		return 1
	}
	return 0
}

func persistAndBroadcastEvents(sink *events.Sub, st store.TaskStore, web *webui.Server, log *slog.Logger) {
	for e := range sink.C {
		if e.ID == 0 {
			id, err := st.InsertEvent(context.Background(), e)
			if err != nil {
				log.Error("event sink insert failed", "err", err)
				continue
			}
			e.ID = id
		}
		web.Broadcast(e)
	}
}

// curatorEventSink adapts the in-process event bus to the curator family's
// narrow emission contract. Bus publish is non-blocking with bounded,
// dropping per-subscriber buffers, so curator activity can never
// backpressure the daemon or a chat turn.
type curatorEventSink struct {
	b *events.Bus
}

func (s curatorEventSink) Emit(kind, detail string, data map[string]any) {
	s.b.Publish(events.Event{Kind: kind, Detail: detail, Data: data})
}

type chatTaskWriterAdapter struct {
	enqueue func(
		ctx context.Context,
		owner, repo, title, body, workflow, identity string,
	) (*store.Task, error)
}

func (a chatTaskWriterAdapter) EnqueueChatTask(
	ctx context.Context,
	owner, repo, title, body, workflow, identity string,
) (int64, error) {
	task, err := a.enqueue(ctx, owner, repo, title, body, workflow, identity)
	if err != nil {
		return 0, err
	}
	if task == nil {
		return 0, fmt.Errorf("enqueue chat task returned no task")
	}
	return task.ID, nil
}

type chatTaskControllerAdapter struct {
	taskByID   func(context.Context, int64) (*store.Task, error)
	requeue    func(context.Context, int64, string, string) error
	transition func(context.Context, int64, string, string, string) error
}

func (a chatTaskControllerAdapter) ChatTaskStatus(ctx context.Context, taskID int64) (gateway.ChatTaskStatus, bool, error) {
	task, err := a.taskByID(ctx, taskID)
	if err != nil {
		return gateway.ChatTaskStatus{}, false, err
	}
	if task == nil {
		return gateway.ChatTaskStatus{}, false, nil
	}
	if !task.IsForgeBacked() {
		return gateway.ChatTaskStatus{Status: task.Status, Identity: task.Identity}, true, nil
	}
	return gateway.ChatTaskStatus{}, false, fmt.Errorf("task %d is not chat-originated", taskID)
}

func (a chatTaskControllerAdapter) ApproveChatTask(ctx context.Context, taskID int64) error {
	return a.requeue(ctx, taskID, store.StatusWaitingHuman, "implement")
}

func (a chatTaskControllerAdapter) CancelChatTask(ctx context.Context, taskID int64, reason string) error {
	task, err := a.taskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	if task.IsForgeBacked() {
		return fmt.Errorf("task %d is not chat-originated", taskID)
	}
	// An operator declining work lands in the same state whichever surface
	// they used. This used to record StatusRejected while the dashboard
	// recorded StatusClosedWontDo, so the same decision showed up as two
	// different states -- and StatusRejected, which the PR reconciler uses
	// for "the pull request was closed without merging", stopped meaning one
	// thing.
	return a.transition(ctx, taskID, task.Status, store.StatusClosedWontDo, reason)
}

// chatTaskLogReaderAdapter gives the gateway a read view of a task's
// persisted log history without importing internal/logging or internal/store
// into the gateway package. Each identity's reader is scoped to its own
// tasks: the identity bound at construction is used for authorization, so a
// model cannot read another identity's task logs by passing a different
// identity through the tool input.
type chatTaskLogReaderAdapter struct {
	tasks    func(context.Context, int64) (*store.Task, error)
	taskLogs *logging.TaskRegistry
}

func (a chatTaskLogReaderAdapter) ReadChatTaskLogs(
	ctx context.Context, identity string, taskID int64, attempt int, q gateway.ChatTaskLogQuery,
) (gateway.ChatTaskLogResult, error) {
	task, err := a.tasks(ctx, taskID)
	if err != nil {
		return gateway.ChatTaskLogResult{}, err
	}
	if task == nil {
		return gateway.ChatTaskLogResult{}, fmt.Errorf("task %d not found", taskID)
	}
	// Match the filter chatTaskListerAdapter already applies: a model
	// bound to one identity must not read logs for another identity's
	// tasks, and tasks with no identity (forge-sourced) are not
	// readable through a chat tool at all — they belong to the daemon,
	// not a particular identity. "The empty string MUST NOT retain
	// special meaning" (docs/architecture/identity.md).
	if task.Identity != identity {
		return gateway.ChatTaskLogResult{}, fmt.Errorf("task %d belongs to %q, not %q", taskID, task.Identity, identity)
	}

	if attempt <= 0 {
		attempt = task.Attempt
	}
	path := a.taskLogs.Path(taskID, attempt)
	if path == "" {
		return gateway.ChatTaskLogResult{Attempt: attempt, Entries: []gateway.ChatTaskLogEntry{}}, nil
	}

	result, err := logging.Tail(path, logging.Query{
		Component: q.Component,
		Contains:  q.Contains,
		Limit:     q.Limit,
	})
	if err != nil {
		return gateway.ChatTaskLogResult{}, err
	}

	entries := make([]gateway.ChatTaskLogEntry, len(result.Entries))
	for i, e := range result.Entries {
		entries[i] = gateway.ChatTaskLogEntry{
			Time:    e.Time,
			Level:   e.Level,
			Message: e.Message,
			Fields:  e.Fields,
		}
	}
	return gateway.ChatTaskLogResult{
		Entries:   entries,
		Attempt:   attempt,
		Truncated: result.Truncated,
	}, nil
}

func chatTaskProfiles(cfg config.Config) ([]gateway.TaskProfile, string) {
	if len(cfg.Identities) > 0 {
		profiles := make([]gateway.TaskProfile, 0, len(cfg.Identities))
		for _, identity := range cfg.Identities {
			if len(identity.Repos) == 0 {
				continue
			}
			profiles = append(profiles, newChatTaskProfile(identity.Name, identity.Repos))
		}
		if len(profiles) == 0 {
			return nil, ""
		}
		return profiles, profiles[0].Identity
	}
	if len(cfg.Repos) == 0 {
		return nil, ""
	}
	return []gateway.TaskProfile{newChatTaskProfile("", cfg.Repos)}, ""
}

func newChatTaskProfile(identity string, repos []config.Repo) gateway.TaskProfile {
	allowed := make([]string, 0, len(repos))
	for _, repo := range repos {
		allowed = append(allowed, repo.Owner+"/"+repo.Name)
	}
	return gateway.TaskProfile{
		Identity:     identity,
		DefaultOwner: repos[0].Owner,
		DefaultRepo:  repos[0].Name,
		Repos:        allowed,
	}
}

func configureTaskCommands(
	router *gateway.Router,
	tasks gateway.TaskCreator,
	controller gateway.TaskController,
	identity string,
) {
	router.Tasks = tasks
	router.Controller = controller
	router.Identity = identity
}

// parseListenAddr splits "host:port" into components, using defaults
// when the input is empty or missing a part.
func parseListenAddr(addr, defaultHost string, defaultPort int) (string, int) {
	if addr == "" {
		return defaultHost, defaultPort
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return defaultHost, defaultPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return host, defaultPort
	}
	return host, port
}

// sessionKey builds a deterministic session identifier from a gateway
// message's routing fields. Platform + channel + thread uniquely identify
// a conversation for session persistence and history retrieval.
func executionProviders(cfg config.Config) map[string]agentexec.Provider {
	return agentexec.ProvidersFromConfig(cfg.Providers)
}

// subscribeSystemLogs subscribes to every task's system log subject at once.
// A sandboxed container's own stderr disappears at AutoRemove; archie-agent
// ships it here instead (agentexec.SubjectForSystem), and this is where it
// lands: one wildcard subscription demuxed by taskID and routed through
// taskLogs, which is opened for a task's duration in Daemon.process, so a
// message for a task not currently open there -- late, duplicate, or from
// an attempt this instance never dispatched -- is expected and silently
// dropped rather than treated as an error.
func subscribeSystemLogs(nc *natsio.Conn, taskLogs *logging.TaskRegistry, log *slog.Logger) (unsubscribe func(), err error) {
	sub, err := nc.Subscribe(agentexec.SubjectSystemWildcard, func(msg *natsio.Msg) {
		taskID, ok := agentexec.TaskIDFromSystemSubject(msg.Subject)
		if !ok {
			log.Warn("system log message on unparseable subject", "subject", msg.Subject)
			return
		}
		var entry logging.Entry
		if err := json.Unmarshal(msg.Data, &entry); err != nil {
			log.Warn("system log message undecodable", "task", taskID, "err", err)
			return
		}
		taskLogs.Write(taskID, entry)
	})
	if err != nil {
		return nil, err
	}
	return func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Warn("system log unsubscribe failed", "err", err)
		}
	}, nil
}

// registerTaskRPCServers subscribes the storerpc/forgerpc/worktreerpc
// handlers on nc so an archie-agent container (which holds no DB
// connection, forge token, or push credential) can proxy those operations
// back to archied. The returned func unsubscribes all of them.
//
// The store is shared across identities, so storerpc registers once on its
// root subjects. Forge and worktree are identity-scoped: the root servers
// answer the root (identity-less) subjects for single-identity deployments
// and root-owned tasks, and one server pair per identity answers that
// identity's scoped subjects so a container-mode task owned by a non-root
// identity has its RPC calls served by its own forge client and worktree
// manager.
func registerTaskRPCServers(nc *natsio.Conn, st store.TaskStore, forgeClient forge.Forge, trees *worktree.Manager, identities []*daemon.IdentityRunner, log *slog.Logger) (unsubscribe func(), err error) {
	unsubs := make([]func(), 0, 3+2*len(identities))
	unsubAll := func() {
		for _, u := range unsubs {
			u()
		}
	}

	storeServer := &storerpc.Server{Store: st, Log: log}
	u, err := storeServer.Register(nc)
	if err != nil {
		return nil, fmt.Errorf("register storerpc: %w", err)
	}
	unsubs = append(unsubs, u)

	registerForge := func(fg forge.Forge, identity string) error {
		srv := &forgerpc.Server{Forge: fg, Log: log.With("rpc_identity", identity)}
		u, err := srv.RegisterFor(nc, identity)
		if err != nil {
			return fmt.Errorf("register forgerpc%s: %w", identitySuffix(identity), err)
		}
		unsubs = append(unsubs, u)
		return nil
	}
	registerTrees := func(mgr *worktree.Manager, identity string) error {
		srv := &worktreerpc.Server{Trees: mgr, Log: log.With("rpc_identity", identity)}
		u, err := srv.RegisterFor(nc, identity)
		if err != nil {
			return fmt.Errorf("register worktreerpc%s: %w", identitySuffix(identity), err)
		}
		unsubs = append(unsubs, u)
		return nil
	}

	// Root (identity-less) forge and worktree servers.
	if err := registerForge(forgeClient, ""); err != nil {
		unsubAll()
		return nil, err
	}
	if err := registerTrees(trees, ""); err != nil {
		unsubAll()
		return nil, err
	}

	// One forge/worktree server pair per identity, on identity-scoped
	// subjects.
	for _, id := range identities {
		if err := registerForge(id.Forge, id.Name); err != nil {
			unsubAll()
			return nil, err
		}
		if err := registerTrees(id.Trees, id.Name); err != nil {
			unsubAll()
			return nil, err
		}
	}

	return unsubAll, nil
}

// identitySuffix renders a log suffix for an identity-scoped registration,
// empty for the root set.
func identitySuffix(identity string) string {
	if identity == "" {
		return ""
	}
	return " (" + identity + ")"
}

func configuredNATSToken(cfg config.NATSConfig, getenv func(string) string) (string, error) {
	if cfg.TokenEnv == "" {
		return "", nil
	}
	token := getenv(cfg.TokenEnv)
	if token == "" {
		return "", fmt.Errorf("%s is required when nats.token_env is configured", cfg.TokenEnv)
	}
	return token, nil
}

func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

func releaseAnnouncementStatePath(workDir, identity string) string {
	identityHash := sha256.Sum256([]byte(identity))
	return filepath.Join(
		workDir,
		fmt.Sprintf("release-announcements-%x.json", identityHash[:8]),
	)
}

// safePluginInfo calls Name() and Version() on a plugin, recovering from
// panics. A panicking plugin must not crash the daemon at startup.
func safePluginInfo(p plugin.Plugin) (name, version string) {
	defer func() {
		if r := recover(); r != nil {
			name = "(panic)"
			version = "(panic)"
		}
	}()
	return p.Name(), p.Version()
}

// webTokenFor returns the dashboard token for a listen address, or "" when
// none is needed.
//
// A loopback bind needs no token: local access already implies the agent's own
// authority, since its shell and edit tools run as this user. A reachable bind
// mints one and logs a URL to click, so an instance cannot be left exposed
// merely by omitting configuration.
func webTokenFor(listen, dbPath string, log *slog.Logger) string {
	if webui.IsLoopback(listen) {
		return ""
	}
	path := filepath.Join(filepath.Dir(dbPath), "web-token")
	tok, err := webui.LoadOrCreateToken(path)
	if err != nil {
		log.Error("web ui token", "err", err)
		return ""
	}
	log.Info("web ui token", "open", webui.DashboardURL(listen, tok), "stored", path)
	return tok
}

// memoryProvider builds the built-in memory provider, falling back to the
// default work directory when the configured one cannot be established.
//
// A bad path warns and degrades rather than stopping the daemon, in line with
// the forge and container paths: memory is one capability among many. The
// fallback is the location an unset work_dir would have produced, so an
// operator who copied a config from another host (a container path such as
// /var/lib/archie/work onto a laptop, say) lands somewhere predictable
// instead of somewhere only this function knows about.
//
// Returns the provider and the directory actually in use. A nil provider
// means the configuration is unusable in a way no fallback can fix.
func memoryProvider(workDir string, log *slog.Logger) (*builtin.Provider, string) {
	// An empty workDir would make filepath.Join produce the relative path
	// "memory", which MkdirAll creates in whatever directory the daemon
	// happens to be started from -- succeeding, and putting memory somewhere
	// nobody will look. Defaulting should have prevented an empty workDir;
	// treat it as unset rather than trusting it.
	var dir string
	var p *builtin.Provider
	if workDir == "" {
		log.Warn("no work directory configured, using the default for memory")
	} else {
		dir = filepath.Join(workDir, "memory")
		var err error
		p, err = builtin.New(builtin.Config{Dir: dir})
		if err != nil {
			log.Warn("memory directory rejected, falling back to the default", "dir", dir, "err", err)
			p = nil
		}
		if p != nil && p.IsAvailable() {
			return p, dir
		}
		if p != nil {
			log.Warn("memory directory unusable, falling back to the default",
				"dir", dir, "err", p.Err())
		}
	}

	fallback := filepath.Join(configuration.DefaultWorkDir(), "memory")
	if fallback == dir {
		// Already the default: there is nowhere else to try, so run degraded
		// and say what that costs rather than pretending memory works.
		log.Error("memory unavailable: the agent will start every conversation "+
			"with no recollection of earlier ones, and memory writes will fail",
			"dir", dir)
		return p, dir
	}

	fb, err := builtin.New(builtin.Config{Dir: fallback})
	if err != nil {
		log.Error("memory provider init failed at the default directory", "dir", fallback, "err", err)
		return p, dir
	}
	if !fb.IsAvailable() {
		log.Error("memory unavailable: neither the configured nor the default "+
			"directory is usable, so the agent will start every conversation "+
			"with no recollection of earlier ones",
			"configured", dir, "default", fallback, "err", fb.Err())
		return fb, fallback
	}
	log.Warn("using the default memory directory", "dir", fallback)
	return fb, fallback
}

// startContainers brings up the container pool and storage backend, returning
// nils when containers are disabled or unavailable.
//
// Failure here degrades rather than aborting, for the same reason resolveForge
// does: containers are one capability among many, and refusing to start denies
// the operator chat, the dashboard and every other subsystem over a feature
// they may not be exercising. Under Restart=on-failure it also turns a
// recoverable problem into a crash loop with the real error scrolling past.
func startContainers(
	ctx context.Context,
	cfg config.Config,
	log *slog.Logger,
) (*container.Pool, storage.Backend, func()) {
	noop := func() {}
	if !cfg.Containers.Enabled {
		return nil, nil, noop
	}

	// Single Docker client shared between pool and storage backend.
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		log.Error("containers disabled: docker client unavailable", "err", err)
		return nil, nil, noop
	}
	closeDocker := func() {
		if err := dockerCli.Close(); err != nil {
			log.Warn("docker client close failed", "err", err)
		}
	}

	pool, err := container.NewPool(ctx, container.Config{
		Image:          cfg.Containers.Image,
		MaxConcurrency: cfg.Containers.MaxConcurrency,
		MaxUptime:      cfg.Containers.MaxUptime.Std(),
		PullPolicy:     cfg.Containers.PullPolicy,
		Network:        cfg.Containers.Network,
		DockerClient:   dockerCli,
	}, cfg.NATS.URL, log)
	if err != nil {
		// A missing image is recoverable by hand. The daemon sends no registry
		// credentials on pull (internal/container/pool.go), so a private
		// registry always needs the operator's CLI to fetch it first.
		log.Error("containers disabled: pool unavailable", "err", err,
			"hint", "run `docker compose pull agent` (or `build agent`) so the image is present locally")
		return nil, storage.NewDockerBackend(dockerCli), closeDocker
	}

	// contextcheck: Pool.Close takes no context by design -- it is a shutdown
	// path that must run to completion after ctx is already cancelled.
	//nolint:contextcheck
	return pool, storage.NewDockerBackend(dockerCli), func() {
		if err := pool.Close(); err != nil {
			log.Warn("container pool close failed", "err", err)
		}
		closeDocker()
	}
}
