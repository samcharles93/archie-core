package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

const (
	updateCallbackPrefix  = "update:"
	updateApproveCallback = updateCallbackPrefix + "approve:"
	updateDeferCallback   = updateCallbackPrefix + "defer:"
)

type updateAction struct {
	recipient int64
	snapshot  releaseupdate.Snapshot
	expiresAt time.Time
}

func (g *Gateway) updateHandler() bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		if g.Updates == nil {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Updates are not configured for this installation.")
			return
		}
		snapshot, err := g.Updates.Check(ctx, msg.From.ID)
		if err != nil {
			g.log.Warn("check updates failed", "error", err)
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "I couldn't check for updates right now.")
			return
		}
		g.sendUpdateMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, msg.From.ID, snapshot)
	}
}

func (g *Gateway) sendUpdateMessage(ctx context.Context, b *bot.Bot, chatID int64, threadID int, recipient int64, snapshot releaseupdate.Snapshot) {
	params := &bot.SendMessageParams{ChatID: chatID, Text: updateText(snapshot)}
	if len(snapshot.Available()) > 0 {
		if g.Updates.CanInstall() {
			params.ReplyMarkup = g.updateKeyboard(recipient, snapshot)
		}
	}
	if threadID != 0 {
		params.MessageThreadID = threadID
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		g.log.Error("send update status failed", "error", err)
	}
}

func updateText(snapshot releaseupdate.Snapshot) string {
	return releaseupdate.FormatSnapshot(snapshot)
}

func (g *Gateway) updateKeyboard(recipient int64, snapshot releaseupdate.Snapshot) *models.InlineKeyboardMarkup {
	token := makeUpdateToken()
	g.updateMu.Lock()
	now := time.Now()
	for key, action := range g.updateActions {
		if !action.expiresAt.After(now) {
			delete(g.updateActions, key)
		}
	}
	g.updateActions[token] = updateAction{recipient: recipient, snapshot: snapshot, expiresAt: now.Add(10 * time.Minute)}
	g.updateMu.Unlock()
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Approve update", CallbackData: updateApproveCallback + token},
		{Text: "Defer", CallbackData: updateDeferCallback + token},
	}}}
}

func makeUpdateToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

func (g *Gateway) handleUpdateCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	query := update.CallbackQuery
	if query == nil {
		return
	}
	if !g.isSenderAllowed(query.From.ID) {
		g.log.Warn("update callback from unauthorized sender", "user_id", query.From.ID)
		g.answerModelCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}
	if g.Updates == nil {
		g.answerModelCallback(ctx, b, query.ID, "Updates are not configured for this installation.", true)
		return
	}
	approved := strings.TrimPrefix(query.Data, updateApproveCallback)
	deferred := strings.TrimPrefix(query.Data, updateDeferCallback)
	token := approved
	if token == query.Data {
		token = deferred
	}
	g.updateMu.Lock()
	action, found := g.updateActions[token]
	if found && action.recipient == query.From.ID {
		delete(g.updateActions, token)
	}
	g.updateMu.Unlock()
	if !found || action.recipient != query.From.ID || !action.expiresAt.After(time.Now()) {
		g.answerModelCallback(ctx, b, query.ID, "That update action is no longer valid.", true)
		return
	}
	g.dispatchUpdateAction(ctx, b, query, action)
}

// dispatchUpdateAction routes the callback to defer, approve, or reject.
func (g *Gateway) dispatchUpdateAction(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, action updateAction) {
	switch {
	case strings.HasPrefix(query.Data, updateDeferCallback):
		if err := g.Updates.Defer(ctx, query.From.ID, action.snapshot); err != nil {
			g.log.Warn("defer update failed", "error", err)
			g.answerModelCallback(ctx, b, query.ID, "Couldn't defer the update.", true)
			return
		}
		g.answerModelCallback(ctx, b, query.ID, "Update deferred.", false)
		g.editUpdateMessage(ctx, b, query, "Update deferred. Archie will ask again when a newer release is available.")
	case strings.HasPrefix(query.Data, updateApproveCallback):
		fresh, err := g.Updates.Check(ctx, query.From.ID)
		if err != nil || !sameUpdateSnapshot(fresh, action.snapshot) {
			g.answerModelCallback(ctx, b, query.ID, "The available releases changed. Run /update again.", true)
			return
		}
		if !g.beginUpdate() {
			g.answerModelCallback(ctx, b, query.ID, "An update is already in progress.", true)
			return
		}
		g.answerModelCallback(ctx, b, query.ID, "Starting update…", false)
		g.editUpdateMessage(ctx, b, query, "Update approved. Starting now…")
		go g.installUpdate(ctx, b, query)
	default:
		g.answerModelCallback(ctx, b, query.ID, "That update action is no longer valid.", true)
	}
}

func (g *Gateway) beginUpdate() bool {
	g.updateMu.Lock()
	defer g.updateMu.Unlock()
	if g.updateInProgress {
		return false
	}
	g.updateInProgress = true
	return true
}

// installUpdate runs the synchronous phase of an update (build/install) and
// reports it live. It deliberately never claims the update itself
// succeeded: the reference installer queues the daemon restart through a
// detached unit precisely so restarting archied.service doesn't kill this
// call before it can report a clean exit, which means the restart -- and
// whether the new version actually comes up healthy -- happens after this
// function returns. That outcome is reported separately, by whichever
// archied process boots next, via SendPendingReport.
func (g *Gateway) installUpdate(ctx context.Context, b *bot.Bot, query *models.CallbackQuery) {
	defer func() {
		g.updateMu.Lock()
		g.updateInProgress = false
		g.updateMu.Unlock()
	}()
	message := query.Message.Message
	if message == nil {
		return
	}
	meta := releaseupdate.InstallMeta{
		Channel: "telegram", ChatID: message.Chat.ID, ThreadID: message.MessageThreadID,
		ReportPath: g.UpdateReportPath,
	}

	var lines []string
	progress := func(text string) {
		lines = append(lines, text)
		if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID: message.Chat.ID, MessageID: message.ID, Text: progressText(lines),
		}); err != nil {
			g.log.Warn("update progress message failed", "error", err)
		}
	}

	result, err := g.Updates.Install(ctx, meta, progress)
	if err != nil {
		g.log.Error("approved update failed", "error", err)
		lines = append(lines, formatInstallFailure(err))
		g.editUpdateMessage(ctx, b, query, progressText(lines))
		return
	}
	lines = append(lines, formatInstallSuccess(result))
	g.editUpdateMessage(ctx, b, query, progressText(lines))
}

// progressText joins accumulated status lines into one message, keeping the
// most recent lines when the total would exceed Telegram's message length
// limit -- the newest status matters most once history has to be dropped.
func progressText(lines []string) string {
	text := strings.Join(lines, "\n")
	runes := []rune(text)
	if len(runes) <= messageMaxLen {
		return text
	}
	return "…\n" + string(runes[len(runes)-messageMaxLen:])
}

// formatInstallSuccess reports the synchronous phase-1 outcome only: build
// and install succeeded, and a restart has been queued. It must not claim
// the update finished -- see installUpdate's doc comment.
func formatInstallSuccess(result releaseupdate.Result) string {
	var text strings.Builder
	text.WriteString("Build and install succeeded")
	if changes := formatVersionChanges(result); changes != "" {
		fmt.Fprintf(&text, ": %s", changes)
	}
	text.WriteString(".")
	if result.RestartRequested {
		text.WriteString(" Restart queued — I'll confirm once the new version is up and healthy.")
	}
	return text.String()
}

func formatInstallFailure(err error) string {
	return fmt.Sprintf("Update failed during build/install: %s. Nothing was restarted; archie-core is still running the previous version.", err)
}

func formatVersionChanges(result releaseupdate.Result) string {
	ids := make([]string, 0, len(result.Installed))
	for id := range result.Installed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		installed := result.Installed[id]
		previous := result.Previous[id]
		if previous == "" || previous == installed {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s → %s", id, previous, installed))
	}
	return strings.Join(parts, ", ")
}

// formatPendingReport describes the phase-2 outcome. There are three
// distinct cases, not two: the daemon came back up healthy; it didn't, and
// was rolled back to its previous version; or it didn't, and there was no
// backup to roll back to. That third case must never be worded like the
// second -- claiming a rollback that didn't happen tells the operator to
// stand down while the daemon may still be running the broken version, or
// not running at all.
func formatPendingReport(report releaseupdate.Report) string {
	changes := formatVersionChanges(releaseupdate.Result{Previous: report.Previous, Installed: report.Installed})
	if !report.RolledBack && report.HealthCheck == "passed" {
		text := "Update complete"
		if changes != "" {
			text += ": " + changes
		}
		return text + ". Health check passed — archie-core is healthy on the new version."
	}

	ids := make([]string, 0, len(report.Previous))
	for id := range report.Previous {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	versions := make([]string, 0, len(ids))
	for _, id := range ids {
		versions = append(versions, fmt.Sprintf("%s %s", id, report.Previous[id]))
	}
	previousVersions := strings.Join(versions, ", ")

	if report.RolledBack {
		return fmt.Sprintf("Update failed health check after restarting and was automatically rolled back to the previous version (%s). archie-core is running normally on the previous release.", previousVersions)
	}
	return fmt.Sprintf("Update failed health check after restarting and could NOT be rolled back automatically (no backup was found). archie-core may still be running the broken version, or may not be running at all — manual intervention is required. Previous version was %s.", previousVersions)
}

// reportPendingUpdate checks for a phase-2 outcome left by the update
// watchdog and relays it, exactly like announceRelease -- called on every
// gateway launch so it fires whether this is a fresh process boot (the
// common case after an update restart) or a later /restart. A report is
// cleared as soon as it's handled -- relayed, or found unreadable -- so a
// later launch never retries it. An unreadable report is discarded rather
// than left in place: without clearing it, every future launch would fail
// the same decode and the chat that approved the update would never learn
// any outcome at all.
func (g *Gateway) reportPendingUpdate(ctx context.Context, b *bot.Bot) {
	if g.UpdateReportPath == "" {
		return
	}
	report, found, err := releaseupdate.ReadPendingReport(g.UpdateReportPath)
	if err != nil {
		g.log.Warn("read pending update report failed; discarding it", "error", err)
		if clearErr := releaseupdate.ClearPendingReport(g.UpdateReportPath); clearErr != nil {
			g.log.Warn("clear unreadable update report failed", "error", clearErr)
		}
		return
	}
	if !found {
		return
	}
	g.SendPendingReport(ctx, b, report)
	if err := releaseupdate.ClearPendingReport(g.UpdateReportPath); err != nil {
		g.log.Warn("clear pending update report failed", "error", err)
	}
}

// SendPendingReport relays the phase-2 outcome of an update -- whether the
// restarted daemon came up healthy, or was rolled back -- to the chat that
// originally approved it. Called once, on startup, by whichever archied
// process finds a pending report left by the previous boot's install; see
// releaseupdate.ReadPendingReport.
func (g *Gateway) SendPendingReport(ctx context.Context, b *bot.Bot, report releaseupdate.Report) {
	g.sendMessage(ctx, b, report.ChatID, report.ThreadID, formatPendingReport(report))
}

func sameUpdateSnapshot(left, right releaseupdate.Snapshot) bool {
	return releaseupdate.SameAvailable(left, right)
}

func (g *Gateway) editUpdateMessage(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, text string) {
	if query.Message.Message == nil {
		return
	}
	message := query.Message.Message
	if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: message.Chat.ID, MessageID: message.ID, Text: text}); err != nil {
		g.log.Warn("update update message failed", "error", err)
	}
}
