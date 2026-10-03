package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// interactiveReplyTimeout bounds how long one clarify or picker request waits
// for the human's reply. It matches the approval window so the two blocking
// interactions expire together and neither can wedge a turn indefinitely.
const interactiveReplyTimeout = messaging.ToolApprovalTimeout

// pendingReplyKey identifies the chat a blocked clarify/picker prompt belongs
// to, so only the addressed human's next text is consumed as the answer.
type pendingReplyKey struct {
	chatID    int64
	threadID  int
	recipient int64
}

// pendingReply is one clarify/picker prompt waiting on the human's next text.
// It is the text-reply analogue of pendingApproval, kept separate so the
// button (callback) and message (text) models cannot entangle.
type pendingReply struct {
	resultCh  chan replyResult
	expiresAt time.Time
}

// replyResult carries the human's typed answer (or the wait's error) back to
// the blocked interaction.
type replyResult struct {
	text string
	err  error
}

// telegramInteractor carries clarify and picker questions as plain text the
// human replies to.
type telegramInteractor struct {
	gw        *Gateway
	bot       *bot.Bot
	chatID    int64
	threadID  int
	recipient int64
}

// Compile-time guards: the adapter carries both interactions and reports them
// through the capability contract, so ClarifierOf/PickerOf resolve it.
var (
	_ messaging.ClarifyRequester   = (*telegramInteractor)(nil)
	_ messaging.PickerRequester    = (*telegramInteractor)(nil)
	_ messaging.CapabilityReporter = (*telegramInteractor)(nil)
)

// Capabilities reports clarify and picker support.
func (i *telegramInteractor) Capabilities() messaging.AdapterCapabilities {
	return messaging.AdapterCapabilities{Clarify: true, Picker: true}
}

// NewInteractor returns the channel's clarify/picker adapter for one chat,
// already resolved to the requesters its capability report advertises. The
// gateway turn stores it with WithInteractive so its question tool can reach
// the human.
func (g *Gateway) NewInteractor(b *bot.Bot, chatID int64, threadID int, recipient int64) messaging.Interactive {
	i := &telegramInteractor{
		gw:        g,
		bot:       b,
		chatID:    chatID,
		threadID:  threadID,
		recipient: recipient,
	}
	return messaging.InteractiveOf(i)
}

// RequestClarification poses the question as text and blocks for the reply.
func (i *telegramInteractor) RequestClarification(ctx context.Context, req messaging.ClarifyRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, interactiveReplyTimeout)
	defer cancel()
	fallback := i.fallback()
	return fallback.RequestClarification(ctx, req)
}

// RequestChoice poses the options as numbered text and matches the typed
// reply. The domain's text fallback owns the rendering and matching, so the
// degrade is one implementation rather than a second parser here.
func (i *telegramInteractor) RequestChoice(ctx context.Context, req messaging.PickerRequest) (messaging.InteractiveChoice, error) {
	ctx, cancel := context.WithTimeout(ctx, interactiveReplyTimeout)
	defer cancel()
	fallback := i.fallback()
	return fallback.RequestChoice(ctx, req)
}

// fallback builds a fresh text fallback for one interaction. Registering the
// pending reply inside Send, before the prompt is delivered, closes the race
// where a fast reply arrives before the blocked call is listening.
func (i *telegramInteractor) fallback() messaging.TextFallback {
	key := pendingReplyKey{chatID: i.chatID, threadID: i.threadID, recipient: i.recipient}
	ch := make(chan replyResult, 1)
	return messaging.TextFallback{
		Send: func(ctx context.Context, text string) error {
			i.gw.registerPendingReply(key, pendingReply{
				resultCh:  ch,
				expiresAt: time.Now().Add(interactiveReplyTimeout),
			})
			if err := i.send(ctx, text); err != nil {
				i.gw.removePendingReply(key)
				return err
			}
			return nil
		},
		Reply: func(ctx context.Context) (string, error) {
			select {
			case res := <-ch:
				return res.text, res.err
			case <-ctx.Done():
				i.gw.removePendingReply(key)
				return "", ctx.Err()
			}
		},
	}
}

// send delivers one prompt. ForceReply makes the reply affordance explicit in
// a chat with other traffic, so the human answers the question rather than
// sending a message the turn cannot tell apart from a new request.
func (i *telegramInteractor) send(ctx context.Context, text string) error {
	params := &bot.SendMessageParams{
		ChatID:      i.chatID,
		Text:        text,
		ReplyMarkup: &models.ForceReply{ForceReply: true, Selective: true},
	}
	if i.threadID != 0 {
		params.MessageThreadID = i.threadID
	}
	if _, err := i.bot.SendMessage(ctx, params); err != nil {
		return fmt.Errorf("send interactive prompt: %w", err)
	}
	return nil
}

// registerPendingReply stores a pending interaction, pruning any that have
// already expired.
func (g *Gateway) registerPendingReply(key pendingReplyKey, pr pendingReply) {
	g.interactiveMu.Lock()
	defer g.interactiveMu.Unlock()
	now := time.Now()
	for existing, pending := range g.pendingReplies {
		if !pending.expiresAt.After(now) {
			delete(g.pendingReplies, existing)
		}
	}
	g.pendingReplies[key] = &pr
}

// removePendingReply drops a pending interaction, if present.
func (g *Gateway) removePendingReply(key pendingReplyKey) {
	g.interactiveMu.Lock()
	defer g.interactiveMu.Unlock()
	delete(g.pendingReplies, key)
}

// consumePendingReply atomically claims the pending interaction for the chat
// and delivers the typed answer. It rejects expired entries and reports
// whether the text was consumed as an answer.
func (g *Gateway) consumePendingReply(key pendingReplyKey, text string) bool {
	g.interactiveMu.Lock()
	pr, found := g.pendingReplies[key]
	expired := !found || !pr.expiresAt.After(time.Now())
	if !expired {
		delete(g.pendingReplies, key)
	}
	g.interactiveMu.Unlock()
	if expired {
		return false
	}
	select {
	case pr.resultCh <- replyResult{text: text}:
		return true
	default:
		// The channel already has a result (the wait timed out between the
		// expiry check and now); do not report a reply that was not taken.
		return false
	}
}

// deliverInteractiveReply routes a text reply to a blocked clarify/picker
// request when one is pending for this chat and sender, and reports whether
// the message was consumed. A consumed reply is not dispatched as a new turn;
// a command is never consumed, so /stop still cancels the blocked turn.
func (g *Gateway) deliverInteractiveReply(msg *models.Message) bool {
	if msg == nil || msg.From == nil {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	return g.consumePendingReply(pendingReplyKey{
		chatID:    msg.Chat.ID,
		threadID:  msg.MessageThreadID,
		recipient: msg.From.ID,
	}, text)
}
