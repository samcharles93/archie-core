package telegram

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

const providerCallbackPrefix = "provider:"

func isProviderSelectorRequest(text string) bool {
	fields := strings.Fields(text)
	if len(fields) != 1 {
		return false
	}
	command, _, _ := strings.Cut(fields[0], "@")
	return command == "/provider"
}

func (g *Gateway) sendProviderSelector(
	ctx context.Context,
	b *bot.Bot,
	msg *models.Message,
	router *gateway.Router,
) {
	manager, ok := router.Models.(gateway.ProviderModelManager)
	if !ok || len(manager.Providers()) == 0 {
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Provider switching is not configured.")
		return
	}
	params := &bot.SendMessageParams{
		ChatID:      msg.Chat.ID,
		Text:        providerSelectorText(manager.ActiveProvider()),
		ReplyMarkup: g.providerSelectorKeyboard(manager),
	}
	if msg.MessageThreadID != 0 {
		params.MessageThreadID = msg.MessageThreadID
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		g.log.Error("send provider selector failed", "error", err)
	}
}

func providerSelectorText(active string) string {
	if active == "" {
		return "Choose a provider:"
	}
	return "Choose a provider:\nActive: " + providerDisplayName(active)
}

func (g *Gateway) providerSelectorKeyboard(manager gateway.ProviderModelManager) *models.InlineKeyboardMarkup {
	active := manager.ActiveProvider()
	providers := manager.Providers()
	rows := make([][]models.InlineKeyboardButton, 0, len(providers))
	for _, provider := range providers {
		label := providerDisplayName(provider)
		if provider == active {
			label = "✓ " + label
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         label,
			CallbackData: g.providerCallbackToken(provider),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (g *Gateway) providerCallbackToken(provider string) string {
	sum := sha256.Sum256([]byte(provider))
	token := fmt.Sprintf("%s%x", providerCallbackPrefix, sum[:24])
	g.providerMu.Lock()
	g.providerCallbacks[token] = provider
	g.providerMu.Unlock()
	return token
}

func (g *Gateway) providerForCallback(token string) (string, bool) {
	g.providerMu.RLock()
	defer g.providerMu.RUnlock()
	provider, ok := g.providerCallbacks[token]
	return provider, ok
}

func providerDisplayName(provider string) string {
	switch strings.ToLower(provider) {
	case "openai":
		return "OpenAI"
	case "openrouter":
		return "OpenRouter"
	case "anthropic":
		return "Anthropic"
	case "deepseek":
		return "DeepSeek"
	case "google":
		return "Google"
	case "ollama":
		return "Ollama"
	default:
		return provider
	}
}

func (g *Gateway) handleProviderCallback(
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
		g.log.Warn("provider callback from unauthorized sender", "user_id", query.From.ID)
		g.answerModelCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}
	manager, ok := router.Models.(gateway.ProviderModelManager)
	if !ok {
		g.answerModelCallback(ctx, b, query.ID, "Provider switching is not configured.", true)
		return
	}
	selected, ok := g.providerForCallback(query.Data)
	if !ok {
		g.answerModelCallback(ctx, b, query.ID, "That provider selection is no longer valid.", true)
		return
	}
	if err := manager.SetActiveProvider(ctx, selected); err != nil {
		g.answerModelCallback(ctx, b, query.ID, fmt.Sprintf("Cannot switch: %v", err), true)
		return
	}
	g.answerModelCallback(ctx, b, query.ID, "Active provider: "+providerDisplayName(selected), false)
	g.updateProviderSelector(ctx, b, query, manager)
}

func (g *Gateway) updateProviderSelector(
	ctx context.Context,
	b *bot.Bot,
	query *models.CallbackQuery,
	manager gateway.ProviderModelManager,
) {
	params := &bot.EditMessageTextParams{
		Text:        providerSelectorText(manager.ActiveProvider()),
		ReplyMarkup: g.providerSelectorKeyboard(manager),
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
		g.log.Warn("update provider selector failed", "error", err)
	}
}
