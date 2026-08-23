package main

import (
	"context"
	"encoding/json"
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
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/releaseannounce"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/tools"
)

// telegramSetup contains the inputs needed to initialise the Telegram
// chat gateway. Every field is intentionally explicit so the function
// signature acts as a contract of what the gateway depends on.
type telegramSetup struct {
	// Cfg is read once per setup function via Get(); the daemon is not
	// running yet at this point, so the reload-safe Holder is mostly a
	// formality, but matching the daemon's API keeps callers honest.
	Cfg                 *config.Holder
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
	ChatTaskLogs        gateway.ChatTaskLogReader
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
	cfg := s.Cfg.Get()
	if cfg.Chat.Telegram.TokenEnv == "" {
		return nil, true
	}
	tgToken := os.Getenv(cfg.Chat.Telegram.TokenEnv)
	if tgToken == "" {
		s.Log.Error("chat.telegram configured but token env var is empty", "env", cfg.Chat.Telegram.TokenEnv)
		return nil, false
	}
	if len(cfg.Chat.Telegram.AllowedUserIDs) == 0 {
		s.Log.Warn("chat.telegram has no allowed_user_ids: every sender will be rejected. " +
			"Add your Telegram user id to chat.telegram.allowed_user_ids to enable the bot.")
	}
	tg := telegram.New(tgToken, "", "", cfg.Chat.Telegram.AllowedUserIDs, s.Log)
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
	tg.SetShowToolCalls(cfg.Chat.ShowToolCalls)
	tg.ReleaseAnnouncements = &releaseannounce.Announcer{
		StatePath: releaseAnnouncementStatePath(cfg.WorkDir, cfg.BotUser),
		Components: []releaseannounce.Component{
			{ID: "gateway", Label: "THE GATEWAY", Version: gatewayVersion, ChangelogPath: packagedGatewayChangelogPath},
			{ID: "runtime", Label: "THE RUNTIME", Version: runtimeVersion, ChangelogPath: packagedRuntimeChangelogPath},
		},
	}
	tg.UpdateReportPath = updateReportPath(cfg.WorkDir, cfg.BotUser)
	tg.RunningVersions = daemonRunningVersions
	tg.Reload = makeTelegramReload(s)

	sessionStore := s.SessionStore
	if sessionStore == nil {
		s.Log.Error("telegram conversation store is not configured")
		return nil, false
	}
	router := buildTelegramRouter(ctx, tg, s, sessionStore)
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

// daemonRunningVersions reports the component versions this process can
// vouch for from its own build, for checking a pending update report against
// (see releaseupdate.Report.Verify).
//
// Only the daemon is listed, and that is the whole point: gatewayVersion is
// compiled into this binary, so if an installer claims it put a version into
// service and this value disagrees, the installer is wrong. runtimeVersion
// is deliberately NOT reported for the agent -- it records the agent version
// archied's own release pipeline stamped, not the version the agent
// container is actually running, and the two diverge in exactly the
// situation this check exists to detect. An unverifiable component is left
// out so it is reported as unchecked rather than as confirmed.
func daemonRunningVersions() map[string]string {
	return map[string]string{releaseupdate.ComponentDaemon: gatewayVersion}
}

func configureTelegramUpdates(tg *telegram.Gateway, s telegramSetup) {
	if updates := makeUpdateService(s); updates != nil {
		tg.Updates = updates
	}
}

func makeUpdateService(s telegramSetup) *releaseupdate.Service {
	cfg := s.Cfg.Get()
	if len(cfg.Chat.Telegram.UpdateCheckCommand) == 0 {
		return nil
	}
	updates := &releaseupdate.Service{
		Catalog:     releaseupdate.CommandCatalog{Command: cfg.Chat.Telegram.UpdateCheckCommand},
		StatePath:   filepath.Join(cfg.WorkDir, "telegram-update-deferrals.json"),
		InstallType: installtype.Type(),
	}
	if len(cfg.Chat.Telegram.UpdateInstallCommand) != 0 {
		updates.Installer = releaseupdate.CommandInstaller{Command: cfg.Chat.Telegram.UpdateInstallCommand}
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
		g.SetShowToolCalls(newCfg.Chat.ShowToolCalls)
		s.Log.Info("chat gateway config reloaded",
			"allowed_user_ids", len(g.AllowedUserIDs),
			"show_tool_calls", g.ShowToolCalls())
		return nil
	}
}

func makeTelegramSessionStore(cfg config.Config) (gateway.SessionStore, error) {
	return gateway.OpenSQLiteSessionStore(conversationDBPath(cfg.DBPath))
}

// conversationDBPath keeps conversation state in its own SQLite database.
func conversationDBPath(taskDBPath string) string {
	return taskDBPath + "-conversations.sqlite"
}

func buildTelegramRouter(ctx context.Context, tg *telegram.Gateway, s telegramSetup, sessionStore gateway.SessionStore) *gateway.Router {
	// The router is built before the responder so the responder can resolve
	// sessions through it. Both orderings work at runtime -- the responder is
	// a closure -- but this way the dependency is visible rather than
	// captured by reference.
	router := gateway.NewRouter(s.St, nil, "telegram")
	router.Models = s.ChatModels
	router.Updates = s.Updates
	router.Personas = s.Personas
	router.InitSessions(sessionStore)
	router.Titles = newChatTitleGenerator(s)
	router.Log = s.Log
	configureTaskCommands(router, s.ChatTasks, s.ChatController, s.DefaultChatIdentity)

	if s.LLM != nil {
		turnRunner := newChatTurnRunner(ctx, tg.Name(), s, sessionStore, router)
		router.LLM = turnRunner.Respond
		router.LLMStream = turnRunner.RespondStream
	}
	return router
}

func makeChatLLMResponder(
	ctx context.Context,
	channel string,
	s telegramSetup,
	sessionStore gateway.SessionStore,
	router *gateway.Router,
) (gateway.LLMResponder, gateway.LLMStreamResponder) {
	if s.LLM == nil {
		return nil, nil
	}
	runner := newChatTurnRunner(ctx, channel, s, sessionStore, router)
	return runner.Respond, runner.RespondStream
}

func newChatTurnRunner(
	ctx context.Context,
	channel string,
	s telegramSetup,
	sessionStore gateway.SessionStore,
	router *gateway.Router,
) *gateway.TurnRunner {
	cfg := s.Cfg.Get()
	runner := gateway.NewTurnRunner(gateway.TurnRunnerConfig{
		Router:       router,
		Sessions:     sessionStore,
		Models:       s.ChatModels,
		Personas:     s.Personas,
		Model:        newChatTurnModel(s.LLM, s.ToolReg, cfg.Chat.MaxSteps, toolLimits(cfg)),
		TaskLister:   s.ChatTaskLister,
		Tasks:        s.ChatTasks,
		TaskLogs:     s.ChatTaskLogs,
		TaskIdentity: s.DefaultChatIdentity,
		Bus:          s.Bus,
		BotUser:      cfg.BotUser,
		Channel:      channel,
		Operator:     cfg.Chat.Operator,
		Log:          s.Log,
	})
	if err := runner.Recover(ctx); err != nil && s.Log != nil {
		s.Log.Error("recover chat turns", "channel", channel, "err", err)
	}
	return runner
}

func sendChatTurn(ctx context.Context, llm *runtime.Runtime, chatModel string, options core.GenerateOptions, turn gateway.TurnStream) (string, error) {
	if turn == nil {
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
	text := drainChatStream(stream.FullStream, turn)
	if _, err := stream.FinishReason(); err != nil {
		return "", fmt.Errorf("llm chat stream: %w", err)
	}
	return text, nil
}

// drainChatStream consumes a model stream to close, reporting assistant text
// and each completed tool call to turn, and returns the assembled reply.
//
// FullStream is authoritative and its writes are synchronous, so it must be
// drained to close even when turn is nil  --  an unread stream stalls the
// generating goroutine.
//
// A tool is reported on its result, never on its call: the call part carries
// no outcome yet, so reporting both would show every tool twice, once with
// nothing to say.
func drainChatStream(parts <-chan core.StreamPart, turn gateway.TurnStream) string {
	type pendingToolCall struct {
		name       string
		parameters string
	}

	var sb strings.Builder
	pending := make(map[string]pendingToolCall)
	for part := range parts {
		switch part.Type {
		case core.StreamPartTextDelta:
			if part.TextDelta == "" {
				continue
			}
			sb.WriteString(part.TextDelta)
			if turn != nil {
				turn.Delta(part.TextDelta)
			}
		case core.StreamPartToolCall:
			if part.ToolCall == nil || part.ToolCall.ToolCallID == "" {
				continue
			}
			pending[part.ToolCall.ToolCallID] = pendingToolCall{
				name:       part.ToolCall.ToolName,
				parameters: gateway.SummarizeToolParameters(part.ToolCall.Input),
			}
		case core.StreamPartToolResult:
			if part.ToolResult == nil || turn == nil {
				continue
			}
			call := pending[part.ToolResult.ToolCallID]
			delete(pending, part.ToolResult.ToolCallID)
			name := part.ToolResult.ToolName
			if name == "" {
				name = call.name
			}
			turn.ToolCall(gateway.ToolCallEvent{
				ID:         part.ToolResult.ToolCallID,
				Name:       name,
				Parameters: call.parameters,
				Output:     part.ToolResult.Output,
				Err:        part.ToolResult.Error,
			})
			for _, ref := range multimodalMediaRefs(part.ToolResult.Output) {
				turn.Media(gateway.MediaEvent{
					ToolName:   name,
					Attachment: gateway.MediaAttachment{Type: ref.Type, URL: ref.URL},
				})
			}
		}
	}
	return sb.String()
}

// multimodalMediaRefs extracts the URLs a tool result carries when its
// output is a tools.MultimodalResult, so drainChatStream can report each as
// a gateway.MediaEvent alongside the ordinary ToolCall event.
//
// output is decoded with json.Unmarshal directly rather than checked
// against a schema first: most tool output is plain text or unrelated
// JSON, and unmarshalling into MultimodalResult simply leaves IsMultimodal
// false for anything that doesn't match, which is exactly "not multimodal"
// -- no separate detection step earns its keep here.
func multimodalMediaRefs(output string) []tools.MediaRef {
	if output == "" {
		return nil
	}
	var result tools.MultimodalResult
	if err := json.Unmarshal([]byte(output), &result); err != nil || !result.IsMultimodal {
		return nil
	}
	return result.URLs
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
