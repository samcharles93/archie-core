package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// approvalCallbackPrefix separates tool-approval callbacks from other
// callback types handled in defaultHandler. It is deliberately distinct
// from every other callback prefix.
const approvalCallbackPrefix = "approval:"

// telegramApprover implements gateway.ApprovalRequester with an inline-button
// prompt, blocking until the human decides, ctx ends or the window elapses.
type telegramApprover struct {
	gw        *Gateway
	bot       *bot.Bot
	chatID    int64
	threadID  int
	recipient int64
}

// pendingApproval records a tool-approval request waiting on a human
// decision. It is kept separate so the
// tool-approval model (result channels) cannot entangle.
type pendingApproval struct {
	token       string
	action      string
	description string
	recipient   int64
	expiresAt   time.Time
	resultCh    chan approvalResult
}

// approvalResult carries the human's decision back to the blocked
// RequestApproval call.
type approvalResult struct {
	decision messaging.ApprovalDecision
	err      error
}

// Compile-time guard.
var (
	_ messaging.ApprovalRequester  = (*telegramApprover)(nil)
	_ messaging.CapabilityReporter = (*telegramApprover)(nil)
)

// Capabilities reports native approval only.
func (a *telegramApprover) Capabilities() messaging.AdapterCapabilities {
	return messaging.AdapterCapabilities{Approval: true}
}

// NewApprover returns an ApprovalRequester that renders prompts in the
// given chat. recipient is the Telegram user who may approve; in a private
// DM this is the same as chatID, in a group it is the sender of the turn
// that triggered the gated tool call.
func (g *Gateway) NewApprover(b *bot.Bot, chatID int64, threadID int, recipient int64) messaging.ApprovalRequester {
	return &telegramApprover{
		gw:        g,
		bot:       b,
		chatID:    chatID,
		threadID:  threadID,
		recipient: recipient,
	}
}

// RequestApproval asks the recipient to approve action. A current permanent
// approval returns ApprovalPermanentlyApproved without prompting.
func (a *telegramApprover) RequestApproval(ctx context.Context, action, description string) (messaging.ApprovalDecision, error) {
	// Compose the lookup key so a permanent approval is scoped to
	// the specific resource: "Approve Permanently" on "delete session
	// abc123" only permanently approves that one session, not every
	// session_delete forever.
	permKey := action + "\x00" + description
	if a.gw.hasPermanentApprovalExact(a.recipient, permKey) {
		return messaging.ApprovalPermanentlyApproved, nil
	}

	token := makeCallbackToken()
	resultCh := make(chan approvalResult, 1)
	a.gw.registerPendingApproval(pendingApproval{
		token:       token,
		action:      action,
		description: description,
		recipient:   a.recipient,
		expiresAt:   time.Now().Add(messaging.ToolApprovalTimeout),
		resultCh:    resultCh,
	})

	params := &bot.SendMessageParams{
		ChatID:      a.chatID,
		Text:        approvalPromptText(action, description),
		ReplyMarkup: a.gw.approvalKeyboard(token),
	}
	if a.threadID != 0 {
		params.MessageThreadID = a.threadID
	}

	approvalCtx, cancel := context.WithTimeout(ctx, messaging.ToolApprovalTimeout)
	defer cancel()

	if _, err := a.bot.SendMessage(approvalCtx, params); err != nil {
		a.gw.removePendingApproval(token)
		if approvalCtx.Err() != nil {
			return messaging.ApprovalDenied, approvalCtx.Err()
		}
		a.gw.log.Error("send approval prompt failed", "error", err)
		return messaging.ApprovalDenied, fmt.Errorf("send approval prompt: %w", err)
	}

	select {
	case result := <-resultCh:
		return a.applyApprovalDecision(action, description, result)
	case <-approvalCtx.Done():
		// A decision may have raced the timeout: drain it rather than
		// discarding a valid approval.
		select {
		case result := <-resultCh:
			return a.applyApprovalDecision(action, description, result)
		default:
			a.gw.removePendingApproval(token)
			return messaging.ApprovalDenied, approvalCtx.Err()
		}
	}
}

// applyApprovalDecision maps a human decision to the gateway contract,
// recording a permanent approval when the human asked for one.
func (a *telegramApprover) applyApprovalDecision(action, description string, result approvalResult) (messaging.ApprovalDecision, error) {
	switch result.decision {
	case messaging.ApprovalApproved:
		return messaging.ApprovalApproved, nil
	case messaging.ApprovalPermanentlyApproved:
		// Scoped: permanently approving "delete session abc123" only
		// silences the prompt for that one resource, not every call to
		// the same tool.
		a.gw.recordPermanentApproval(a.recipient, action+"\x00"+description)
		return messaging.ApprovalPermanentlyApproved, nil
	case messaging.ApprovalDenied:
		if result.err != nil {
			return messaging.ApprovalDenied, result.err
		}
		return messaging.ApprovalDenied, messaging.ErrApprovalDenied
	default:
		return messaging.ApprovalDenied, fmt.Errorf("unexpected approval decision %v", result.decision)
	}
}

// approvalKeyboard builds the three-button inline keyboard for a tool
// approval: "Approve this time", "Approve Permanently", "Deny". It mirrors
// the approval: callback prefix.
func (g *Gateway) approvalKeyboard(token string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text:         "Approve this time",
					CallbackData: approvalCallbackPrefix + "approve:" + token,
				},
			},
			{
				{
					Text:         "Approve Permanently",
					CallbackData: approvalCallbackPrefix + "permanent:" + token,
				},
			},
			{
				{
					Text:         "Deny",
					CallbackData: approvalCallbackPrefix + "deny:" + token,
				},
			},
		},
	}
}

// registerPendingApproval stores a pending tool approval, pruning any that
// have already expired.
func (g *Gateway) registerPendingApproval(pa pendingApproval) {
	g.approvalMu.Lock()
	defer g.approvalMu.Unlock()

	now := time.Now()
	for key, existing := range g.pendingApprovals {
		if !existing.expiresAt.After(now) {
			delete(g.pendingApprovals, key)
		}
	}
	g.pendingApprovals[pa.token] = &pa
}

// removePendingApproval drops a pending tool approval, if present.
func (g *Gateway) removePendingApproval(token string) {
	g.approvalMu.Lock()
	defer g.approvalMu.Unlock()
	delete(g.pendingApprovals, token)
}

// consumePendingApproval validates and atomically consumes the pending
// approval for the callback. It rejects expired tokens and callbacks from
// anyone other than the intended recipient, and enforces single use.
func (g *Gateway) consumePendingApproval(token string, recipient int64) (*pendingApproval, bool) {
	g.approvalMu.Lock()
	defer g.approvalMu.Unlock()

	pa, found := g.pendingApprovals[token]
	if found && pa.recipient == recipient && pa.expiresAt.After(time.Now()) {
		delete(g.pendingApprovals, token)
	}
	return pa, found && pa.recipient == recipient && pa.expiresAt.After(time.Now())
}

// handleApprovalCallback is the entrypoint for tool-approval inline-button
// taps. It validates the token (recipient, expiry, single use) and delivers
// the decision to the blocked RequestApproval call.
func (g *Gateway) handleApprovalCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	query := update.CallbackQuery
	if query == nil {
		return
	}
	if !g.isSenderAllowed(query.From.ID) {
		g.log.Warn("approval callback from unauthorized sender", "user_id", query.From.ID)
		g.answerCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}

	decision, token := parseApprovalCallback(query.Data)
	if decision == "" {
		g.answerCallback(ctx, b, query.ID, "That action is no longer valid.", true)
		return
	}

	pa, ok := g.consumePendingApproval(token, query.From.ID)
	if !ok {
		g.answerCallback(ctx, b, query.ID, "That action is no longer valid.", true)
		return
	}

	g.log.Info(
		"tool approval decision",
		"action", pa.action, "decision", decision,
		"recipient", query.From.ID, "username", query.From.Username,
	)

	switch decision {
	case "approve":
		pa.resultCh <- approvalResult{decision: messaging.ApprovalApproved}
		g.answerCallback(ctx, b, query.ID, "Approved and executed.", false)
		g.editCallbackMessage(ctx, b, query, "✅ Approved: "+pa.description)
	case "permanent":
		pa.resultCh <- approvalResult{decision: messaging.ApprovalPermanentlyApproved}
		g.answerCallback(ctx, b, query.ID, "Permanently approved (valid 24h).", false)
		g.editCallbackMessage(ctx, b, query, "✅ Approved (permanent, 24h): "+pa.description)
	case "deny":
		pa.resultCh <- approvalResult{decision: messaging.ApprovalDenied, err: messaging.ErrApprovalDenied}
		g.answerCallback(ctx, b, query.ID, "Action denied.", false)
		g.editCallbackMessage(ctx, b, query, "❌ Denied: "+pa.description)
	}
}

// parseApprovalCallback splits an approval callback into decision
// ("approve", "permanent", "deny") and token.
func parseApprovalCallback(data string) (decision, token string) {
	data = strings.TrimPrefix(data, approvalCallbackPrefix)
	decision, token, _ = strings.Cut(data, ":")
	if decision != "approve" && decision != "permanent" && decision != "deny" {
		return "", ""
	}
	return decision, token
}

// approvalPromptText renders the message shown above the approval keyboard.
// The expiry figure is derived from the shared constant so a change to the
// approval window cannot leave the prompt out of date.
func approvalPromptText(action, description string) string {
	minutes := int(messaging.ToolApprovalTimeout.Minutes())
	return fmt.Sprintf(
		"⚠️ Approval required\n\n"+
			"Action: %s\n"+
			"%s\n\n"+
			"This request expires in %d minutes.",
		action, description, minutes,
	)
}

// answerCallback is the common acknowledge/shake response.
func (g *Gateway) answerCallback(ctx context.Context, b *bot.Bot, queryID, text string, alert bool) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: queryID,
		Text:            text,
		ShowAlert:       alert,
	}); err != nil {
		g.log.Warn("answer callback failed", "error", err)
	}
}

func (g *Gateway) editCallbackMessage(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, text string) {
	if query.Message.Message == nil {
		return
	}
	message := query.Message.Message
	if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    message.Chat.ID,
		MessageID: message.ID,
		Text:      text,
	}); err != nil {
		g.log.Warn("edit callback message failed", "error", err)
	}
}

func makeCallbackToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

// permanentApproval records a human's decision to approve one tool for 24
// hours. Scope and lifetime are explicit.
type permanentApproval struct {
	Pattern   string
	Recipient int64
	ExpiresAt time.Time
}

func (g *Gateway) recordPermanentApproval(recipient int64, pattern string) {
	g.permanentMu.Lock()
	defer g.permanentMu.Unlock()
	g.permanentApprovals = append(g.livePermanentApprovals(), permanentApproval{
		Pattern: pattern, Recipient: recipient, ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	g.log.Info("tool permanently approved for 24h", "tool", pattern, "recipient", recipient)
}

// hasPermanentApprovalExact reports whether recipient approved pattern within
// the last 24 hours.
func (g *Gateway) hasPermanentApprovalExact(recipient int64, pattern string) bool {
	g.permanentMu.Lock()
	defer g.permanentMu.Unlock()
	g.permanentApprovals = g.livePermanentApprovals()
	for _, pa := range g.permanentApprovals {
		if pa.Recipient == recipient && pa.Pattern == pattern {
			return true
		}
	}
	return false
}

// livePermanentApprovals drops expired approvals. permanentMu must be held.
func (g *Gateway) livePermanentApprovals() []permanentApproval {
	now := time.Now()
	kept := g.permanentApprovals[:0]
	for _, pa := range g.permanentApprovals {
		if pa.ExpiresAt.After(now) {
			kept = append(kept, pa)
		}
	}
	return kept
}
