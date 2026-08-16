package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// approvalCallbackPrefix separates tool-approval callbacks from other
// callback types handled in defaultHandler. It is deliberately distinct
// from dangerousCmdPrefix so the two approval systems never collide.
const approvalCallbackPrefix = "approval:"

// telegramApprover implements gateway.ApprovalRequester by rendering an
// inline-button prompt in the chat it was built for and blocking until the
// human decides, the context is cancelled, or the approval window elapses.
//
// It generalises the dangerous-command approval machinery: instead of
// carrying an onApprove callback that executes a fixed command, a tool
// approval carries a result channel that the callback handler signals, and
// the blocked RequestApproval caller observes the decision.
type telegramApprover struct {
	gw        *Gateway
	bot       *bot.Bot
	chatID    int64
	threadID  int
	recipient int64
}

// pendingApproval records a tool-approval request waiting on a human
// decision. It is the tool-approval analogue of dangerousAction, kept
// separate so the dangerous-command model (onApprove callbacks) and the
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
	decision gateway.ApprovalDecision
	err      error
}

// Compile-time guard.
var _ gateway.ApprovalRequester = (*telegramApprover)(nil)

// NewApprover returns an ApprovalRequester that renders prompts in the
// given chat. recipient is the Telegram user who may approve; in a private
// DM this is the same as chatID, in a group it is the sender of the turn
// that triggered the gated tool call.
func (g *Gateway) NewApprover(b *bot.Bot, chatID int64, threadID int, recipient int64) gateway.ApprovalRequester {
	return &telegramApprover{
		gw:        g,
		bot:       b,
		chatID:    chatID,
		threadID:  threadID,
		recipient: recipient,
	}
}

// RequestApproval asks the recipient to approve or deny action.
//
// A current permanent approval for the action short-circuits the prompt and
// returns ApprovalPermanentlyApproved. Otherwise the recipient is shown the
// three-button inline keyboard and the call blocks until the human decides,
// the context is cancelled, or the 2-minute approval window
// (gateway.ToolApprovalTimeout) elapses.
func (a *telegramApprover) RequestApproval(ctx context.Context, action, description string) (gateway.ApprovalDecision, error) {
	// Compose the lookup key so a permanent approval is scoped to
	// the specific resource: "Approve Permanently" on "delete session
	// abc123" only permanently approves that one session, not every
	// session_delete forever.
	permKey := action + "\x00" + description
	if a.gw.hasPermanentApprovalExact(a.recipient, permKey) {
		return gateway.ApprovalPermanentlyApproved, nil
	}

	token := makeDangerousToken()
	resultCh := make(chan approvalResult, 1)
	a.gw.registerPendingApproval(pendingApproval{
		token:       token,
		action:      action,
		description: description,
		recipient:   a.recipient,
		expiresAt:   time.Now().Add(gateway.ToolApprovalTimeout),
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

	approvalCtx, cancel := context.WithTimeout(ctx, gateway.ToolApprovalTimeout)
	defer cancel()

	if _, err := a.bot.SendMessage(approvalCtx, params); err != nil {
		a.gw.removePendingApproval(token)
		if approvalCtx.Err() != nil {
			return gateway.ApprovalDenied, approvalCtx.Err()
		}
		a.gw.log.Error("send approval prompt failed", "error", err)
		return gateway.ApprovalDenied, fmt.Errorf("send approval prompt: %w", err)
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
			return gateway.ApprovalDenied, approvalCtx.Err()
		}
	}
}

// applyApprovalDecision maps a human decision to the gateway contract,
// recording a permanent approval when the human asked for one.
func (a *telegramApprover) applyApprovalDecision(action, description string, result approvalResult) (gateway.ApprovalDecision, error) {
	switch result.decision {
	case gateway.ApprovalApproved:
		return gateway.ApprovalApproved, nil
	case gateway.ApprovalPermanentlyApproved:
		// Scoped: permanently approving "delete session abc123" only
		// silences the prompt for that one resource, not every call to
		// the same tool.
		a.gw.recordPermanentApproval(a.recipient, action+"\x00"+description)
		return gateway.ApprovalPermanentlyApproved, nil
	case gateway.ApprovalDenied:
		if result.err != nil {
			return gateway.ApprovalDenied, result.err
		}
		return gateway.ApprovalDenied, gateway.ErrApprovalDenied
	default:
		return gateway.ApprovalDenied, fmt.Errorf("unexpected approval decision %v", result.decision)
	}
}

// approvalKeyboard builds the three-button inline keyboard for a tool
// approval: "Approve this time", "Approve Permanently", "Deny". It mirrors
// dangerousKeyboard but carries the approval: callback prefix.
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
		g.answerDangerousCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}

	decision, token := parseApprovalCallback(query.Data)
	if decision == "" {
		g.answerDangerousCallback(ctx, b, query.ID, "That action is no longer valid.", true)
		return
	}

	pa, ok := g.consumePendingApproval(token, query.From.ID)
	if !ok {
		g.answerDangerousCallback(ctx, b, query.ID, "That action is no longer valid.", true)
		return
	}

	g.log.Info(
		"tool approval decision",
		"action", pa.action, "decision", decision,
		"recipient", query.From.ID, "username", query.From.Username,
	)

	switch decision {
	case "approve":
		pa.resultCh <- approvalResult{decision: gateway.ApprovalApproved}
		g.answerDangerousCallback(ctx, b, query.ID, "Approved and executed.", false)
		g.editDangerousMessage(ctx, b, query, "✅ Approved: "+pa.description)
	case "permanent":
		pa.resultCh <- approvalResult{decision: gateway.ApprovalPermanentlyApproved}
		g.answerDangerousCallback(ctx, b, query.ID, "Permanently approved (valid 24h).", false)
		g.editDangerousMessage(ctx, b, query, "✅ Approved (permanent, 24h): "+pa.description)
	case "deny":
		pa.resultCh <- approvalResult{decision: gateway.ApprovalDenied, err: gateway.ErrApprovalDenied}
		g.answerDangerousCallback(ctx, b, query.ID, "Action denied.", false)
		g.editDangerousMessage(ctx, b, query, "❌ Denied: "+pa.description)
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
	minutes := int(gateway.ToolApprovalTimeout.Minutes())
	return fmt.Sprintf(
		"⚠️ Approval required\n\n"+
			"Action: %s\n"+
			"%s\n\n"+
			"This request expires in %d minutes.",
		action, description, minutes,
	)
}
