package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
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
	DefaultChatIdentity string
	Log                 *slog.Logger
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
	tg.Version = func() string {
		return fmt.Sprintf("Archie\nGateway: %s\nRuntime: %s", gatewayVersion, runtimeVersion)
	}
	configureTelegramUpdates(tg, s)
	tg.ReleaseAnnouncements = &releaseannounce.Announcer{
		StatePath: releaseAnnouncementStatePath(s.Cfg.WorkDir, s.Cfg.BotUser),
		Components: []releaseannounce.Component{
			{ID: "gateway", Label: "THE GATEWAY", Version: gatewayVersion, ChangelogPath: packagedGatewayChangelogPath},
			{ID: "runtime", Label: "THE RUNTIME", Version: runtimeVersion, ChangelogPath: packagedRuntimeChangelogPath},
		},
	}
	tg.Reload = makeTelegramReload(s)

	sessionStore := makeTelegramSessionStore(s)
	router := buildTelegramRouter(ctx, tg, s, sessionStore)
	return func() {
		go func() {
			if err := tg.Start(ctx, router); err != nil && ctx.Err() == nil {
				s.Log.Error("telegram gateway stopped", "err", err)
			}
		}()
		s.Log.Info("telegram gateway started")
	}, true
}

func configureTelegramUpdates(tg *telegram.Gateway, s telegramSetup) {
	if len(s.Cfg.Chat.Telegram.UpdateCheckCommand) == 0 {
		return
	}
	updates := &releaseupdate.Service{
		Catalog:   releaseupdate.CommandCatalog{Command: s.Cfg.Chat.Telegram.UpdateCheckCommand},
		StatePath: filepath.Join(s.Cfg.WorkDir, "telegram-update-deferrals.json"),
	}
	if len(s.Cfg.Chat.Telegram.UpdateInstallCommand) != 0 {
		updates.Installer = releaseupdate.CommandInstaller{Command: s.Cfg.Chat.Telegram.UpdateInstallCommand}
	}
	tg.Updates = updates
}

func makeTelegramReload(s telegramSetup) func(*telegram.Gateway) error {
	return func(g *telegram.Gateway) error {
		doc, err := configuration.New(s.Log).Overlay(s.CfgPath, s.OverlayPath)
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

func buildTelegramRouter(ctx context.Context, tg *telegram.Gateway, s telegramSetup, sessionStore gateway.SessionStore) *gateway.Router {
	// The router is built before the responder so the responder can resolve
	// sessions through it. Both orderings work at runtime -- the responder is
	// a closure -- but this way the dependency is visible rather than
	// captured by reference.
	router := gateway.NewRouter(s.St, nil, "telegram")
	router.Models = s.ChatModels
	router.InitSessions(sessionStore)
	configureTaskCommands(router, s.ChatTasks, s.ChatController, s.DefaultChatIdentity)

	router.LLM, router.LLMStream = makeTelegramLLMResponder(ctx, tg, s, sessionStore, router)
	return router
}

func makeTelegramLLMResponder(ctx context.Context, tg *telegram.Gateway, s telegramSetup, sessionStore gateway.SessionStore, router *gateway.Router) (gateway.LLMResponder, gateway.LLMStreamResponder) {
	if s.LLM == nil {
		return nil, nil
	}
	respond := func(ctx context.Context, msg gateway.Message, onDelta func(string)) (string, error) {
		// Resolve through the router's session tracker, not a local
		// channel-keyed helper. They agree until /new, /branch or /resume
		// assigns a session a generated id; from then on the commands and
		// the conversation address different histories, so /new would report
		// history cleared while the next turn still saw all of it.
		sk, err := router.ResolveSessionKey(ctx, msg)
		if err != nil {
			return "", fmt.Errorf("resolve chat session: %w", err)
		}
		if err := sessionStore.SaveMessage(ctx, sk, msg); err != nil {
			return "", fmt.Errorf("save inbound chat message: %w", err)
		}
		history, err := sessionStore.RecentMessages(ctx, sk, 100)
		if err != nil {
			return "", fmt.Errorf("load chat history: %w", err)
		}
		// Chat turns were entirely unlogged, so a report of lost context had
		// no evidence to check against: session id, history depth and the
		// resolved persona are the three things needed to tell "wrong
		// session" from "history not loaded".
		s.Log.Info("chat turn",
			"session", sk,
			"channel", msg.ChannelID,
			"thread", msg.ThreadID,
			"history_messages", len(history))
		compressed := make([]gateway.CompressedMessage, 0, len(history))
		for _, h := range history {
			role := "user"
			if h.From == s.Cfg.BotUser {
				role = "assistant"
			}
			compressed = append(compressed, gateway.CompressedMessage{Role: role, Content: h.Text})
		}
		view := gateway.CompressHistory(compressed, gateway.DefaultCompressionConfig())
		options, err := chatGenerateOptions(nil, s.ToolReg, s.Cfg.Chat.MaxSteps)
		if err != nil {
			return "", fmt.Errorf("build chat tools: %w", err)
		}
		chatModel := s.ChatModels.ActiveModel()
		systemPrompt := gateway.BuildSystemPrompt(gateway.SystemPromptConfig{
			Persona: s.Personas.GetActive(sk), Tools: toolSummaries(options.Tools),
			Channel: tg.Name(), Model: chatModel, SessionID: sk, Now: time.Now(),
			Operator: s.Cfg.Chat.Operator,
		})
		view.Messages = append(
			[]gateway.CompressedMessage{{Role: "system", Content: systemPrompt}}, view.Messages...,
		)
		messages := make([]chat.Message, len(view.Messages))
		for i, cm := range view.Messages {
			role := chat.RoleUser
			switch cm.Role {
			case "assistant":
				role = chat.RoleAssistant
			case "system":
				role = chat.RoleSystem
			}
			messages[i] = chat.Message{Role: role, Content: cm.Content}
		}
		options.Messages = messages
		text, err := sendChatTurn(ctx, s.LLM, chatModel, options, onDelta)
		if err != nil {
			return "", err
		}
		if err := sessionStore.SaveMessage(ctx, sk, gateway.Message{From: s.Cfg.BotUser, Text: text}); err != nil {
			return "", fmt.Errorf("save outbound chat message: %w", err)
		}
		return text, nil
	}
	return func(ctx context.Context, msg gateway.Message) (string, error) {
			return respond(ctx, msg, nil)
		}, func(ctx context.Context, msg gateway.Message, onDelta func(string)) (string, error) {
			return respond(ctx, msg, onDelta)
		}
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
