package archied

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/tools"
)

// setupChatRuntime wires the model runtime, tool registry, persona registry,
// chat task creator/controller, default chat identity and the release-update
// service. Both the daemon (its own Telegram/email routers) and the standalone
// Gateway (the web router) build through this one constructor so the two
// processes cannot diverge.
func (b *boot) setupChatRuntime(cfg config.Config) {
	// ── LLM runtime ──────────────────────────────────────────────────
	// Created before gateways so the LLMResponder can be wired into the
	// Telegram router for non-command message processing.
	providers := executionProviders(cfg)
	b.llm = agentexec.NewRuntime(providers)
	b.toolReg = tools.NewRegistry()
	chatModels := newChatModelManager(cfg.Models, cfg.Chat.Models, b.catalogModels)
	chatModels.ApplyModelCatalog(b.catalog)
	b.chatModels = chatModels

	// ── Persona registry ─────────────────────────────────────────────
	b.personas = gateway.NewPersonaRegistry(gateway.DefaultPersonas())

	profiles, defaultChatIdentity := chatTaskProfiles(cfg)
	var chatTasks gateway.TaskCreator
	if len(profiles) > 0 {
		chatTasks = gateway.NewStoreTaskCreatorForProfiles(
			chatTaskWriterAdapter{enqueue: b.stateStore.EnqueueChatTask},
			profiles,
		)
	}
	b.chatTasks = chatTasks
	b.defaultChatIdentity = defaultChatIdentity
	b.chatController = gateway.NewStoreTaskController(chatTaskControllerAdapter{
		taskByID:   b.stateStore.TaskByID,
		requeue:    b.stateStore.Requeue,
		transition: b.stateStore.Transition,
	})
	b.updateService = makeUpdateService(telegramSetup{Cfg: config.NewHolder(cfg)})
}

// setupGatewayChat is the sole production constructor of the local contract.
// Frontends use its gRPC representation; only this service owns the router.
func (b *boot) setupGatewayChat(ctx context.Context, actor gateway.ChatTaskActor) gateway.ChatContract {
	b.setupChatRuntime(b.cfg)
	cfg := b.cfg
	router := gateway.NewRouter(b.stateStore, nil, "web")
	router.Version = fmt.Sprintf("Archie\nGateway: %s\nRuntime: %s", gatewayVersion, runtimeVersion)
	router.Models = b.chatModels
	router.Personas = b.personas
	router.Updates = b.updateService
	router.InitSessions(b.chatSessionStore)
	configureTaskCommands(router, b.chatTasks, b.chatController, chatTaskListerAdapter{tasks: b.stateStore.Tasks}, b.defaultChatIdentity)
	setup := telegramSetup{
		Cfg: config.NewHolder(cfg), St: b.stateStore, LLM: b.llm, ChatModels: b.chatModels, ToolReg: b.toolReg,
		Personas: b.personas, ChatTasks: b.chatTasks, ChatController: b.chatController,
		ChatTaskLister: chatTaskListerAdapter{tasks: b.stateStore.Tasks},
		ChatTaskLogs:   chatTaskLogReaderAdapter{tasks: b.stateStore.TaskByID, taskLogs: b.taskLogs},
		ChatTaskActor:  actor, DefaultChatIdentity: b.defaultChatIdentity, SessionStore: b.chatSessionStore,
		Bus: b.bus, Log: b.log, Secrets: b.secrets,
	}
	router.LLM, router.LLMStream = makeChatLLMResponder(ctx, "web", setup, b.chatSessionStore, router)
	router.Titles = newChatTitleGenerator(setup)
	router.Log = b.log
	return &gateway.LocalChatAdapter{
		Router: router, Sessions: b.chatSessionStore, Turns: gateway.NewTurns(b.log),
		Models: b.chatModels, Personas: b.personas, TaskActor: actor,
	}
}
