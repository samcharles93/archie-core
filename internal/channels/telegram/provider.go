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
		Text:        providerSelectorText(manager.ActiveModel(), manager.ActiveProvider()),
		ReplyMarkup: g.providerSelectorKeyboard(manager),
	}
	if msg.MessageThreadID != 0 {
		params.MessageThreadID = msg.MessageThreadID
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		g.log.Error("send provider selector failed", "error", err)
	}
}

func providerSelectorText(activeModel, activeProvider string) string {
	return "⚙ Model Configuration\n\nCurrent model: " +
		modelDisplayName(activeModel, activeProvider) +
		"\nProvider: " + providerDisplayName(activeProvider) +
		"\n\nSelect a provider:"
}

func (g *Gateway) providerSelectorKeyboard(manager gateway.ProviderModelManager) *models.InlineKeyboardMarkup {
	active := manager.ActiveProvider()
	providers := manager.Providers()
	buttons := make([]models.InlineKeyboardButton, 0, len(providers))
	for _, provider := range providers {
		label := fmt.Sprintf("%s (%d)", providerName(manager, provider), len(manager.ModelsForProvider(provider)))
		if provider == active {
			label = "✓ " + label
		}
		buttons = append(buttons, models.InlineKeyboardButton{
			Text:         label,
			CallbackData: g.providerCallbackToken(provider),
		})
	}
	rows := make([][]models.InlineKeyboardButton, 0, (len(buttons)+1)/2)
	for i := 0; i < len(buttons); i += 2 {
		end := min(i+2, len(buttons))
		rows = append(rows, buttons[i:end])
	}
	rows = append(rows, []models.InlineKeyboardButton{{
		Text: "✗ Cancel", CallbackData: modelCancelCallback,
	}})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func providerName(manager gateway.ModelManager, provider string) string {
	if namer, ok := manager.(gateway.ProviderDisplayNamer); ok {
		if name := strings.TrimSpace(namer.ProviderDisplayName(provider)); name != "" {
			return name
		}
	}
	return providerDisplayName(provider)
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
	if len(manager.ModelsForProvider(selected)) == 0 {
		g.answerModelCallback(ctx, b, query.ID, "That provider has no selectable models.", true)
		return
	}
	g.answerModelCallback(ctx, b, query.ID, "", false)
	g.updateModelSelectorForProvider(ctx, b, query, manager, selected)
}

func (g *Gateway) updateModelSelectorForProvider(
	ctx context.Context,
	b *bot.Bot,
	query *models.CallbackQuery,
	manager gateway.ProviderModelManager,
	provider string,
) {
	params := &bot.EditMessageTextParams{
		Text:        modelSelectorTextForProvider(provider, 0, len(manager.ModelsForProvider(provider))),
		ReplyMarkup: g.modelSelectorKeyboardForProvider(manager, provider),
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
