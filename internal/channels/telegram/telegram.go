// Package telegram implements gateway.Gateway using the Telegram Bot API
// via go-telegram/bot. Ported from GopherClaw's telegram.go and adapted
// for archie-core's gateway architecture.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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

	// Dangerous is the sandbox/process authority for /rollback and /stop.
	// When nil, those commands return "not configured". The composition
	// root supplies this; Telegram only renders approval UX.
	Dangerous DangerousCommandAuthority

	// showToolCalls controls whether new live replies render completed tool
	// calls. Each reply snapshots it so reloads affect only later turns.
	showToolCalls atomic.Bool

	// restartCh carries /restart requests from a bot handler to the
	// supervisor loop in Start. Buffered so a handler never blocks.
	restartCh         chan restartRequest
	pendingRestart    *restartRequest
	modelMu           sync.RWMutex
	modelCallbacks    map[string]string
	modelPages        map[string]modelPage
	providerMu        sync.RWMutex
	providerCallbacks map[string]string
	updateMu          sync.Mutex
	updateInProgress  bool
	updateActions     map[string]updateAction

	// dangerous command approval state
	dangerousMu        sync.Mutex
	dangerousActions   map[string]dangerousAction
	permanentApprovals []permanentApproval

	// tool approval state (gateway.ApprovalRequester). Kept separate from
	// the dangerous-command state above: tool approvals deliver the human's
	// decision on a channel to the blocked RequestApproval call rather than
	// executing through an onApprove callback.
	approvalMu       sync.Mutex
	pendingApprovals map[string]*pendingApproval

	// turns serialises chat turns per session off the update worker and
	// makes the running one cancellable by /stop. Rebuilt on every launch
	// so a restart abandons in-flight turns with the old bot instance.
	turns *gateway.Turns

	log           *slog.Logger
	bot           *bot.Bot
	webhookCancel context.CancelFunc
	running       bool

	// serverURL redirects the Bot API at a test server. Empty in production,
	// where the library's default endpoint is used. It exists so launch's
	// wiring can be tested without reaching api.telegram.org.
	serverURL string
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
		Token:              token,
		WebhookURL:         webhookURL,
		WebhookSecret:      webhookSecret,
		AllowedUserIDs:     allowedUserIDs,
		restartCh:          make(chan restartRequest, 1),
		modelCallbacks:     make(map[string]string),
		modelPages:         make(map[string]modelPage),
		providerCallbacks:  make(map[string]string),
		updateActions:      make(map[string]updateAction),
		dangerousActions:   make(map[string]dangerousAction),
		permanentApprovals: nil,
		pendingApprovals:   make(map[string]*pendingApproval),
		log:                log.With("component", "gateway-telegram"),
	}
}

func (g *Gateway) Name() string { return "telegram" }

// SetShowToolCalls controls tool visibility for replies created after the call.
func (g *Gateway) SetShowToolCalls(show bool) {
	g.showToolCalls.Store(show)
}

// ShowToolCalls reports the visibility setting that a new reply must snapshot.
func (g *Gateway) ShowToolCalls() bool {
	return g.showToolCalls.Load()
}

// RequestRestart asks the gateway supervisor to reload this adapter. It is
// safe for callers outside Telegram (such as the local Web UI) and never
// blocks when a restart is already queued.
func (g *Gateway) RequestRestart() error {
	select {
	case g.restartCh <- restartRequest{}:
		return nil
	default:
		return errors.New("telegram gateway restart already in progress")
	}
}

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
		if req := g.pendingRestart; req != nil && req.chatID != 0 {
			g.sendMessage(runCtx, b, req.chatID, req.threadID, "✅ Archie reloaded.")
		}
		if g.pendingRestart != nil {
			g.pendingRestart = nil
		}

		select {
		case <-ctx.Done():
			cancel()
			return g.Stop(context.WithoutCancel(ctx))
		case req := <-g.restartCh:
			g.log.Info("restarting telegram gateway", "requested_by", req.chatID)
			cancel()
			if err := g.Stop(context.WithoutCancel(ctx)); err != nil {
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
	// Turns are per-launch. Each queued turn carries the update handler's
	// context, which is this launch's, so a /restart cancels everything
	// still running under the outgoing bot instance.
	g.turns = gateway.NewTurns(g.log)

	opts := []bot.Option{
		bot.WithErrorsHandler(g.pipelineErrorHandler(ctx)),
		bot.WithDebugHandler(func(format string, args ...any) {
			g.log.Debug("telegram debug", "message", fmt.Sprintf(format, args...))
		}),
		bot.WithDefaultHandler(g.defaultHandler(router)),
		bot.WithMiddlewares(g.panicRecoveryMiddleware(), g.updateLoggingMiddleware()),
	}
	if g.WebhookSecret != "" {
		opts = append(opts, bot.WithWebhookSecretToken(g.WebhookSecret))
	}
	if g.serverURL != "" {
		opts = append(opts, bot.WithServerURL(g.serverURL), bot.WithSkipGetMe())
	}

	b, err := bot.New(g.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}
	g.bot = b

	// Gateway-local commands: handled directly, no LLM.
	b.RegisterHandler(bot.HandlerTypeMessageText, "/status", bot.MatchTypeExact, g.statusHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/whoami", bot.MatchTypeExact, g.whoamiHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/profile", bot.MatchTypeExact, g.profileHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/sessions", bot.MatchTypeExact, g.sessionsHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/resume", bot.MatchTypeExact, g.resumeHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/agents", bot.MatchTypeExact, g.agentsHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/version", bot.MatchTypeExact, g.versionHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/update", bot.MatchTypeExact, g.updateHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, g.startHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, g.helpHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/restart", bot.MatchTypeExact, g.restartHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/rollback", bot.MatchTypePrefix, g.rollbackHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/stop", bot.MatchTypePrefix, g.stopHandler(router))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/approve", bot.MatchTypeExact, g.approveHandler())
	b.RegisterHandler(bot.HandlerTypeMessageText, "/deny", bot.MatchTypeExact, g.denyHandler())

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
		g.dropPendingUpdates(ctx, b)
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
	return g.routeCmdHandler(router, "/status")
}

func (g *Gateway) whoamiHandler(router *gateway.Router) bot.HandlerFunc {
	return g.routeCmdHandler(router, "/whoami")
}

func (g *Gateway) profileHandler(router *gateway.Router) bot.HandlerFunc {
	return g.routeCmdHandler(router, "/profile")
}

func (g *Gateway) sessionsHandler(router *gateway.Router) bot.HandlerFunc {
	return g.routeCmdHandler(router, "/sessions")
}

func (g *Gateway) agentsHandler(router *gateway.Router) bot.HandlerFunc {
	return g.routeCmdHandler(router, "/agents")
}

func (g *Gateway) resumeHandler(router *gateway.Router) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		reply, err := router.Route(ctx, gateway.Message{
			ChannelID: fmt.Sprintf("%d", msg.Chat.ID),
			ThreadID:  threadIDString(msg.MessageThreadID),
			From:      msg.From.Username,
			Text:      msg.Text,
		})
		if err != nil {
			g.log.Error("resume handler failed", "error", err)
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, reply)
	}
}

// routeCmdHandler builds a handler that routes a fixed command through the
// gateway.Router and sends the reply.
func (g *Gateway) routeCmdHandler(router *gateway.Router, cmd string) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		reply, err := router.Route(ctx, gateway.Message{
			ChannelID: fmt.Sprintf("%d", msg.Chat.ID),
			ThreadID:  threadIDString(msg.MessageThreadID),
			From:      msg.From.Username,
			Text:      cmd,
		})
		if err != nil {
			g.log.Error("command handler failed", "command", cmd, "error", err)
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
			g.handleCallback(ctx, b, update, router)
			return
		}

		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok || msg.Text == "" {
			return
		}
		if isModelSelectorRequest(msg.Text) {
			g.sendProviderSelector(ctx, b, msg, router)
			return
		}
		if isModelCommand(msg.Text) {
			g.handleModelCommand(ctx, b, msg, router)
			return
		}
		if isPersonalityRequest(msg.Text) {
			g.handlePersonalityCommand(ctx, b, msg, router)
			return
		}
		g.submitTurn(ctx, b, msg, router)
	}
}

// handleCallback dispatches an inline-button callback query to the handler
// for its prefix. Callback prefixes are mutually exclusive; an unknown
// callback is ignored.
func (g *Gateway) handleCallback(ctx context.Context, b *bot.Bot, update *models.Update, router *gateway.Router) {
	switch {
	case strings.HasPrefix(update.CallbackQuery.Data, approvalCallbackPrefix):
		g.handleApprovalCallback(ctx, b, update)
	case strings.HasPrefix(update.CallbackQuery.Data, dangerousCmdPrefix):
		g.handleDangerousCallback(ctx, b, update)
	case strings.HasPrefix(update.CallbackQuery.Data, providerCallbackPrefix):
		g.handleProviderCallback(ctx, b, update, router)
	case strings.HasPrefix(update.CallbackQuery.Data, modelCallbackPrefix):
		g.handleModelCallback(ctx, b, update, router)
	case strings.HasPrefix(update.CallbackQuery.Data, modelPageCallbackPrefix):
		g.handleModelPageCallback(ctx, b, update, router)
	case update.CallbackQuery.Data == modelBackCallback:
		g.handleModelBackCallback(ctx, b, update, router)
	case update.CallbackQuery.Data == modelCancelCallback:
		g.handleModelCancelCallback(ctx, b, update)
	case update.CallbackQuery.Data == modelNoopCallback:
		g.handleModelNoopCallback(ctx, b, update)
	case strings.HasPrefix(update.CallbackQuery.Data, personalityCallbackPrefix):
		g.handlePersonalityCallback(ctx, b, update, router)
	case strings.HasPrefix(update.CallbackQuery.Data, updateCallbackPrefix):
		g.handleUpdateCallback(ctx, b, update)
	}
}

// submitTurn hands the message to its session's turn lane and returns at
// once, leaving the bot's single update worker free.
//
// The turn must not run on the caller's goroutine. go-telegram/bot serves
// updates from one worker that invokes handlers synchronously, so a turn
// running here would block delivery of every later update -- including the
// /stop meant to cancel it, which would sit unread until the turn it was
// aimed at had already finished.
func (g *Gateway) submitTurn(ctx context.Context, b *bot.Bot, msg *models.Message, router *gateway.Router) {
	gm := gateway.Message{
		// Telegram's message ID makes persistence idempotent: the store
		// derives a canonical ID from it, so a redelivered update is a
		// no-op rather than appending a duplicate or overwriting the
		// stored record, which must stay immutable. Date is the sender's
		// clock reading and is what history should be ordered by.
		SourceID:  fmt.Sprintf("%d", msg.ID),
		ChannelID: fmt.Sprintf("%d", msg.Chat.ID),
		ThreadID:  threadIDString(msg.MessageThreadID),
		From:      msg.From.Username,
		Text:      msg.Text,
		At:        time.Unix(int64(msg.Date), 0).UTC(),
	}

	// The lane key must be the session, so that /stop -- which resolves
	// the same key -- reaches the turn the sender is actually watching.
	session, err := router.ResolveSessionKey(ctx, gm)
	if err != nil {
		g.log.Error("resolve session for turn", "error", err)
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "❌ Could not resolve this conversation's session.")
		return
	}

	chatID, threadID := msg.Chat.ID, msg.MessageThreadID
	g.turns.Submit(ctx, session, func(turnCtx context.Context) {
		// If the turn invokes a tool that requires human approval,
		// the dispatch layer blocks on this approver. Nil is fine
		// — most turns need no gating.
		approver := g.NewApprover(b, chatID, threadID, msg.From.ID)
		turnCtx = gateway.WithApprovalRequester(turnCtx, approver)

		// reply appears as it is written; the typing indicator covers the
		// gap before the first token and any non-streaming path.
		stopTyping := g.startTyping(turnCtx, b, chatID, threadID)
		live := g.newLiveReply(turnCtx, b, chatID, threadID, g.ShowToolCalls())

		// If it starts with / but wasn't matched by a registered
		// handler, it's unknown  --  let the router handle it (which
		// will say "unrecognized").
		reply, err := router.RouteStream(turnCtx, gm, live)
		stopTyping()

		// A cancelled turn is a /stop, not a fault. The stop handler has
		// already acknowledged it, and turnCtx is dead, so there is
		// nothing useful left to send from here  --  but whatever was
		// already streamed stays, minus the cursor that would otherwise
		// claim the answer is still being written. abandon strips turnCtx's
		// own cancellation before it edits, so the cursor-drop is not itself
		// aborted by the /stop that triggered it.
		//
		// errors.Is(err, context.Canceled) reliably separates a /stop from a
		// fault; either way turnCtx is dead by the time we get here. A
		// genuine fault is marked as failed rather than left as a clean
		// partial: /stop is an acknowledged interruption, a provider error
		// is not, and an unmarked partial reads as a finished answer either
		// way.
		if err != nil {
			if errors.Is(err, context.Canceled) {
				g.log.Info("chat turn stopped", "session", session)
				live.abandon(turnCtx)
			} else {
				g.log.Error("route failed", "error", err)
				live.abandonFailed(turnCtx)
			}
			return
		}
		live.finalize(turnCtx, reply)
	})
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

// messageMaxLen keeps a single message under Telegram's 4096 character
// limit, with room for the continuation marker split adds.
const messageMaxLen = 4000

func (g *Gateway) sendMessage(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int, text string) {
	if len(text) <= messageMaxLen {
		g.send(ctx, b, chatID, messageThreadID, text, "send message failed")
		return
	}
	parts := splitLongMessage(text, messageMaxLen)
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

// splitLongMessage divides text into parts of at most maxLen runes each,
// preferring to cut on newlines. Bounds are counted in runes, not bytes,
// and an oversized line is itself cut on rune boundaries  --  slicing by byte
// offset instead would, for any line containing multi-byte UTF-8 (CJK,
// emoji, ...), land a cut inside a rune's continuation bytes and produce a
// part that is not valid UTF-8 on either side of the cut.
func splitLongMessage(text string, maxLen int) []string {
	if utf8.RuneCountInString(text) <= maxLen {
		return []string{text}
	}
	var parts []string
	lines := strings.Split(text, "\n")
	var cur strings.Builder
	curRunes := 0
	for _, line := range lines {
		lineRunes := utf8.RuneCountInString(line)
		if curRunes+lineRunes+1 > maxLen {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
				curRunes = 0
			}
			if lineRunes > maxLen {
				runes := []rune(line)
				for i := 0; i < len(runes); i += maxLen {
					end := min(i+maxLen, len(runes))
					parts = append(parts, string(runes[i:end]))
				}
				continue
			}
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
			curRunes++
		}
		cur.WriteString(line)
		curRunes += lineRunes
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

// dropPendingUpdates clears Telegram's queue of undelivered updates before
// long polling starts.
//
// Telegram holds updates for 24h and replays them on the first getUpdates,
// so without this a daemon that was down overnight boots and answers every
// message sent while it was gone -- one LLM turn each, in a burst. The
// webhook path already passes DropPendingUpdates when it registers; polling
// has no equivalent, so it has to be asked for explicitly. deleteWebhook is
// the call that carries the flag, and it doubles as a guard against a stale
// webhook registration, which would make getUpdates fail with 409.
//
// This also runs on /restart, so a message sent during the restart window is
// discarded rather than answered late. That matches the webhook branch, which
// passes the same flag on every launch.
//
// Best-effort: a failure here is logged, not fatal. Chat availability matters
// more than a clean queue, and the deadline keeps an unreachable Telegram
// from holding up gateway startup.
func (g *Gateway) dropPendingUpdates(ctx context.Context, b *bot.Bot) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := b.DeleteWebhook(ctx, &bot.DeleteWebhookParams{DropPendingUpdates: true}); err != nil {
		g.log.Warn("could not drop pending telegram updates, backlog may be replayed", "error", err)
	}
}
