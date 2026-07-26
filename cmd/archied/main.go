// Command archied is the archie orchestrator daemon: it watches GitHub
// for issues labelled for archie, works each one in an isolated
// worktree through its routed workflow, and opens pull requests for
// human review.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"net"
	"strconv"
	"syscall"

	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"

	"github.com/moby/moby/client"
	"github.com/samcharles93/archie-core/internal/agentexec"
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

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
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

	token := os.Getenv(cfg.Forge.TokenEnv)
	if token == "" {
		fmt.Fprintln(os.Stderr, cfg.Forge.TokenEnv+" is required")
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

	// ── Persona registry ─────────────────────────────────────────────
	personas := gateway.NewPersonaRegistry(gateway.DefaultPersonas())

	// ── Gateways ──────────────────────────────────────────────────────
	// Multi-agent collaboration PRD phase C (docs/prds/multi-agent-collaboration.md).
	if cfg.Chat.Telegram.TokenEnv != "" {
		tgToken := os.Getenv(cfg.Chat.Telegram.TokenEnv)
		if tgToken == "" {
			log.Error("chat.telegram configured but token env var is empty", "env", cfg.Chat.Telegram.TokenEnv)
			return 1
		}
		tg := telegram.New(tgToken, "", "", nil, log)

		// Session store backed by the same NellDB log as task state.
		// Sessions and messages survive daemon restarts.
		var sessionStore gateway.SessionStore
		if nellAdapter, ok := st.(*nell.Adapter); ok {
			sessionStore = gateway.NewSessionStore(nellAdapter.Store(), cfg.BotUser)
		} else {
			sessionStore = gateway.NewSessionStoreMemory(cfg.BotUser)
		}

		// Build an LLM responder from the runtime. Gateway-local commands
		// (/status, /models, /model, /spawn) are handled directly; all
		// other messages are routed through the LLM with conversation
		// history from the session store.
		var llmResponder gateway.LLMResponder
		if llm != nil {
			chatModel := cfg.Models["chat"]
			if chatModel == "" {
				chatModel = cfg.Models["builder"]
			}
			llmResponder = func(ctx context.Context, msg gateway.Message) (string, error) {
				sessionKey := sessionKey(msg)
				_ = sessionStore.SaveMessage(ctx, sessionKey, msg)

				// Load recent conversation history for context.
				// Use a larger fetch window so compression has room to work
				// — older messages get summarised, recent ones stay intact.
				history, _ := sessionStore.RecentMessages(ctx, sessionKey, 100)
				compressed := make([]gateway.CompressedMessage, 0, len(history)+1)
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
				compressed = append(compressed, gateway.CompressedMessage{
					Role:    "user",
					Content: msg.Text,
				})

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

				result, err := llm.Chat(ctx, chatModel, core.GenerateOptions{
					Messages: messages,
					MaxSteps: 1,
				})
				if err != nil {
					return "", fmt.Errorf("llm chat: %w", err)
				}

				// Persist the response.
				_ = sessionStore.SaveMessage(ctx, sessionKey, gateway.Message{
					From: cfg.BotUser,
					Text: result.Text,
				})

				return result.Text, nil
			}
		}

		router := gateway.NewRouter(st, llmResponder, "telegram")
		if len(cfg.Repos) > 0 {
			router.Tasks = gateway.NewStoreTaskCreator(st, cfg.Repos[0].Owner, cfg.Repos[0].Name)
		}
		go func() {
			if err := tg.Start(ctx, router); err != nil && ctx.Err() == nil {
				log.Error("telegram gateway stopped", "err", err)
			}
		}()
		log.Info("telegram gateway started")
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
		go func() {
			if err := wh.Start(ctx, whRouter); err != nil && ctx.Err() == nil {
				log.Error("webhook gateway stopped", "err", err)
			}
		}()
		log.Info("webhook gateway started", "addr", fmt.Sprintf("%s:%d", host, port))
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
			agentRunner = agentexec.NewInProcessRunner(llm, log)
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
	var pluginReg *plugin.Registry
	if cfg.PluginDir != "" {
		plugins, err := plugin.LoadDir(cfg.PluginDir, pluginextract.Symbols)
		if err != nil {
			log.Error("plugin load failed", "dir", cfg.PluginDir, "err", err)
			return 1
		}
		pluginReg = &plugin.Registry{}
		for _, p := range plugins {
			pluginReg.Register(p)
			name, version := safePluginInfo(p)
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
	toolReg := tools.NewRegistry()
	for _, srv := range cfg.Tools.MCPServers {
		transport := mcp.NewStdioTransport(mcp.StdioTransportConfig{
			Command: srv.Command,
			Args:    srv.Args,
		})
		if err := transport.Start(context.Background()); err != nil {
			log.Error("mcp server start failed", "name", srv.Name, "err", err)
			continue
		}
		log.Info("mcp server started", "name", srv.Name)
	}
	// Built-in tools and MCP-discovered tools are registered at startup
	// via init() and the MCP transport. Tool discovery via Yaegi is
	// deferred to the container/agent runtime.

	d := &daemon.Daemon{
		Cfg:            cfg,
		Store:          st,
		Bus:            bus,
		Forge:          forgeClient,
		Trees:          trees,
		Runtime:        llm,
		Agent:          agentRunner,
		Workflows:      registry,
		PluginRegistry: pluginReg,
		Storage:        storeBackend,
		Log:            log,
		CustomStages:   wfeval.Discover,
		Nats:           natsClient,
		ContainerPool:  containerPool,
		Memory:         memManager,
		Guardrails:     guardrails,
		ToolRegistry:   toolReg,
	}

	if err := d.Startup(ctx); err != nil {
		log.Error("startup", "err", err)
		return 1
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
