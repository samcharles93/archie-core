package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	channelruntime "github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/nell"
	"github.com/samcharles93/archie-core/internal/releaseannounce"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/tools"
)

// telegramSetup contains the inputs needed to initialise the Telegram
// chat gateway. Every field is intentionally explicit so the function
// signature acts as a contract of what the gateway depends on.
type telegramSetup struct {
	Cfg                 config.Config
	CfgPath             string
	OverlayPath         string
	St                  store.TaskStore
	LLM                 *runtime.Runtime
	ChatModels          gateway.ModelManager
	ToolReg             *tools.Registry
	Personas            *gateway.PersonaRegistry
	ChatTasks           gateway.TaskCreator
	ChatController      gateway.TaskController
	ChatTaskLister      gateway.ChatTaskLister
	DefaultChatIdentity string
	SessionStore        gateway.SessionStore
	Updates             telegram.UpdateService
	Dangerous           gateway.DangerousCommandAuthority
	RegisterRestart     func(func() error)
	// Bus carries primary-input events (archie-core-035): a completed
	// chat turn is published here so input-driven curators can wake. Nil
	// disables turn events (tests, minimal setups).
	Bus            *events.Bus
	Log            *slog.Logger
	ChannelManager *channelruntime.Manager
}

// setupTelegramGateway initialises the Telegram chat gateway when a
// token is configured. It returns a start function (nil when no token
// is configured) and ok=false to signal the caller must exit early.
func setupTelegramGateway(ctx context.Context, s telegramSetup) (start func(), ok bool) {
	if s.Cfg.Chat.Telegram.TokenEnv == "" {
		return nil, true
	}
	tgToken := os.Getenv(s.Cfg.Chat.Telegram.TokenEnv)
	if tgToken == "" {
		s.Log.Error("chat.telegram configured but token env var is empty", "env", s.Cfg.Chat.Telegram.TokenEnv)
		return nil, false
	}
	if len(s.Cfg.Chat.Telegram.AllowedUserIDs) == 0 {
		s.Log.Warn("chat.telegram has no allowed_user_ids: every sender will be rejected. " +
			"Add your Telegram user id to chat.telegram.allowed_user_ids to enable the bot.")
	}
	tg := telegram.New(tgToken, "", "", s.Cfg.Chat.Telegram.AllowedUserIDs, s.Log)
	if s.RegisterRestart != nil {
		s.RegisterRestart(tg.RequestRestart)
	}
	tg.Version = func() string {
		return fmt.Sprintf("Archie\nGateway: %s\nRuntime: %s", gatewayVersion, runtimeVersion)
	}
	if s.Updates != nil {
		tg.Updates = s.Updates
	} else {
		configureTelegramUpdates(tg, s)
	}
	tg.Dangerous = s.Dangerous
	tg.ReleaseAnnouncements = &releaseannounce.Announcer{
		StatePath: releaseAnnouncementStatePath(s.Cfg.WorkDir, s.Cfg.BotUser),
		Components: []releaseannounce.Component{
			{ID: "gateway", Label: "THE GATEWAY", Version: gatewayVersion, ChangelogPath: packagedGatewayChangelogPath},
			{ID: "runtime", Label: "THE RUNTIME", Version: runtimeVersion, ChangelogPath: packagedRuntimeChangelogPath},
		},
	}
	tg.Reload = makeTelegramReload(s)

	sessionStore := s.SessionStore
	if sessionStore == nil {
		sessionStore = makeTelegramSessionStore(s)
	}
	router := buildTelegramRouter(tg, s, sessionStore)
	return func() {
		if s.ChannelManager != nil {
			s.ChannelManager.MarkStarting("telegram")
		}
		go func() {
			if err := tg.Start(ctx, router); err != nil && ctx.Err() == nil {
				if s.ChannelManager != nil {
					s.ChannelManager.MarkFailed("telegram", err.Error())
				}
				s.Log.Error("telegram gateway stopped", "err", err)
			}
		}()
		s.Log.Info("telegram gateway started")
	}, true
}

func configureTelegramUpdates(tg *telegram.Gateway, s telegramSetup) {
	if updates := makeUpdateService(s); updates != nil {
		tg.Updates = updates
	}
}

func makeUpdateService(s telegramSetup) *releaseupdate.Service {
	if len(s.Cfg.Chat.Telegram.UpdateCheckCommand) == 0 {
		return nil
	}
	updates := &releaseupdate.Service{
		Catalog:   releaseupdate.CommandCatalog{Command: s.Cfg.Chat.Telegram.UpdateCheckCommand},
		StatePath: filepath.Join(s.Cfg.WorkDir, "telegram-update-deferrals.json"),
	}
	if len(s.Cfg.Chat.Telegram.UpdateInstallCommand) != 0 {
		updates.Installer = releaseupdate.CommandInstaller{Command: s.Cfg.Chat.Telegram.UpdateInstallCommand}
	}
	return updates
}

func makeTelegramReload(s telegramSetup) func(*telegram.Gateway) error {
	return func(g *telegram.Gateway) error {
		doc, err := configuration.New(s.Log).Resolve(s.CfgPath, s.OverlayPath)
		if err != nil {
			return fmt.Errorf("reload config: %w", err)
		}
		newCfg := doc.Config
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
		s.Log.Info("chat gateway config reloaded", "allowed_user_ids", len(g.AllowedUserIDs))
		return nil
	}
}

func makeTelegramSessionStore(s telegramSetup) gateway.SessionStore {
	if nellAdapter, ok := s.St.(*nell.Adapter); ok {
		return gateway.NewSessionStore(nellAdapter.Store(), s.Cfg.BotUser)
	}
	return gateway.NewSessionStoreMemory(s.Cfg.BotUser)
}

func buildTelegramRouter(tg *telegram.Gateway, s telegramSetup, sessionStore gateway.SessionStore) *gateway.Router {
	// The router is built before the responder so the responder can resolve
	// sessions through it. Both orderings work at runtime -- the responder is
	// a closure -- but this way the dependency is visible rather than
	// captured by reference.
	router := gateway.NewRouter(s.St, nil, "telegram")
	router.Models = s.ChatModels
	router.Updates = s.Updates
	router.InitSessions(sessionStore)
	router.Titles = newChatTitleGenerator(s)
	router.Log = s.Log
	configureTaskCommands(router, s.ChatTasks, s.ChatController, s.DefaultChatIdentity)

	if s.LLM != nil {
		turnRunner := newChatTurnRunner(tg.Name(), s, sessionStore, router)
		router.LLM = turnRunner.Respond
		router.LLMStream = turnRunner.RespondStream
	}
	return router
}

func makeChatLLMResponder(
	channel string,
	s telegramSetup,
	sessionStore gateway.SessionStore,
	router *gateway.Router,
) (gateway.LLMResponder, gateway.LLMStreamResponder) {
	if s.LLM == nil {
		return nil, nil
	}
	runner := newChatTurnRunner(channel, s, sessionStore, router)
	return runner.Respond, runner.RespondStream
}

func newChatTurnRunner(
	channel string,
	s telegramSetup,
	sessionStore gateway.SessionStore,
	router *gateway.Router,
) *gateway.TurnRunner {
	runner := gateway.NewTurnRunner(gateway.TurnRunnerConfig{
		Router:       router,
		Sessions:     sessionStore,
		Models:       s.ChatModels,
		Personas:     s.Personas,
		Model:        newChatTurnModel(s.LLM, s.ToolReg, s.Cfg.Chat.MaxSteps, toolLimits(s.Cfg)),
		TaskLister:   s.ChatTaskLister,
		Tasks:        s.ChatTasks,
		TaskIdentity: s.DefaultChatIdentity,
		Bus:          s.Bus,
		BotUser:      s.Cfg.BotUser,
		Channel:      channel,
		Operator:     s.Cfg.Chat.Operator,
		Log:          s.Log,
	})
	if err := runner.Recover(context.Background()); err != nil && s.Log != nil {
		s.Log.Error("recover chat turns", "channel", channel, "err", err)
	}
	return runner
}

func sendChatTurn(ctx context.Context, llm *runtime.Runtime, chatModel string, options core.GenerateOptions, onDelta func(string)) (string, error) {
	if onDelta == nil {
		result, err := llm.Chat(ctx, chatModel, options)
		if err != nil {
			return "", fmt.Errorf("llm chat: %w", err)
		}
		return result.Text, nil
	}
	stream, err := llm.ChatStream(ctx, chatModel, options)
	if err != nil {
		return "", fmt.Errorf("llm chat stream: %w", err)
	}
	var sb strings.Builder
	for part := range stream.FullStream {
		if part.Type != core.StreamPartTextDelta || part.TextDelta == "" {
			continue
		}
		sb.WriteString(part.TextDelta)
		onDelta(part.TextDelta)
	}
	if _, err := stream.FinishReason(); err != nil {
		return "", fmt.Errorf("llm chat stream: %w", err)
	}
	return sb.String(), nil
}

// chatTitleGenerator proposes session titles through the chat model. It
// is a pure proposal: it persists nothing, so a failed or slow title
// call can never corrupt conversation history or fail the turn. The
// gateway bounds the call with its own timeout; errors leave the session
// untitled and are logged here.
type chatTitleGenerator struct {
	log        *slog.Logger
	llm        *runtime.Runtime
	chatModels gateway.ModelManager
}

// newChatTitleGenerator wires an LLM-backed title generator for a chat
// setup, or nil when no model is available. Title generation is optional
// everywhere, so every caller tolerates nil.
func newChatTitleGenerator(s telegramSetup) gateway.TitleGenerator {
	if s.LLM == nil || s.ChatModels == nil {
		return nil
	}
	return &chatTitleGenerator{log: s.Log, llm: s.LLM, chatModels: s.ChatModels}
}

func (g *chatTitleGenerator) GenerateTitle(ctx context.Context, sessionID, firstMessage string) (string, error) {
	messages := []chat.Message{
		{Role: chat.RoleSystem, Content: gateway.TitleGenerationSystemPrompt},
		{Role: chat.RoleUser, Content: firstMessage},
	}
	// No tools: a title is a single completion. The active model is read
	// at call time so a /model switch applies to titles too.
	text, err := sendChatTurn(ctx, g.llm, g.chatModels.ActiveModel(),
		core.GenerateOptions{Messages: messages, MaxSteps: 1}, nil)
	if err != nil {
		if g.log != nil {
			g.log.Error("session title generation failed", "session", sessionID, "err", err)
		}
		return "", fmt.Errorf("generate title for session %s: %w", sessionID, err)
	}
	if g.log != nil {
		g.log.Info("session title generated", "session", sessionID)
	}
	return text, nil
}
