// Command archied is the archie orchestrator daemon: it watches GitHub
// for issues labelled for archie, works each one in an isolated
// worktree through its routed workflow, and opens pull requests for
// human review.
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"

	"github.com/moby/moby/client"
	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/channels/email"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/channels/webhook"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/memory/builtin"
	"github.com/samcharles93/archie-core/internal/nats"
	"github.com/samcharles93/archie-core/internal/nell"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	mcptoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/mcp"
	memorytoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/memory"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
	"github.com/samcharles93/archie-core/internal/releaseannounce"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/webui"
	"github.com/samcharles93/archie-core/internal/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/workflow/wfeval"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

func main() {
	os.Exit(run())
}

const (
	defaultChatMaxSteps          = 8
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

func chatGenerateOptions(
	messages []chat.Message,
	registry *tools.Registry,
) (core.GenerateOptions, error) {
	toolSet, err := agentexec.BuildToolSet(registry)
	if err != nil {
		return core.GenerateOptions{}, err
	}
	return core.GenerateOptions{
		Messages: messages,
		Tools:    toolSet,
		MaxSteps: defaultChatMaxSteps,
	}, nil
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
	if transportType != "stdio" {
		return nil, fmt.Errorf("MCP transport %q is not supported", transportType)
	}
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
}

func run() int {
	defaultCfg := filepath.Join(configHome(), "archie", "config.toml")
	cfgPath := flag.String("config", defaultCfg, "path to config.toml")
	overlayPath := flag.String("config-overlay", "", "path to a config.toml overlay applied on top of -config (only the fields it sets are overridden)")
	once := flag.Bool("once", false, "run a single poll+process cycle and exit (systemd timer / testing)")
	requeue := flag.Int64("requeue", 0, "requeue a parked/waiting task by id (keeps its workflow), then exit unless -once is also set")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.LoadOverlay(*cfgPath, *overlayPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	secrets := secret.NewRegistry()
	token, err := cfg.Forge.Token.Resolve(secrets)
	if err != nil || token == "" {
		fmt.Fprintf(os.Stderr, "forge token (engine %q, key %q) is required: %v\n", cfg.Forge.Token.Engine, cfg.Forge.Token.Key, err)
		return 1
	}
	forgeClient, err := forge.New(cfg.Forge.Type, token, cfg.Forge.Host, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	st, err := nell.OpenStore(cfg.DBPath, cfg.BotUser)
	if err != nil {
		log.Error("open store", "err", err)
		return 1
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("close store", "err", err)
		}
	}()

	if *requeue > 0 {
		if err := st.Requeue(context.Background(), *requeue, "manual", ""); err != nil {
			log.Error("requeue failed", "task", *requeue, "err", err)
			return 1
		}
		log.Info("task requeued", "task", *requeue)
		if !*once {
			return 0
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Observability: every event is logged to SQLite (stamped with its
	// row id) and then fanned out to live dashboard connections.
	bus := events.NewBus()
	defer bus.Close()
	web := &webui.Server{Store: st, Log: log}
	sink := bus.Subscribe(256)
	go func() {
		for e := range sink.C {
			id, err := st.InsertEvent(context.Background(), e)
			if err != nil {
				log.Error("event sink insert failed", "err", err)
				continue
			}
			e.ID = id
			web.Broadcast(e)
		}
	}()
	if l := cfg.Web.Listen; l != "" && l != "off" {
		go func() {
			if err := web.Run(ctx, l); err != nil {
				log.Error("web ui failed", "err", err)
			}
		}()
	}

	// NATS client (optional  --  SQLite flow unchanged when [nats] is absent).
	var natsClient *nats.Client
	if cfg.NATS.URL != "" {
		natsToken, err := configuredNATSToken(cfg.NATS, os.Getenv)
		if err != nil {
			log.Error("nats credentials", "err", err)
			return 1
		}
		natsClient, err = nats.Connect(ctx, cfg.NATS.URL, natsToken, log)
		if err != nil {
			log.Error("nats connect failed", "err", err)
			return 1
		}
		defer natsClient.Close()
		log.Info("nats connected", "url", cfg.NATS.URL)
	}

	// Container pool (optional  --  no containers when [containers] is absent).
	var containerPool *container.Pool
	var storeBackend storage.Backend
	if cfg.Containers.Enabled {
		// Single Docker client shared between pool and storage backend.
		dockerCli, err := client.New(client.FromEnv)
		if err != nil {
			log.Error("docker client failed", "err", err)
			return 1
		}
		defer func() {
			if err := dockerCli.Close(); err != nil {
				log.Warn("docker client close failed", "err", err)
			}
		}()

		containerPool, err = container.NewPool(ctx, container.Config{
			Image:          cfg.Containers.Image,
			MaxConcurrency: cfg.Containers.MaxConcurrency,
			MaxUptime:      cfg.Containers.MaxUptime.Std(),
			PullPolicy:     cfg.Containers.PullPolicy,
			Network:        cfg.Containers.Network,
			DockerClient:   dockerCli,
		}, cfg.NATS.URL, log)
		if err != nil {
			log.Error("container pool failed", "err", err)
			return 1
		}
		defer func() {
			if err := containerPool.Close(); err != nil {
				log.Warn("container pool close failed", "err", err)
			}
		}()

		storeBackend = storage.NewDockerBackend(dockerCli)
	}

	// ── LLM runtime ──────────────────────────────────────────────────
	// Created before gateways so the LLMResponder can be wired into the
	// Telegram router for non-command message processing.
	providers := executionProviders(cfg)
	llm := agentexec.NewRuntime(providers)
	toolReg := tools.NewRegistry()
	chatModels := newChatModelManager(cfg.Models, cfg.Chat.Models)

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

	// ── Gateways ──────────────────────────────────────────────────────
	// Multi-agent collaboration PRD phase C (docs/prds/multi-agent-collaboration.md).
	var startGateways []func()
	if cfg.Chat.Telegram.TokenEnv != "" {
		tgToken := os.Getenv(cfg.Chat.Telegram.TokenEnv)
		if tgToken == "" {
			log.Error("chat.telegram configured but token env var is empty", "env", cfg.Chat.Telegram.TokenEnv)
			return 1
		}
		// Deny-by-default: an empty allowlist blocks everyone. A bot handle
		// is public, so failing open would expose the chat agent's tools,
		// which run with this daemon's authority, to any stranger.
		if len(cfg.Chat.Telegram.AllowedUserIDs) == 0 {
			log.Warn("chat.telegram has no allowed_user_ids: every sender will be rejected. " +
				"Add your Telegram user id to chat.telegram.allowed_user_ids to enable the bot.")
		}
		tg := telegram.New(tgToken, "", "", cfg.Chat.Telegram.AllowedUserIDs, log)
		releaseAnnouncements := &releaseannounce.Announcer{
			StatePath: releaseAnnouncementStatePath(cfg.WorkDir, cfg.BotUser),
			Components: []releaseannounce.Component{
				{
					ID:            "gateway",
					Label:         "THE GATEWAY",
					Version:       gatewayVersion,
					ChangelogPath: packagedGatewayChangelogPath,
				},
				{
					ID:            "runtime",
					Label:         "THE RUNTIME",
					Version:       runtimeVersion,
					ChangelogPath: packagedRuntimeChangelogPath,
				},
			},
		}
		tg.ReleaseAnnouncements = releaseAnnouncements

		// /restart re-reads config from disk so allowlist and token edits
		// apply without restarting the daemon and killing running tasks.
		// Only gateway-scoped fields are refreshed; everything else needs a
		// full restart, since it was wired into the daemon at construction.
		tg.Reload = func(g *telegram.Gateway) error {
			newCfg, err := config.LoadOverlay(*cfgPath, *overlayPath)
			if err != nil {
				return fmt.Errorf("reload config: %w", err)
			}
			tokenEnv := newCfg.Chat.Telegram.TokenEnv
			if tokenEnv == "" {
				return fmt.Errorf("reload config: chat.telegram.token_env is unset")
			}
			token := os.Getenv(tokenEnv)
			if token == "" {
				return fmt.Errorf("reload config: %s is empty", tokenEnv)
			}
			g.Token = token
			g.AllowedUserIDs = newCfg.Chat.Telegram.AllowedUserIDs
			log.Info("chat gateway config reloaded",
				"allowed_user_ids", len(g.AllowedUserIDs))
			return nil
		}

		// Session store backed by the same NellDB log as task state.
		// Sessions and messages survive daemon restarts.
		var sessionStore gateway.SessionStore
		if nellAdapter, ok := st.(*nell.Adapter); ok {
			sessionStore = gateway.NewSessionStore(nellAdapter.Store(), cfg.BotUser)
		} else {
			sessionStore = gateway.NewSessionStoreMemory(cfg.BotUser)
		}

		// Build an LLM responder from the runtime. Gateway-local commands
		// (/status, /model, /spawn) are handled directly; all
		// other messages are routed through the LLM with conversation
		// history from the session store.
		var llmResponder gateway.LLMResponder
		var llmStream gateway.LLMStreamResponder
		if llm != nil {
			// respond runs one chat turn. When onDelta is non-nil the reply
			// is streamed and each fragment reported as it arrives; the
			// assembled text is returned either way, so both the blocking
			// and streaming responders share this single path.
			respond := func(ctx context.Context, msg gateway.Message, onDelta func(string)) (string, error) {
				sessionKey := sessionKey(msg)
				if err := sessionStore.SaveMessage(ctx, sessionKey, msg); err != nil {
					return "", fmt.Errorf("save inbound chat message: %w", err)
				}

				// Load recent conversation history for context.
				// Use a larger fetch window so compression has room to work
				// — older messages get summarised, recent ones stay intact.
				history, err := sessionStore.RecentMessages(ctx, sessionKey, 100)
				if err != nil {
					return "", fmt.Errorf("load chat history: %w", err)
				}
				compressed := make([]gateway.CompressedMessage, 0, len(history))
				for _, h := range history {
					role := "user"
					if h.From == cfg.BotUser {
						role = "assistant"
					}
					compressed = append(compressed, gateway.CompressedMessage{
						Role:    role,
						Content: h.Text,
					})
				}
				// Apply context compression for long-running sessions.
				compCfg := gateway.DefaultCompressionConfig()
				view := gateway.CompressHistory(compressed, compCfg)

				// Prepend persona prompt if one is active for this session.
				if personaPrompt := personas.GetActive(sessionKey); personaPrompt != "" {
					view.Messages = append(
						[]gateway.CompressedMessage{{Role: "system", Content: personaPrompt}},
						view.Messages...,
					)
				}

				// Convert to chat.Message for the LLM.
				messages := make([]chat.Message, len(view.Messages))
				for i, cm := range view.Messages {
					role := chat.RoleUser
					switch cm.Role {
					case "assistant":
						role = chat.RoleAssistant
					case "system":
						role = chat.RoleSystem
					}
					messages[i] = chat.Message{
						Role:    role,
						Content: cm.Content,
					}
				}

				options, err := chatGenerateOptions(messages, toolReg)
				if err != nil {
					return "", fmt.Errorf("build chat tools: %w", err)
				}
				chatModel := chatModels.ActiveModel()
				var text string
				if onDelta == nil {
					result, err := llm.Chat(ctx, chatModel, options)
					if err != nil {
						return "", fmt.Errorf("llm chat: %w", err)
					}
					text = result.Text
				} else {
					stream, err := llm.ChatStream(ctx, chatModel, options)
					if err != nil {
						return "", fmt.Errorf("llm chat stream: %w", err)
					}
					// Consume FullStream, not TextStream. Per the ai-sdk
					// channel contract FullStream is authoritative and MUST
					// be drained until close: its writes are synchronous, so
					// leaving it unread stalls the producer and deadlocks the
					// whole turn. TextStream is a best-effort view that drops
					// deltas when it is not being read, so it cannot be used
					// to reconstruct the reply either.
					var sb strings.Builder
					for part := range stream.FullStream {
						if part.Type != core.StreamPartTextDelta || part.TextDelta == "" {
							continue
						}
						sb.WriteString(part.TextDelta)
						onDelta(part.TextDelta)
					}
					// Resolves once the producer completes, so a mid-stream
					// failure surfaces here rather than a truncated reply
					// passing as a short but complete one.
					if _, err := stream.FinishReason(); err != nil {
						return "", fmt.Errorf("llm chat stream: %w", err)
					}
					text = sb.String()
				}

				// Persist the response.
				if err := sessionStore.SaveMessage(ctx, sessionKey, gateway.Message{
					From: cfg.BotUser,
					Text: text,
				}); err != nil {
					return "", fmt.Errorf("save outbound chat message: %w", err)
				}

				return text, nil
			}

			llmResponder = func(ctx context.Context, msg gateway.Message) (string, error) {
				return respond(ctx, msg, nil)
			}
			llmStream = func(ctx context.Context, msg gateway.Message, onDelta func(string)) (string, error) {
				return respond(ctx, msg, onDelta)
			}
		}

		router := gateway.NewRouter(st, llmResponder, "telegram")
		router.LLMStream = llmStream
		router.Models = chatModels
		configureTaskCommands(router, chatTasks, chatController, defaultChatIdentity)
		startGateways = append(startGateways, func() {
			go func() {
				if err := tg.Start(ctx, router); err != nil && ctx.Err() == nil {
					log.Error("telegram gateway stopped", "err", err)
				}
			}()
			log.Info("telegram gateway started")
		})
	}

	// ── Email gateway (optional) ───────────────────────────────────
	if cfg.Chat.Email.ListenAddr != "" {
		em := email.New(cfg.Chat.Email.ListenAddr, cfg.Chat.Email.RelayAddr, log)
		emRouter := gateway.NewRouter(st, nil, "email")
		configureTaskCommands(emRouter, chatTasks, chatController, defaultChatIdentity)
		startGateways = append(startGateways, func() {
			go func() {
				if err := em.Start(ctx, emRouter); err != nil && ctx.Err() == nil {
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
			go func() {
				if err := wh.Start(ctx, whRouter); err != nil && ctx.Err() == nil {
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
			agentRunner = agentexec.NewInProcessRunner(llm, log, toolReg)
		case "nats":
			if natsClient == nil {
				log.Error("agent.mode is nats but [nats] is not configured")
				return 1
			}
			agentRunner = &agentexec.NATSRunner{
				Nats:      natsClient,
				Providers: providers,
				Log:       log,
			}
		}
	}
	// Build the workflow registry from the skill catalog. Plugin-defined
	// workflows override built-ins of the same name; built-ins fill gaps.
	skillsBase := cfg.SkillsDir
	if skillsBase == "" {
		skillsBase = cfg.WorkDir
	}
	registry, err := skillbuild.BuildRegistry(skillsBase)
	if err != nil {
		log.Error("skill registry build failed", "err", err)
		return 1
	}
	log.Info("workflow registry built", "workflows", len(registry))

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
	// and Run() takes the legacy single-identity path unchanged.
	var identityRunners []*daemon.IdentityRunner
	for _, idCfg := range cfg.Identities {
		idToken, err := idCfg.Forge.Token.Resolve(secrets)
		if err != nil || idToken == "" {
			log.Error("identity forge token unresolved", "identity", idCfg.Name, "err", err)
			return 1
		}
		idForge, err := forge.New(idCfg.Forge.Type, idToken, idCfg.Forge.Host, log)
		if err != nil {
			log.Error("identity forge client failed", "identity", idCfg.Name, "err", err)
			return 1
		}
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
		identityRunners = append(identityRunners, runner)
		log.Info("identity configured", "identity", idCfg.Name, "bot_user", idCfg.BotUser, "repos", len(idCfg.Repos))
	}

	// Let archie-agent containers (which hold no DB connection, forge
	// token, or push credential) proxy Store/Forge/worktree operations
	// back to archied over NATS.
	if natsClient != nil {
		unsubscribe, err := registerTaskRPCServers(natsClient.Conn(), st, forgeClient, trees, log)
		if err != nil {
			log.Error("task RPC server registration failed", "err", err)
			return 1
		}
		defer unsubscribe()
	}

	// ── Memory manager ────────────────────────────────────────────────
	// Built-in file-backed provider (MEMORY.md + USER.md) under the
	// daemon work directory. External providers from config are added
	// via RegisterExternal when cfg.Memory.Provider is set.
	var memManager *memory.Manager
	memDir := filepath.Join(cfg.WorkDir, "memory")
	memProvider, err := builtin.New(builtin.Config{Dir: memDir})
	if err != nil {
		log.Error("memory provider init failed", "err", err)
		return 1
	}
	memManager, err = memory.NewManager(memProvider, nil)
	if err != nil {
		log.Error("memory manager init failed", "err", err)
		return 1
	}
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
	for _, srv := range cfg.Tools.MCPServers {
		provider, err := configuredMCPProvider(srv)
		if err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}
		if err := providerRegistry.Register(provider); err != nil {
			log.Error("mcp tool provider registration failed", "name", srv.Name, "err", err)
			return 1
		}
	}
	if err := capabilityHost.Register(providerRegistry); err != nil {
		log.Error("tool-provider capability registration failed", "err", err)
		return 1
	}

	// Skill catalog → skill_activate tool (progressive disclosure: the
	// catalog's name+description is always in the tool schema; the full
	// SKILL.md body loads only when the model activates one).
	if catalog, err := skill.Catalog(cfg.WorkDir); err != nil {
		log.Warn("skill catalog load failed", "err", err)
	} else if entry := skill.ActivateTool(cfg.WorkDir, catalog); entry != nil {
		if err := toolReg.Register(*entry); err != nil {
			log.Warn("skill_activate registration failed", "err", err)
		} else {
			log.Info("skill catalog registered", "skills", len(catalog))
		}
	}

	// Built-in tools are registered at startup via init(). Tool discovery
	// via Yaegi is deferred to the container/agent runtime.

	d := &daemon.Daemon{
		Cfg:            cfg,
		Store:          st,
		Bus:            bus,
		Forge:          forgeClient,
		Trees:          trees,
		Runtime:        llm,
		Agent:          agentRunner,
		Workflows:      registry,
		CapabilityHost: capabilityHost,
		Storage:        storeBackend,
		Log:            log,
		CustomStages:   wfeval.Discover,
		Nats:           natsClient,
		ContainerPool:  containerPool,
		Memory:         memManager,
		Guardrails:     guardrails,
		ToolRegistry:   toolReg,
		Identities:     identityRunners,
	}

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

	if err := d.Startup(ctx); err != nil {
		log.Error("startup", "err", err)
		return 1
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

type chatTaskWriterAdapter struct {
	enqueue func(
		ctx context.Context,
		owner, repo, title, body, workflow, identity string,
		issueNumber int,
	) (*store.Task, error)
}

func (a chatTaskWriterAdapter) EnqueueChatTask(
	ctx context.Context,
	owner, repo, title, body, workflow, identity string,
	issueNumber int,
) (int64, error) {
	task, err := a.enqueue(ctx, owner, repo, title, body, workflow, identity, issueNumber)
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
	return a.transition(ctx, taskID, task.Status, store.StatusRejected, reason)
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
func sessionKey(msg gateway.Message) string {
	if msg.ThreadID != "" {
		return msg.ChannelID + ":" + msg.ThreadID
	}
	return msg.ChannelID
}

func executionProviders(cfg config.Config) map[string]agentexec.Provider {
	return agentexec.ProvidersFromConfig(cfg.Providers)
}

// registerTaskRPCServers subscribes the storerpc/forgerpc/worktreerpc
// handlers on nc so an archie-agent container (which holds no DB
// connection, forge token, or push credential) can proxy those operations
// back to archied. The returned func unsubscribes all three.
func registerTaskRPCServers(nc *natsio.Conn, st store.TaskStore, forgeClient forge.Forge, trees *worktree.Manager, log *slog.Logger) (unsubscribe func(), err error) {
	storeServer := &storerpc.Server{Store: st, Log: log}
	unsubStore, err := storeServer.Register(nc)
	if err != nil {
		return nil, fmt.Errorf("register storerpc: %w", err)
	}

	forgeServer := &forgerpc.Server{Forge: forgeClient, Log: log}
	unsubForge, err := forgeServer.Register(nc)
	if err != nil {
		unsubStore()
		return nil, fmt.Errorf("register forgerpc: %w", err)
	}

	treesServer := &worktreerpc.Server{Trees: trees, Log: log}
	unsubTrees, err := treesServer.Register(nc)
	if err != nil {
		unsubStore()
		unsubForge()
		return nil, fmt.Errorf("register worktreerpc: %w", err)
	}

	return func() {
		unsubStore()
		unsubForge()
		unsubTrees()
	}, nil
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
