package archied

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/tools"
)

// setupChatRuntime wires the model runtime, tool registry, persona registry,
// chat task creator/controller, default chat identity and the release-update
// service. The daemon and the standalone Gateway build through this one
// constructor so the two processes cannot diverge.
func (b *boot) setupChatRuntime(ctx context.Context, cfg config.Config) error {
	// ── LLM runtime ──────────────────────────────────────────────────
	// Created before the router so the LLMResponder can be wired in for
	// non-command message processing.
	providers := executionProviders(cfg)
	b.llm = agentexec.NewRuntime(providers)
	b.toolReg = tools.NewRegistry()
	chatModels := newChatModelManager(cfg.Models, cfg.Chat.Models, b.catalogModels)
	chatModels.ApplyModelCatalog(b.catalog)
	b.chatModels = chatModels

	// ── Persona registry ─────────────────────────────────────────────
	if b.personas == nil {
		personas, version, err := b.controlPlane.Personas(ctx)
		if err != nil {
			return fmt.Errorf("load personas: %w", err)
		}
		b.personas = gateway.NewPersonaRegistry(nil)
		b.applyPersonas(personas, version)
		updates, err := b.controlPlane.WatchPersonas(ctx, version)
		if err != nil {
			return fmt.Errorf("watch personas: %w", err)
		}
		go func() {
			for update := range updates {
				if update.Err != nil {
					b.log.Error("persona watch failed", "err", update.Err)
					return
				}
				b.applyPersonas(update.Collection, update.Version)
			}
		}()
	}

	// ── Operator health surface ──────────────────────────────────────
	// Built before the gateways because every turn runner and router built
	// later carries both: the recorder is written by sendChatTurn, the source
	// is read by /status. See newStatusHealth for why the source reads boot's
	// fields lazily.
	b.providerOutcomes = newProviderOutcomeRecorder()
	b.statusHealth = newStatusHealth(b)

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
	b.updateService = makeUpdateService(chatSetup{Cfg: config.NewHolder(cfg)})
	return nil
}

func (b *boot) applyPersonas(collection agent.PersonaCollection, version int64) {
	personas := make([]gateway.Persona, 0, len(collection.Personas))
	for _, persona := range collection.Personas {
		personas = append(personas, gateway.Persona{Name: persona.Name, Prompt: persona.Prompt})
	}
	b.personas.Replace(personas, collection.Default)
	b.log.Info("personas applied", "version", version)
}

// setupGatewayChat is the sole production constructor of the local contract.
// Frontends use its gRPC representation; only this service owns the router.
func (b *boot) setupGatewayChat(ctx context.Context, actor gateway.ChatTaskActor) (gateway.ChatContract, error) {
	if err := b.setupChatRuntime(ctx, b.cfg); err != nil {
		return nil, err
	}
	cfg := b.cfg
	router := gateway.NewRouter(b.stateStore, nil, "web")
	router.Limiter = b.rateLimiter
	router.Version = fmt.Sprintf("Archie\nGateway: %s\nRuntime: %s", gatewayVersion, runtimeVersion)
	router.Models = b.chatModels
	router.Personas = b.personas
	router.Updates = b.updateService
	router.InitSessions(b.chatSessionStore)
	configureTaskCommands(router, b.chatTasks, b.chatController, chatTaskListerAdapter{tasks: b.stateStore.Tasks}, b.defaultChatIdentity)
	router.Health = b.statusHealth
	setup := chatSetup{
		Cfg: config.NewHolder(cfg), LLM: b.llm, ChatModels: b.chatModels, ToolReg: b.toolReg,
		Personas: b.personas, ChatTasks: b.chatTasks,
		ChatTaskLister: chatTaskListerAdapter{tasks: b.stateStore.Tasks},
		ChatTaskLogs:   chatTaskLogReaderAdapter{tasks: b.stateStore.TaskByID, taskLogs: b.taskLogs},
		ChatTaskActor:  actor, ChatPRReviewer: b.prReviewer(),
		DefaultChatIdentity: b.defaultChatIdentity,
		Bus:                 b.bus, Log: b.log,
		MemoryEngine: b.memoryStore(),
		MemoryWriter: b.memoryWriter(),
		// The Gateway executes the web chat's turns, so it is the process
		// that must record their outcomes for /status.
		ProviderOutcomes: b.providerOutcomes,
	}
	router.LLM, router.LLMStream = makeChatLLMResponder(ctx, "web", setup, b.chatSessionStore, router)
	router.Titles = newChatTitleGenerator(setup)
	router.Log = b.log
	return &gateway.LocalChatAdapter{
		Router: router, Sessions: b.chatSessionStore, Turns: gateway.NewTurns(b.log),
		Models: b.chatModels, Personas: b.personas, TaskActor: actor,
	}, nil
}
