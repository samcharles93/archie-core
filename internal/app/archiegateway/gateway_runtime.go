package archiegateway

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/app/chattask"
	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/tools"
)

// setupChatRuntime wires the model runtime, tool registry, persona registry,
// chat task creator/controller, default chat identity and the release-update
// service. The daemon and the standalone Gateway build through this one
// constructor so the two processes cannot diverge.
func (b *server) setupChatRuntime(ctx context.Context, cfg config.Config, actor gateway.ChatTaskActor) error {
	// ── LLM runtime ──────────────────────────────────────────────────
	// Created before the router so the LLMResponder can be wired in for
	// non-command message processing.
	providers := executionProviders(cfg)
	b.setLLM(modelloop.NewRuntime(providers, cfg.ModelLimits))
	// Transcription is a model-role capability, like the chat models it sits
	// beside: it is built here, on the model-owning side, from this process's
	// own [models]/[providers]. The Messaging Service carries a voice note's
	// bytes to the Gateway and never holds the credential.
	b.setupTranscriber(cfg, b.log)
	b.toolReg = tools.NewRegistry()
	chatModels := newChatModelManager(cfg.Models, cfg.Chat.Models)
	catalog, catalogModels := b.catalogState()
	chatModels.SetModelCatalog(catalog, catalogModels)
	b.chatModels = chatModels

	// ── Persona registry ─────────────────────────────────────────────
	if b.personas == nil {
		personas, version, err := b.controlPlane.Personas(ctx)
		if err != nil {
			return fmt.Errorf("load personas: %w", err)
		}
		b.personas = gateway.NewPersonaRegistry(nil)
		b.applyPersonas(personas, version)
		b.applyStatus.Report(ctx, controlplane.PersonasKind, version, nil)
		if err := b.watchPersonas(ctx, version); err != nil {
			return fmt.Errorf("watch personas: %w", err)
		}
	}

	// ── SOUL ─────────────────────────────────────────────────────────
	if b.soul == nil {
		soul, version, err := b.controlPlane.Soul(ctx)
		if err != nil {
			return fmt.Errorf("load soul: %w", err)
		}
		b.soul = newSoulSource(b.cfgPath, b.log)
		b.applySoul(soul, version)
		b.applyStatus.Report(ctx, controlplane.SoulKind, version, nil)
		if err := b.watchSoul(ctx, version); err != nil {
			return fmt.Errorf("watch soul: %w", err)
		}
	}

	// ── Operator health surface ──────────────────────────────────────
	// Built before the gateways because every turn runner and router built
	// later carries both: the recorder is written by sendChatTurn, the source
	// is read by /status.
	b.providerOutcomes = newProviderOutcomeRecorder()
	b.statusHealth = newStatusHealth(b)

	b.setupChatTasks(cfg)
	b.chatController = gateway.NewStoreTaskController(chatTaskControllerAdapter{
		taskByID: b.stateStore.TaskByID,
		// Chat's /approve reaches the daemon's one task-action service rather
		// than its own requeue, so a chat approval and a dashboard approval
		// cannot record different decisions for one operator intent
		approve: func(ctx context.Context, scope *string, by taskactions.Actor, taskID int64, res taskactions.ActionPayload) error {
			_, err := actor.ApplyChatTaskAction(ctx, scope, by, taskID, taskstate.ActionApprove, res)
			return err
		},
		cancelExecution: b.stateStore.CancelExecution,
	})
	b.updateService = makeUpdateService(b.controlPlane, cfg)
	return nil
}

// setupChatTasks wires the task creator chat commands and scheduled workflows
// enqueue through.
func (b *server) setupChatTasks(cfg config.Config) {
	profiles, defaultChatIdentity := chattask.Profiles(cfg)
	if len(profiles) > 0 {
		b.chatTasks = gateway.NewStoreTaskCreatorForProfiles(
			chattask.Writer{Enqueue: b.stateStore.EnqueueChatTask},
			profiles,
		)
	}
	b.defaultChatIdentity = defaultChatIdentity
}

// watchStatus is KeepWatch carrying the outage bookkeeping: an applied
// refusal becomes the record's error, a stream failure and a stream end
// report the gap, and a re-established watch clears it, so a record never
// reads the boot version as current while no live updates can arrive.
func watchStatus[T any](
	ctx context.Context,
	report func(ctx context.Context, kind string, version int64, err error),
	log *slog.Logger,
	kind, label string, version int64, updates <-chan T,
	open func(ctx context.Context, after int64) (<-chan T, error),
	versionOf func(T) int64,
	errOf func(T) error,
	deliver func(T),
) {
	var lastApplyErr, streamErr error
	watchOpen := func(ctx context.Context, after int64) (<-chan T, error) {
		next, err := open(ctx, after)
		if err == nil {
			report(ctx, kind, after, lastApplyErr)
		}
		return next, err
	}
	go servicekit.KeepWatch(ctx, log, kind, version, updates,
		watchOpen,
		servicekit.WaitFor,
		versionOf,
		func(update T) {
			if err := errOf(update); err != nil {
				if v := versionOf(update); v > 0 {
					lastApplyErr = err
					report(ctx, kind, v, err)
				} else {
					streamErr = err
				}
				log.Error("control-plane watch failed", "kind", kind, "err", err)
				return
			}
			deliver(update)
		},
		func() {
			// A refusal already on the record outranks the outage; it is the
			// state the settings are actually in.
			if lastApplyErr != nil {
				return
			}
			if streamErr == nil {
				streamErr = fmt.Errorf("control-plane watch stream ended")
			}
			report(ctx, kind, 0, fmt.Errorf("%s watch unavailable: %w", label, streamErr))
			streamErr = nil
		})
}

// watchPersonas keeps the persona stream established for the life of the
// process. The first stream is opened synchronously, so a control plane that
// cannot be watched at all fails the boot that asked for it rather than
// leaving the process running personas it can no longer update; after that the
// watch reconnects instead of ending (see servicekit.KeepWatch).
func (b *server) watchPersonas(ctx context.Context, version int64) error {
	updates, err := b.controlPlane.WatchPersonas(ctx, version)
	if err != nil {
		return err
	}
	watchStatus(ctx, b.applyStatus.Report, b.log,
		controlplane.PersonasKind, "personas", version, updates,
		b.controlPlane.WatchPersonas,
		func(update controlplane.AppliedPersonas) int64 { return update.Version },
		func(update controlplane.AppliedPersonas) error { return update.Err },
		func(update controlplane.AppliedPersonas) {
			b.applyPersonas(update.Collection, update.Version)
			b.applyStatus.Report(ctx, controlplane.PersonasKind, update.Version, nil)
		})
	return nil
}

func (b *server) applyPersonas(collection agent.PersonaCollection, version int64) {
	personas := make([]gateway.Persona, 0, len(collection.Personas))
	for _, persona := range collection.Personas {
		personas = append(personas, gateway.Persona{Name: persona.Name, Prompt: persona.Prompt})
	}
	b.personas.Replace(personas, collection.Default)
	b.log.Info("personas applied", "version", version)
}

// watchSoul keeps the soul stream established for the life of the process, so
// a dashboard edit reaches the next turn without a restart. The stream rides
// the same outage bookkeeping as personas (watchStatus).
func (b *server) watchSoul(ctx context.Context, version int64) error {
	updates, err := b.controlPlane.WatchSoul(ctx, version)
	if err != nil {
		return err
	}
	watchStatus(ctx, b.applyStatus.Report, b.log,
		controlplane.SoulKind, "soul", version, updates,
		b.controlPlane.WatchSoul,
		func(update controlplane.AppliedSoul) int64 { return update.Version },
		func(update controlplane.AppliedSoul) error { return update.Err },
		func(update controlplane.AppliedSoul) {
			b.applySoul(update.Soul, update.Version)
			b.applyStatus.Report(ctx, controlplane.SoulKind, update.Version, nil)
		})
	return nil
}

func (b *server) applySoul(soul agent.Soul, version int64) {
	b.soul.apply(soul.Text)
	b.log.Info("soul applied", "version", version)
}

// setupGatewayChat is the sole production constructor of the local contract.
// Frontends use its gRPC representation; only this service owns the router.
func (b *server) setupGatewayChat(ctx context.Context, actor gateway.ChatTaskActor) (gateway.ChatContract, error) {
	if err := b.setupChatRuntime(ctx, b.cfg, actor); err != nil {
		return nil, err
	}
	cfg := b.cfg
	router := gateway.NewRouter(b.stateStore, nil, "web")
	router.Limiter = b.rateLimiter
	router.Version = fmt.Sprintf("Archie\nGateway: %s\nRuntime: %s", buildinfo.Version, buildinfo.Runtime)
	router.Models = b.chatModels
	router.Personas = b.personas
	router.Updates = b.updateService
	router.InitSessions(b.chatSessionStore)
	configureTaskCommands(router, b.chatTasks, b.chatController, chatTaskListerAdapter{tasks: b.stateStore.Tasks}, b.defaultChatIdentity)
	router.Health = b.statusHealth
	// ToolLimits reads the live holder, not cfg: the runtime-resource
	// watch republishes a tool-settings change through it, so a policy edit
	// applies to the next turn in the process that serves this runner.
	setup := chatSetup{
		Cfg:        config.NewHolder(cfg),
		ToolLimits: func() modelloop.ToolLimits { return toolLimits(b.cfgHolder.Get()) },
		LLM:        b.chatLLM, ChatModels: b.chatModels, ToolReg: b.toolReg,
		Soul: b.soul, ChatTasks: b.chatTasks,
		ChatTaskLister:      chatTaskListerAdapter{tasks: b.stateStore.Tasks},
		ChatTaskLogs:        chatTaskLogReaderAdapter{tasks: b.stateStore.TaskByID, taskLogs: b.taskLogs},
		ChatTaskActor:       actor,
		DefaultChatIdentity: b.defaultChatIdentity,
		Bus:                 b.bus, Log: b.log,
		MemoryEngine: b.memoryStore(),
		MemoryWriter: b.memoryWriter(),
		// The transcription capability is built beside the chat runtime, from
		// the model-role configuration only this process reads.
		Transcriber: b.transcriber,
		// The Gateway executes the web chat's turns, so it is the process
		// that must record their outcomes for /status.
		ProviderOutcomes: b.providerOutcomes,
	}
	router.LLM, router.LLMStream = makeChatLLMResponder(ctx, "web", setup, b.chatSessionStore, router)
	router.Titles = newChatTitleGenerator(setup)
	router.Log = b.log
	return &gateway.LocalChatAdapter{
		Router: router, Sessions: b.chatSessionStore, Turns: gateway.NewTurns(b.log),
		Models: b.chatModels, Personas: b.personas, TaskActor: actor, Tasks: b.stateStore,
		VoiceAvailable: b.transcriber != nil,
	}, nil
}
