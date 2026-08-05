package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	progress := func(text string) {
		g.sendMessage(ctx, b, message.Chat.ID, message.MessageThreadID, text)
	}
	if err := g.Updates.Install(ctx, progress); err != nil {
		g.log.Error("approved update failed", "error", err)
		progress("Update failed. Archie is still running; check the installation logs and try again.")
		return
	}
	progress("Update completed. Archie will use the new version after it restarts.")
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
