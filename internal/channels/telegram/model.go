package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

const modelCallbackPrefix = "model:"

func isModelSelectorRequest(text string) bool {
	fields := strings.Fields(text)
	if len(fields) != 1 {
		return false
	}
	command, _, _ := strings.Cut(fields[0], "@")
	return command == "/model"
}

func (g *Gateway) sendModelSelector(
	ctx context.Context,
	b *bot.Bot,
	msg *models.Message,
	router *gateway.Router,
) {
	if router.Models == nil || len(router.Models.Models()) == 0 {
		reply, err := router.Route(ctx, gateway.Message{Text: "/model"})
		if err != nil {
			g.log.Error("model selector fallback failed", "error", err)
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, reply)
		return
	}

	params := &bot.SendMessageParams{
		ChatID:      msg.Chat.ID,
		Text:        modelSelectorText(router.Models.ActiveModel()),
		ReplyMarkup: modelSelectorKeyboard(router.Models),
	}
	if msg.MessageThreadID != 0 {
		params.MessageThreadID = msg.MessageThreadID
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		g.log.Error("send model selector failed", "error", err)
	}
}

func modelSelectorText(active string) string {
	if active == "" {
		return "Choose a model:"
	}
	return "Choose a model:\nActive: " + active
}

func modelSelectorKeyboard(manager gateway.ModelManager) *models.InlineKeyboardMarkup {
	active := manager.ActiveModel()
	available := selectableModels(manager)
	activeProvider := ""
	if providerManager, ok := manager.(gateway.ProviderModelManager); ok {
		activeProvider = providerManager.ActiveProvider()
	}
	rows := make([][]models.InlineKeyboardButton, 0, len(available))
	for index, model := range available {
		label := model
		if activeProvider != "" {
			label = strings.TrimPrefix(model, activeProvider+"/")
		}
		if model == active {
			label = "✓ " + model
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         label,
			CallbackData: modelCallbackPrefix + strconv.Itoa(index),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func selectableModels(manager gateway.ModelManager) []string {
	if providerManager, ok := manager.(gateway.ProviderModelManager); ok {
		return providerManager.ModelsForProvider(providerManager.ActiveProvider())
	}
	return manager.Models()
}

func (g *Gateway) handleModelCallback(
	ctx context.Context,
	b *bot.Bot,
	update *models.Update,
	router *gateway.Router,
) {
	query := update.CallbackQuery
	if query == nil {
		return
	}
	if !g.isSenderAllowed(query.From.ID) {
		g.log.Warn("model callback from unauthorized sender", "user_id", query.From.ID)
		g.answerModelCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}
	if router.Models == nil {
		g.answerModelCallback(ctx, b, query.ID, "Model switching is not configured.", true)
		return
	}

	indexText := strings.TrimPrefix(query.Data, modelCallbackPrefix)
	index, err := strconv.Atoi(indexText)
	available := selectableModels(router.Models)
	if err != nil || index < 0 || index >= len(available) {
		g.answerModelCallback(ctx, b, query.ID, "That model selection is no longer valid.", true)
		return
	}
	selected := available[index]
	if err := router.Models.SetActiveModel(ctx, selected); err != nil {
		g.answerModelCallback(ctx, b, query.ID, fmt.Sprintf("Cannot switch: %v", err), true)
		return
	}

	g.answerModelCallback(ctx, b, query.ID, "Active model: "+selected, false)
	g.updateModelSelector(ctx, b, query, router.Models)
}

func (g *Gateway) answerModelCallback(
	ctx context.Context,
	b *bot.Bot,
	queryID string,
	text string,
	alert bool,
) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: queryID,
		Text:            text,
		ShowAlert:       alert,
	}); err != nil {
		g.log.Warn("answer model callback failed", "error", err)
	}
}

func (g *Gateway) updateModelSelector(
	ctx context.Context,
	b *bot.Bot,
	query *models.CallbackQuery,
	manager gateway.ModelManager,
) {
	params := &bot.EditMessageTextParams{
		Text:        modelSelectorText(manager.ActiveModel()),
		ReplyMarkup: modelSelectorKeyboard(manager),
	}
	switch {
	case query.Message.Message != nil:
		params.ChatID = query.Message.Message.Chat.ID
		params.MessageID = query.Message.Message.ID
	case query.InlineMessageID != "":
		params.InlineMessageID = query.InlineMessageID
	default:
		return
	}
	if _, err := b.EditMessageText(ctx, params); err != nil {
		g.log.Warn("update model selector failed", "error", err)
	}
}
