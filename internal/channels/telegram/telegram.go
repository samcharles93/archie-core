// Package telegram implements gateway.Gateway using the Telegram Bot API
// via go-telegram/bot. Ported from GopherClaw's telegram.go and adapted
// for archie-core's gateway architecture.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// Gateway is a Telegram gateway.Gateway backed by go-telegram/bot.
type Gateway struct {
	Token         string
	WebhookURL    string
	WebhookSecret string
	// AllowedUserIDs lists the Telegram user IDs allowed to use the bot,
	// matched against the sender rather than the chat so the bot cannot be
	// reached by adding it to a group. Empty denies everyone: a bot handle
	// is public, and chat tools run with the daemon's authority, so failing
	// open would expose those tools to any stranger who finds the bot.
	AllowedUserIDs []int64
	// Reload re-reads configuration during /restart and applies it to g.
	// Supplied by the composition root, which owns config paths. Nil
	// restarts with the settings already in memory.
	Reload func(*Gateway) error
	// ReleaseAnnouncements sends one-time upgrade notes to authorized users.
	// It is supplied by the composition root because version, changelog, and
	// persistence paths are deployment concerns.
	ReleaseAnnouncements ReleaseAnnouncer
	// Version reports installed component versions. The composition root owns
	// build metadata; Telegram only renders it for authorized users.
	Version func() string
	// Updates is injected by the composition root. It owns release discovery,
	// deferral persistence, and installation; Telegram only presents it.
	Updates UpdateService

	// restartCh carries /restart requests from a bot handler to the
	// supervisor loop in Start. Buffered so a handler never blocks.
	restartCh         chan restartRequest
	pendingRestart    *restartRequest
	modelMu           sync.RWMutex
	modelCallbacks    map[string]string
	providerMu        sync.RWMutex
	providerCallbacks map[string]string
	updateMu          sync.Mutex
	updateInProgress  bool
	updateActions     map[string]updateAction

	log           *slog.Logger
	bot           *bot.Bot
	webhookCancel context.CancelFunc
	running       bool
}

type UpdateService interface {
	Check(context.Context, int64) (releaseupdate.Snapshot, error)
	Defer(context.Context, int64, releaseupdate.Snapshot) error
	Install(context.Context, func(string)) error
	CanInstall() bool
}

type ReleaseAnnouncer interface {
	Announce(
		ctx context.Context,
		recipients []int64,
		send func(context.Context, int64, string) error,
	) error
}

// New returns an unstarted Gateway. Call Start to begin webhook
// processing.
func New(token, webhookURL, webhookSecret string, allowedUserIDs []int64, log *slog.Logger) *Gateway {
	return &Gateway{
		Token:             token,
		WebhookURL:        webhookURL,
		WebhookSecret:     webhookSecret,
		AllowedUserIDs:    allowedUserIDs,
		restartCh:         make(chan restartRequest, 1),
		modelCallbacks:    make(map[string]string),
		providerCallbacks: make(map[string]string),
		updateActions:     make(map[string]updateAction),
		log:               log.With("component", "gateway-telegram"),
	}
}

func (g *Gateway) Name() string { return "telegram" }

// Start supervises the bot: it launches an instance and relaunches it
// whenever a restart is requested, blocking until ctx is cancelled.
//
// Restarts are scoped to this gateway. The daemon keeps running, so
// in-flight agent tasks are untouched  --  the whole point of the escape
// hatch is to recover chat without disturbing work in progress.
func (g *Gateway) Start(ctx context.Context, router *gateway.Router) error {
	if g.Token == "" {
		return fmt.Errorf("telegram bot token is required")
	}
	g.log.Info("starting telegram gateway")

	for {
		runCtx, cancel := context.WithCancel(ctx)
		b, err := g.launch(runCtx, router)
		if err != nil {
			cancel()
			return err
		}
		// Confirm to whoever asked, now that the new instance can send.
		if req := g.pendingRestart; req != nil {
			g.sendMessage(runCtx, b, req.chatID, req.threadID, "✅ Archie reloaded.")
			g.pendingRestart = nil
		}

		select {
		case <-ctx.Done():
			cancel()
			return g.Stop(context.Background())
		case req := <-g.restartCh:
			g.log.Info("restarting telegram gateway", "requested_by", req.chatID)
			cancel()
			if err := g.Stop(context.Background()); err != nil {
				g.log.Warn("stop during restart failed", "error", err)
			}
			if g.Reload != nil {
				if err := g.Reload(g); err != nil {
					// Keep the previous settings rather than dying: a bad
					// config edit must not take the gateway down for good.
					g.log.Error("config reload failed, restarting with previous settings", "error", err)
				}
			}
			g.pendingRestart = &req
		}
	}
}

// launch builds one bot instance, registers handlers and begins receiving.
// It returns as soon as delivery is running; the caller owns ctx.
func (g *Gateway) launch(ctx context.Context, router *gateway.Router) (*bot.Bot, error) {
	opts := []bot.Option{
		bot.WithErrorsHandler(func(err error) {
			g.log.Error("telegram pipeline error", "error", err)
		}),
		bot.WithDebugHandler(func(format string, args ...any) {
			g.log.Debug("telegram debug", "message", fmt.Sprintf(format, args...))
		}),
		bot.WithDefaultHandler(g.defaultHandler(router)),
		bot.WithMiddlewares(g.panicRecoveryMiddleware(), g.updateLoggingMiddleware()),
	}
	if g.WebhookSecret != "" {
		opts = append(opts, bot.WithWebhookSecretToken(g.WebhookSecret))
	}

	b, err := bot.New(g.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}
	g.bot = b

	// Gateway-local commands: handled directly, no LLM.
	b.RegisterHandler(bot.HandlerTypeMessageText, "/status", bot.MatchTypeExact, g.statusHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/version", bot.MatchTypeExact, g.versionHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/update", bot.MatchTypeExact, g.updateHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, g.startHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, g.helpHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/restart", bot.MatchTypeExact, g.restartHandler())

	// Publish the command list so Telegram renders a menu. Without this
	// the commands are undiscoverable and the LLM, having no idea they
	// exist, tells users there are none.
	g.registerCommands(ctx, b)
	g.announceRelease(ctx, b)

	// Set up webhook or fall back to long polling.
	if g.WebhookURL != "" {
		params := &bot.SetWebhookParams{
			URL:                g.WebhookURL,
			SecretToken:        g.WebhookSecret,
			MaxConnections:     40,
			AllowedUpdates:     []string{"message", "callback_query"},
			DropPendingUpdates: true,
		}
		if _, err := b.SetWebhook(ctx, params); err != nil {
			return nil, fmt.Errorf("set webhook: %w", err)
		}
		g.log.Info("webhook set", "url", g.WebhookURL)

		info, err := b.GetWebhookInfo(ctx)
		if err != nil {
			g.log.Warn("failed to get webhook info", "error", err)
		} else {
			g.log.Info("webhook info",
				"url", info.URL,
				"pending_update_count", info.PendingUpdateCount,
				"last_error_message", info.LastErrorMessage,
				"max_connections", info.MaxConnections,
			)
		}

		webhookCtx, cancel := context.WithCancel(ctx)
		g.webhookCancel = cancel
		g.running = true
		go b.StartWebhook(webhookCtx)
	} else {
		g.running = true
		go b.Start(ctx)
	}

	return b, nil
}

func (g *Gateway) announceRelease(ctx context.Context, b *bot.Bot) {
	if g.ReleaseAnnouncements == nil {
		return
	}
	err := g.ReleaseAnnouncements.Announce(
		ctx,
		slices.Clone(g.AllowedUserIDs),
		func(ctx context.Context, recipient int64, message string) error {
			_, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: recipient,
				Text:   message,
			})
			return err
		},
	)
	if err != nil {
		// Notifications are ancillary to chat availability. Keep the gateway
		// running and let the announcer retry unrecorded recipients next time.
		g.log.Warn("release announcement failed", "error", err)
	}
}

// Stop gracefully shuts down the bot and deletes the webhook.
func (g *Gateway) Stop(ctx context.Context) error {
	if !g.running {
		return nil
	}
	g.log.Info("stopping telegram gateway")
	g.running = false

	if g.webhookCancel != nil {
		g.webhookCancel()
		g.webhookCancel = nil
	}
	if g.WebhookURL != "" && g.bot != nil {
		if _, err := g.bot.DeleteWebhook(ctx, &bot.DeleteWebhookParams{DropPendingUpdates: true}); err != nil {
			g.log.Warn("error deleting webhook", "error", err)
		}
	}
	return nil
}

// WebhookHandler returns the HTTP handler for Telegram webhooks.
func (g *Gateway) WebhookHandler() http.Handler {
	if g.bot == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bot not initialized", http.StatusServiceUnavailable)
		})
	}
	return g.bot.WebhookHandler()
}

// ── command handlers (gateway-local  --  no LLM) ────────────────

func (g *Gateway) statusHandler(router *gateway.Router) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		reply, err := router.Route(ctx, gateway.Message{
			ChannelID: fmt.Sprintf("%d", msg.Chat.ID),
			ThreadID:  threadIDString(msg.MessageThreadID),
			From:      msg.From.Username,
			Text:      "/status",
		})
		if err != nil {
			g.log.Error("status handler failed", "error", err)
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, reply)
	}
}

func (g *Gateway) versionHandler() bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		if g.Version == nil {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Version information is not configured.")
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, g.Version())
	}
}

func (g *Gateway) helpHandler() bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, gatewayHelpText())
	}
}

func (g *Gateway) startHandler() bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
			"Archie is running. Send a message to chat, or type /help for available commands.")
	}
}

// ── default handler (non-command text → router) ─────────────

func (g *Gateway) defaultHandler(router *gateway.Router) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.CallbackQuery != nil {
			switch {
			case strings.HasPrefix(update.CallbackQuery.Data, providerCallbackPrefix):
				g.handleProviderCallback(ctx, b, update, router)
			case strings.HasPrefix(update.CallbackQuery.Data, modelCallbackPrefix):
				g.handleModelCallback(ctx, b, update, router)
			case strings.HasPrefix(update.CallbackQuery.Data, updateCallbackPrefix):
				g.handleUpdateCallback(ctx, b, update)
			}
			return
		}

		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok || msg.Text == "" {
			return
		}
		if isModelSelectorRequest(msg.Text) {
			g.sendModelSelector(ctx, b, msg, router)
			return
		}
		if isProviderSelectorRequest(msg.Text) {
			g.sendProviderSelector(ctx, b, msg, router)
			return
		}

		// An LLM turn takes many seconds. Stream it into a draft so the
		// reply appears as it is written; the typing indicator covers the
		// gap before the first token and any non-streaming path.
		stopTyping := g.startTyping(ctx, b, msg.Chat.ID, msg.MessageThreadID)
		draft := g.newDraft(b, msg.Chat.ID, msg.MessageThreadID)

		// If it starts with / but wasn't matched by a registered
		// handler, it's unknown  --  let the router handle it (which
		// will say "unrecognized").
		reply, err := router.RouteStream(ctx, gateway.Message{
			ChannelID: fmt.Sprintf("%d", msg.Chat.ID),
			ThreadID:  threadIDString(msg.MessageThreadID),
			From:      msg.From.Username,
			Text:      msg.Text,
		}, draft.onDelta)
		stopTyping()
		if err != nil {
			g.log.Error("route failed", "error", err)
			return
		}
		if reply != "" {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, reply)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────

// threadIDString converts a Telegram message_thread_id to a string suitable
// for gateway.Message.ThreadID and SessionSource.ThreadID. A zero value
// (no topic thread, or the General topic) maps to empty string so that
// flat-chat routing continues to work without changes.
func threadIDString(id int) string {
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("%d", id)
}

func (g *Gateway) authorizedMessage(ctx context.Context, b *bot.Bot, update *models.Update) (*models.Message, bool) {
	if update.Message == nil || update.Message.From == nil {
		return nil, false
	}
	if g.isSenderAllowed(update.Message.From.ID) {
		return update.Message, true
	}
	g.log.Warn("message from unauthorized sender",
		"user_id", update.Message.From.ID,
		"username", update.Message.From.Username,
		"chat_id", update.Message.Chat.ID,
	)
	// Reply so an authorised user who is simply missing from the list can
	// tell the bot is alive and misconfigured, rather than dead.
	g.sendMessage(ctx, b, update.Message.Chat.ID, update.Message.MessageThreadID,
		"⛔ You are not authorised to use this bot.")
	return nil, false
}

// typingRefresh is how often the typing action is re-sent. Telegram clears
// the indicator after ~5s, so it must be refreshed to stay visible for the
// length of an LLM turn.
const typingRefresh = 4 * time.Second

// startTyping shows the "typing…" indicator and keeps it alive until the
// returned stop function is called. Errors are logged at debug level only:
// a missing typing indicator must never block or fail the actual reply.
func (g *Gateway) startTyping(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int) (stop func()) {
	send := func() {
		params := &bot.SendChatActionParams{
			ChatID: chatID,
			Action: models.ChatActionTyping,
		}
		if messageThreadID != 0 {
			params.MessageThreadID = messageThreadID
		}
		if _, err := b.SendChatAction(ctx, params); err != nil {
			g.log.Debug("send chat action failed", "error", err)
		}
	}

	typingCtx, cancel := context.WithCancel(ctx)
	send()
	go func() {
		ticker := time.NewTicker(typingRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return cancel
}

// isSenderAllowed reports whether a message's sender may use the bot.
// An empty allowlist denies everyone (see Gateway.AllowedUserIDs).
func (g *Gateway) isSenderAllowed(userID int64) bool {
	return slices.Contains(g.AllowedUserIDs, userID)
}

func (g *Gateway) sendMessage(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int, text string) {
	// Split long messages to stay under Telegram's 4096 character limit.
	const maxLen = 4000
	if len(text) <= maxLen {
		g.send(ctx, b, chatID, messageThreadID, text, "send message failed")
		return
	}
	parts := splitLongMessage(text, maxLen)
	for i, part := range parts {
		partText := part
		if i < len(parts)-1 {
			partText += "\n\n_(continued...)_"
		}
		g.send(ctx, b, chatID, messageThreadID, partText, "send message part failed")
	}
}

// send delivers one message as a rich message, letting Telegram render the
// Markdown an LLM already emits.
//
// sendRichMessage takes Markdown natively and supports constructs the
// legacy parse modes cannot express at all  --  notably tables, which LLM
// replies use freely. That removes the need to translate Markdown into the
// narrow HTML subset the older API accepted.
//
// Rich messages are a recent Bot API addition, so a rejection here is
// treated as "unsupported" rather than fatal: fall back to a plain send so
// the user still receives the reply, unformatted, instead of silence.
func (g *Gateway) send(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int, text, errMsg string) {
	rich := &bot.SendRichMessageParams{
		ChatID:      chatID,
		RichMessage: models.InputRichMessage{Markdown: text},
	}
	if messageThreadID != 0 {
		rich.MessageThreadID = messageThreadID
	}
	if _, err := b.SendRichMessage(ctx, rich); err == nil {
		return
	} else {
		g.log.Warn("rich send failed, retrying unformatted", "error", err)
	}

	plain := &bot.SendMessageParams{ChatID: chatID, Text: text}
	if messageThreadID != 0 {
		plain.MessageThreadID = messageThreadID
	}
	if _, err := b.SendMessage(ctx, plain); err != nil {
		g.log.Error(errMsg, "error", err)
	}
}

func splitLongMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var parts []string
	lines := strings.Split(text, "\n")
	var cur strings.Builder
	for _, line := range lines {
		if cur.Len()+len(line)+1 > maxLen {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
			if len(line) > maxLen {
				for i := 0; i < len(line); i += maxLen {
					end := min(i+maxLen, len(line))
					parts = append(parts, line[i:end])
				}
				continue
			}
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// ── middlewares ──────────────────────────────────────────────

func (g *Gateway) panicRecoveryMiddleware() bot.Middleware {
	return func(next bot.HandlerFunc) bot.HandlerFunc {
		return func(ctx context.Context, b *bot.Bot, update *models.Update) {
			defer func() {
				if r := recover(); r != nil {
					kind, chatID, hasChat := updateMetadata(update)
					attrs := []any{
						"panic", r,
						"update_kind", kind,
						"update_id", update.ID,
						"stack", string(debug.Stack()),
					}
					if hasChat {
						attrs = append(attrs, "chat_id", chatID)
					}
					g.log.Error("panic in telegram middleware", attrs...)
				}
			}()
			next(ctx, b, update)
		}
	}
}

func (g *Gateway) updateLoggingMiddleware() bot.Middleware {
	return func(next bot.HandlerFunc) bot.HandlerFunc {
		return func(ctx context.Context, b *bot.Bot, update *models.Update) {
			kind, chatID, hasChat := updateMetadata(update)
			attrs := []any{"update_kind", kind, "update_id", update.ID}
			if hasChat {
				attrs = append(attrs, "chat_id", chatID)
			}
			if threadID := updateThreadID(update); threadID != 0 {
				attrs = append(attrs, "thread_id", threadID)
			}
			g.log.Debug("telegram update", attrs...)
			next(ctx, b, update)
		}
	}
}

func updateMetadata(update *models.Update) (kind string, chatID int64, hasChat bool) {
	switch {
	case update == nil:
		return "unknown", 0, false
	case update.Message != nil:
		return "message", update.Message.Chat.ID, true
	case update.CallbackQuery != nil:
		if update.CallbackQuery.Message.Message != nil {
			return "callback_query", update.CallbackQuery.Message.Message.Chat.ID, true
		}
		return "callback_query", 0, false
	default:
		return "other", 0, false
	}
}

// updateThreadID extracts the message_thread_id from an update for logging
// and session routing. Returns 0 for flat chats / non-message updates.
func updateThreadID(update *models.Update) int {
	if update == nil || update.Message == nil {
		return 0
	}
	return update.Message.MessageThreadID
}

// ConfigSchema returns the JSON Schema for the Telegram channel config.
func (g *Gateway) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "required": ["token_env"],
  "properties": {
    "token_env": {
      "type": "string",
      "description": "Environment variable holding the Telegram bot token from @BotFather"
    },
    "webhook_url": {
      "type": "string",
      "description": "Public HTTPS URL for Telegram webhook delivery"
    },
    "webhook_secret": {
      "type": "string",
      "description": "Secret token for webhook validation"
    },
    "allowed_chat_ids": {
      "type": "array",
      "items": { "type": "integer" },
      "description": "Restrict bot access to specific chat IDs"
    }
  }
}`)
}

// ValidateConfig checks the Telegram channel configuration.
func (g *Gateway) ValidateConfig(cfg map[string]any) error {
	if cfg == nil {
		return fmt.Errorf("telegram config is required")
	}
	tokenEnv, _ := cfg["token_env"].(string)
	if tokenEnv == "" {
		return fmt.Errorf("telegram.token_env is required")
	}
	return nil
}

// Compile-time guard.
var _ channels.Channel = (*Gateway)(nil)
