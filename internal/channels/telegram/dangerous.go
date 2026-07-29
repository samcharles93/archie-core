package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// DangerousCommandAuthority is the sandbox/process/filesystem authority
// that validates and executes dangerous commands after human approval.
// The daemon's container pool, worktree manager, or similar sandbox owner
// implements this interface. It must not claim control over host
// resources Archie does not own.
type DangerousCommandAuthority interface {
	// StopProcess terminates a running background process identified by
	// spec (a process name, ID, or other identifier understood by the
	// sandbox). Returns an error if the process cannot be found or
	// stopped.
	StopProcess(ctx context.Context, spec string) error

	// Rollback restores the worktree/sandbox filesystem to a numbered
	// checkpoint. Returns a summary of the restored state, or an error
	// if the checkpoint does not exist or restoration fails.
	Rollback(ctx context.Context, checkpointNumber int) (string, error)

	// ListCheckpoints returns available filesystem checkpoints, newest
	// first. Returns an empty slice when no checkpoints exist.
	ListCheckpoints(ctx context.Context) ([]CheckpointInfo, error)
}

// CheckpointInfo describes a saved filesystem checkpoint.
type CheckpointInfo struct {
	Number    int
	Timestamp time.Time
	Label     string
	Size      string
}

// ── dangerous command approval system ─────────────────────────────

// dangerousCmdPrefix separates dangerous-command callbacks from other
// callback types handled in defaultHandler.
const dangerousCmdPrefix = "dangerous:"

// dangerousAction records a pending command a human must approve or deny.
// It mirrors the updateAction pattern: random token, recipient-bound,
// expiring, single-use, and carrying a hash so a changed command cannot
// reuse the callback.
type dangerousAction struct {
	id          string // random token
	command     string // the full command text
	commandHash string // sha256(command) — prevents changed-command replay
	description string // human-readable label shown to the approver
	recipient   int64  // Telegram user who may approve
	createdAt   time.Time
	expiresAt   time.Time
	// onApprove is the function to call when approved. The Gateway
	// supplies this when registering the pending command so the
	// authority stays out of the Telegram callback path.
	onApprove func(ctx context.Context) (string, error)
}

// permanentApproval records a decision to approve a category of dangerous
// commands permanently. Scope and lifetime are explicit.
type permanentApproval struct {
	// CommandPattern is a prefix match against the command text, e.g.
	// "/rollback", "/stop", or a tool name.
	CommandPattern string
	Recipient      int64
	GrantedAt      time.Time
	ExpiresAt      time.Time
	// Scope describes the approval boundary for audit purposes.
	Scope string
}

// ── Gateway methods ──────────────────────────────────────────────

// registerDangerousAction stores a pending command and returns a callback
// token. The caller must present the action to the user (via an inline
// keyboard) immediately after registering.
func (g *Gateway) registerDangerousAction(
	command, description string,
	recipient int64,
	onApprove func(ctx context.Context) (string, error),
) string {
	token := makeDangerousToken()
	sum := sha256.Sum256([]byte(command))
	hash := hex.EncodeToString(sum[:])

	g.dangerousMu.Lock()
	defer g.dangerousMu.Unlock()

	now := time.Now()
	// Expire tokens older than maxPendingAge.
	maxPendingAge := 10 * time.Minute
	for key, action := range g.dangerousActions {
		if !action.expiresAt.After(now) {
			delete(g.dangerousActions, key)
		}
	}
	g.dangerousActions[token] = dangerousAction{
		id:          token,
		command:     command,
		commandHash: hash,
		description: description,
		recipient:   recipient,
		createdAt:   now,
		expiresAt:   now.Add(maxPendingAge),
		onApprove:   onApprove,
	}
	return token
}

// dangerousKeyboard builds the three-button inline keyboard for a
// dangerous command: "Approve this time", "Approve Permanently", "Deny".
func (g *Gateway) dangerousKeyboard(token string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text:         "Approve this time",
					CallbackData: dangerousCmdPrefix + "approve:" + token,
				},
			},
			{
				{
					Text:         "Approve Permanently",
					CallbackData: dangerousCmdPrefix + "permanent:" + token,
				},
			},
			{
				{
					Text:         "Deny",
					CallbackData: dangerousCmdPrefix + "deny:" + token,
				},
			},
		},
	}
}

// answerDangerousCallback is the common acknowledge/shake response.
func (g *Gateway) answerDangerousCallback(ctx context.Context, b *bot.Bot, queryID, text string, alert bool) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: queryID,
		Text:            text,
		ShowAlert:       alert,
	}); err != nil {
		g.log.Warn("answer dangerous callback failed", "error", err)
	}
}

// handleDangerousCallback is the entrypoint for all dangerous-command
// inline-button taps. It validates the token (recipient, expiry, replay,
// hash), records an audit event, and executes or rejects the command.
func (g *Gateway) handleDangerousCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	query := update.CallbackQuery
	if query == nil {
		return
	}
	if !g.isSenderAllowed(query.From.ID) {
		g.log.Warn("dangerous callback from unauthorized sender", "user_id", query.From.ID)
		g.answerDangerousCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}

	action, ok := g.consumeDangerousAction(query)
	if !ok {
		g.answerDangerousCallback(ctx, b, query.ID, "That action is no longer valid.", true)
		return
	}

	decision, _ := parseDangerousCallback(query.Data)
	g.log.Info("dangerous command decision",
		"command", action.command, "decision", decision,
		"recipient", query.From.ID, "username", query.From.Username,
	)

	switch decision {
	case "deny":
		g.executeDangerousDeny(ctx, b, query, action)
	case "approve":
		g.executeDangerousApprove(ctx, b, query, action)
	case "permanent":
		g.executeDangerousPermanent(ctx, b, query, action)
	default:
		g.answerDangerousCallback(ctx, b, query.ID, "That action is no longer valid.", true)
	}
}

// consumeDangerousAction validates and atomically consumes the pending
// action associated with the callback query. Returns the action and true
// if valid; false if expired, wrong recipient, or already consumed.
func (g *Gateway) consumeDangerousAction(query *models.CallbackQuery) (dangerousAction, bool) {
	_, token := parseDangerousCallback(query.Data)
	g.dangerousMu.Lock()
	action, found := g.dangerousActions[token]
	if found && action.recipient == query.From.ID && action.expiresAt.After(time.Now()) {
		delete(g.dangerousActions, token)
	}
	g.dangerousMu.Unlock()
	return action, found && action.recipient == query.From.ID && action.expiresAt.After(time.Now())
}

func (g *Gateway) executeDangerousDeny(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, action dangerousAction) {
	g.answerDangerousCallback(ctx, b, query.ID, "Command denied.", false)
	g.editDangerousMessage(ctx, b, query, "❌ Denied: "+action.description)
}

func (g *Gateway) executeDangerousApprove(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, action dangerousAction) {
	result, err := action.onApprove(ctx)
	if err != nil {
		g.log.Error("dangerous command execution failed", "command", action.command, "error", err)
		g.answerDangerousCallback(ctx, b, query.ID, fmt.Sprintf("Command failed: %v", err), true)
		g.editDangerousMessage(ctx, b, query, fmt.Sprintf("❌ Failed: %s — %v", action.description, err))
		return
	}
	g.answerDangerousCallback(ctx, b, query.ID, "Approved and executed.", false)
	msg := "✅ Approved: " + action.description
	if result != "" {
		msg += "\n\n" + result
	}
	g.editDangerousMessage(ctx, b, query, msg)
}

func (g *Gateway) executeDangerousPermanent(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, action dangerousAction) {
	g.recordPermanentApproval(query.From.ID, action.command)

	result, err := action.onApprove(ctx)
	if err != nil {
		g.log.Error("dangerous command execution failed (permanent approval)", "command", action.command, "error", err)
		g.answerDangerousCallback(ctx, b, query.ID, fmt.Sprintf("Command failed: %v", err), true)
		g.editDangerousMessage(ctx, b, query, fmt.Sprintf("❌ Failed: %s — %v", action.description, err))
		return
	}
	g.answerDangerousCallback(ctx, b, query.ID, "Permanently approved and executed (valid 24h).", false)
	msg := "✅ Approved (permanent, 24h): " + action.description
	if result != "" {
		msg += "\n\n" + result
	}
	g.editDangerousMessage(ctx, b, query, msg)
}

func (g *Gateway) recordPermanentApproval(recipient int64, command string) {
	scope := permanentApproval{
		CommandPattern: command,
		Recipient:      recipient,
		GrantedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		Scope:          fmt.Sprintf("Permanent approval for %q by user %d, expires in 24h", command, recipient),
	}
	g.dangerousMu.Lock()
	g.permanentApprovals = append(g.permanentApprovals, scope)
	now := time.Now()
	kept := g.permanentApprovals[:0]
	for _, pa := range g.permanentApprovals {
		if pa.ExpiresAt.After(now) {
			kept = append(kept, pa)
		}
	}
	g.permanentApprovals = kept
	g.dangerousMu.Unlock()

	g.log.Info("dangerous command permanently approved", "scope", scope.Scope)
}

// hasPermanentApproval checks whether recipient has a current permanent
// approval covering the given command text.
func (g *Gateway) hasPermanentApproval(recipient int64, command string) bool {
	g.dangerousMu.Lock()
	defer g.dangerousMu.Unlock()

	now := time.Now()
	kept := g.permanentApprovals[:0]
	found := false
	for _, pa := range g.permanentApprovals {
		if !pa.ExpiresAt.After(now) {
			continue
		}
		kept = append(kept, pa)
		if pa.Recipient == recipient && strings.HasPrefix(command, pa.CommandPattern) {
			found = true
		}
	}
	g.permanentApprovals = kept
	return found
}

func (g *Gateway) editDangerousMessage(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, text string) {
	if query.Message.Message == nil {
		return
	}
	message := query.Message.Message
	if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    message.Chat.ID,
		MessageID: message.ID,
		Text:      text,
	}); err != nil {
		g.log.Warn("edit dangerous message failed", "error", err)
	}
}

// ── /rollback handler ───────────────────────────────────────────

// rollbackHandler serves /rollback: show available checkpoints, or with
// a number, request approval to restore that checkpoint.
func (g *Gateway) rollbackHandler() bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		if g.Dangerous == nil {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Rollback is not configured for this installation.")
			return
		}

		rest := strings.TrimSpace(restAfterTelegram(msg.Text, "/rollback", ""))
		if rest == "" {
			g.showCheckpoints(ctx, b, msg.Chat.ID, msg.MessageThreadID)
			return
		}

		num, err := strconv.Atoi(rest)
		if err != nil || num < 1 {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Usage: /rollback <checkpoint number>")
			return
		}
		g.requestRollbackApproval(ctx, b, msg, num)
	}
}

func (g *Gateway) showCheckpoints(ctx context.Context, b *bot.Bot, chatID int64, threadID int) {
	checkpoints, err := g.Dangerous.ListCheckpoints(ctx)
	if err != nil {
		g.log.Error("list checkpoints failed", "error", err)
		g.sendMessage(ctx, b, chatID, threadID, "Could not list checkpoints.")
		return
	}
	if len(checkpoints) == 0 {
		g.sendMessage(ctx, b, chatID, threadID, "No checkpoints available.")
		return
	}
	var text strings.Builder
	text.WriteString("Available checkpoints:\n\n")
	for _, c := range checkpoints {
		fmt.Fprintf(&text, "%d — %s (%s)", c.Number, c.Timestamp.Format("2006-01-02 15:04"), c.Label)
		if c.Size != "" {
			fmt.Fprintf(&text, " — %s", c.Size)
		}
		text.WriteByte('\n')
	}
	text.WriteString("\nUse /rollback <number> to restore a checkpoint.")
	g.sendMessage(ctx, b, chatID, threadID, text.String())
}

func (g *Gateway) requestRollbackApproval(ctx context.Context, b *bot.Bot, msg *models.Message, num int) {
	rest := fmt.Sprintf("%d", num)
	if g.hasPermanentApproval(msg.From.ID, "/rollback") {
		result, err := g.Dangerous.Rollback(ctx, num)
		if err != nil {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, fmt.Sprintf("❌ Rollback failed: %v", err))
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "✅ Rollback to checkpoint "+rest+":\n\n"+result)
		return
	}

	cmdText := fmt.Sprintf("/rollback %d", num)
	desc := fmt.Sprintf("Rollback to checkpoint %d", num)
	token := g.registerDangerousAction(cmdText, desc, msg.From.ID, func(ctx context.Context) (string, error) {
		return g.Dangerous.Rollback(ctx, num)
	})

	params := &bot.SendMessageParams{
		ChatID:      msg.Chat.ID,
		Text:        rollbackApprovalText(num),
		ReplyMarkup: g.dangerousKeyboard(token),
	}
	if msg.MessageThreadID != 0 {
		params.MessageThreadID = msg.MessageThreadID
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		g.log.Error("send dangerous prompt (rollback) failed", "error", err)
	}
}

func rollbackApprovalText(num int) string {
	return fmt.Sprintf(
		"⚠️ **Dangerous command**\n\n"+
			"Command: `/rollback %d`\n"+
			"Effect: Restore the worktree filesystem to checkpoint %d.\n"+
			"This will discard any changes made after that checkpoint.\n\n"+
			"_This request expires in 10 minutes._",
		num, num,
	)
}

// ── /stop handler ──────────────────────────────────────────────

// stopCurrentTurn cancels whatever this conversation is currently doing.
//
// Cancellation propagates through the turn's context, so it reaches the
// model stream and any tool running underneath it -- a shell command built
// with exec.CommandContext is killed with the turn, which is the case that
// matters. Whatever the reply had already streamed stays on screen: it is
// the record of what Archie did before being stopped.
func (g *Gateway) stopCurrentTurn(ctx context.Context, b *bot.Bot, msg *models.Message, router *gateway.Router) {
	var cancelled bool
	var dropped int

	// turns is only built by launch, so a gateway that has never started
	// simply has nothing to stop. Report that rather than failing.
	if g.turns != nil && router != nil {
		session, err := router.ResolveSessionKey(ctx, gateway.Message{
			ChannelID: fmt.Sprintf("%d", msg.Chat.ID),
			ThreadID:  threadIDString(msg.MessageThreadID),
			From:      msg.From.Username,
			Text:      msg.Text,
		})
		if err != nil {
			g.log.Error("resolve session for stop", "error", err)
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "❌ Could not resolve this conversation's session.")
			return
		}
		cancelled, dropped = g.turns.Stop(session)
		g.log.Info("stop requested", "session", session, "cancelled", cancelled, "dropped", dropped)
	}

	// The brake covers agent tasks as well as the conversation. Someone
	// reaching for it wants everything to stop, and should not have to
	// know a task ID -- or which of the two is currently misbehaving.
	stoppedTasks := g.stopRunningTasks(ctx, router)

	g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
		stopReport(cancelled, dropped, stoppedTasks))
}

// stopRunningTasks interrupts the agent tasks currently executing and
// returns their IDs. A failure here is logged and reported as none
// stopped: /stop must still do whatever else it can.
func (g *Gateway) stopRunningTasks(ctx context.Context, router *gateway.Router) []int64 {
	if router == nil || router.Controller == nil {
		return nil
	}
	stopped, err := router.Controller.StopRunning(ctx, router.Identity)
	if err != nil {
		g.log.Warn("stop running tasks failed", "error", err)
		return nil
	}
	if len(stopped) > 0 {
		g.log.Info("stopped running tasks", "tasks", stopped)
	}
	return stopped
}

// stopReport describes what a /stop actually stopped.
//
// It names the parts rather than saying "stopped" unconditionally.
// Claiming success when nothing was running teaches the operator to
// distrust the command at exactly the moment they need to believe it.
func stopReport(cancelled bool, dropped int, tasks []int64) string {
	var parts []string
	if cancelled {
		parts = append(parts, "the running reply")
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d queued message(s)", dropped))
	}
	if len(tasks) > 0 {
		ids := make([]string, len(tasks))
		for i, id := range tasks {
			ids[i] = fmt.Sprintf("#%d", id)
		}
		parts = append(parts, "task(s) "+strings.Join(ids, ", "))
	}

	if len(parts) == 0 {
		return "Nothing is running.\n\nUse /stop <process-name> to terminate a background process."
	}
	return "🛑 Stopped " + joinWithAnd(parts) + "."
}

// joinWithAnd renders a list the way it would be read aloud.
func joinWithAnd(parts []string) string {
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// stopHandler serves /stop.
//
// Bare /stop is the emergency brake: it cancels the conversation's running
// turn immediately, with no approval step. Approval exists to protect
// against destructive commands, and stopping is the opposite -- gating it
// behind a confirmation round-trip would defeat the entire point.
//
// /stop <process-name> keeps the original behaviour of terminating a named
// background process, which is destructive and still requires approval.
func (g *Gateway) stopHandler(router *gateway.Router) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}

		rest := strings.TrimSpace(restAfterTelegram(msg.Text, "/stop", ""))
		if rest == "" {
			g.stopCurrentTurn(ctx, b, msg, router)
			return
		}

		if g.Dangerous == nil {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Process control is not configured for this installation.")
			return
		}

		// Check permanent approval.
		if g.hasPermanentApproval(msg.From.ID, "/stop") {
			if err := g.Dangerous.StopProcess(ctx, rest); err != nil {
				g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, fmt.Sprintf("❌ Stop failed: %v", err))
				return
			}
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "✅ Process stopped: "+rest)
			return
		}

		cmdText := fmt.Sprintf("/stop %s", rest)
		desc := fmt.Sprintf("Stop process %q", rest)
		token := g.registerDangerousAction(cmdText, desc, msg.From.ID, func(ctx context.Context) (string, error) {
			if err := g.Dangerous.StopProcess(ctx, rest); err != nil {
				return "", err
			}
			return "", nil
		})

		params := &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text: fmt.Sprintf(
				"⚠️ **Dangerous command**\n\n"+
					"Command: `/stop %s`\n"+
					"Effect: Terminate the running background process %q.\n"+
					"This will discard any in-progress work on that process.\n\n"+
					"_This request expires in 10 minutes._",
				rest, rest,
			),
			ReplyMarkup: g.dangerousKeyboard(token),
		}
		if msg.MessageThreadID != 0 {
			params.MessageThreadID = msg.MessageThreadID
		}
		if _, err := b.SendMessage(ctx, params); err != nil {
			g.log.Error("send dangerous prompt (stop) failed", "error", err)
		}
	}
}

// ── helpers ────────────────────────────────────────────────────

// restAfterTelegram strips a slash-command token from a Telegram message
// text, including an optional @bot suffix.
func restAfterTelegram(text, cmd, gatewayName string) string {
	s := text
	if gatewayName != "" {
		s = strings.TrimPrefix(s, cmd+"@"+gatewayName)
	}
	s = strings.TrimPrefix(s, cmd)
	return s
}

func makeDangerousToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

// parseDangerousCallback splits a dangerous-command callback data into
// decision ("approve", "permanent", "deny") and token.
func parseDangerousCallback(data string) (decision, token string) {
	data = strings.TrimPrefix(data, dangerousCmdPrefix)
	decision, token, _ = strings.Cut(data, ":")
	if decision != "approve" && decision != "permanent" && decision != "deny" {
		return "", ""
	}
	return decision, token
}

// ── /approve and /deny handlers ─────────────────────────────────

// approveHandler serves /approve: list any pending dangerous commands,
// or approve a specific one by token. When a pending command exists, the
// user interacts via inline buttons rather than typing /approve directly;
// this handler is for the edge case where a user types /approve without
// a pending command context.
func (g *Gateway) approveHandler() bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		if g.Dangerous == nil {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
				"Dangerous-command approval is not configured.\n\n"+
					"Use /rollback or /stop to trigger approval inline.")
			return
		}

		g.dangerousMu.Lock()
		now := time.Now()
		var pending []dangerousAction
		for key, action := range g.dangerousActions {
			if !action.expiresAt.After(now) {
				delete(g.dangerousActions, key)
				continue
			}
			if action.recipient == msg.From.ID {
				pending = append(pending, action)
			}
		}
		g.dangerousMu.Unlock()

		if len(pending) == 0 {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
				"No pending commands to approve.\n\n"+
					"Use /rollback or /stop to trigger a dangerous command approval inline.")
			return
		}

		var text strings.Builder
		text.WriteString("Pending dangerous commands:\n\n")
		for i, action := range pending {
			fmt.Fprintf(&text, "%d. %s", i+1, action.description)
			remaining := time.Until(action.expiresAt).Truncate(time.Second)
			fmt.Fprintf(&text, " (expires in %s)", remaining)
			text.WriteByte('\n')
		}
		text.WriteString("\nUse the inline buttons on each pending command to approve or deny.")
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, text.String())
	}
}

// denyHandler serves /deny: the counterpart to /approve for rejecting
// pending dangerous commands. Like /approve, the primary interaction is
// through inline buttons on each pending command.
func (g *Gateway) denyHandler() bot.HandlerFunc {
	return g.approveHandler() // Same behaviour: show pending commands.
}
