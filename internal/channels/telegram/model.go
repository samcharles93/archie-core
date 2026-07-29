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

const modelCallbackPrefix = "model:"

func isModelSelectorRequest(text string) bool {
	fields := strings.Fields(text)
	if len(fields) != 1 {
		return false
	}
	command, _, _ := strings.Cut(fields[0], "@")
	return command == "/model"
}

// isModelCommand checks whether text is a /model command, with or without
// arguments. When the user provides a model ref or flags, the inline
// selector is skipped and the command is handled directly.
func isModelCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	command, _, _ := strings.Cut(fields[0], "@")
	return command == "/model"
}

// parseModelRest extracts model, provider, and scope flags from the text
// after the /model command token. It handles --provider=<name>,
// --provider <name>, --global, --session, and --refresh.
func parseModelRest(rest string) (model, provider string, global, session, refresh bool) {
	fields := strings.Fields(rest)
	var modelParts []string
	provider = extractProviderFlag(fields, &modelParts)
	for _, f := range fields {
		switch f {
		case "--global":
			global = true
		case "--session":
			session = true
		case "--refresh":
			refresh = true
		default:
			if !strings.HasPrefix(f, "--provider") {
				modelParts = append(modelParts, f)
			}
		}
	}
	model = strings.Join(modelParts, " ")
	return
}

// extractProviderFlag extracts --provider=<value> or --provider <value>
// from fields, returning the non-provider fields via modelParts.
func extractProviderFlag(fields []string, modelParts *[]string) string {
	for i := range fields {
		f := fields[i]
		if !strings.HasPrefix(f, "--provider") {
			*modelParts = append(*modelParts, f)
			continue
		}
		if after, ok := strings.CutPrefix(f, "--provider="); ok {
			return after
		}
		// --provider <value> (space-separated)
		if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
			return fields[i+1]
		}
	}
	return ""
}

// handleModelCommand handles /model with arguments, performing direct
// model/provider switching, scope selection, and model refresh.
func (g *Gateway) handleModelCommand(
	ctx context.Context,
	b *bot.Bot,
	msg *models.Message,
	router *gateway.Router,
) {
	if router.Models == nil {
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Model switching is not configured.")
		return
	}

	text := strings.TrimSpace(msg.Text)
	fields := strings.Fields(text)
	// fields[0] is "/model" (possibly with @bot mention); rest is everything
	// after it.
	rest := ""
	if len(fields) > 1 {
		// Reconstruct rest by joining everything after the command token.
		// We can't use strings.Join because fields splits by whitespace.
		cmdToken := fields[0]
		if _, after, ok := strings.Cut(text, cmdToken); ok {
			rest = strings.TrimSpace(after)
		}
	}

	model, provider, global, _, refresh := parseModelRest(rest)

	// --refresh: no-op for now (catalog is loaded at startup). Show current.
	if refresh {
		active := router.Models.ActiveModel()
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
			fmt.Sprintf("Active model: %s.", modelDisplayName(active, "")))
		return
	}

	// --provider without a specific model: switch provider only.
	if provider != "" && model == "" {
		manager, ok := router.Models.(gateway.ProviderModelManager)
		if !ok {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Provider switching is not configured.")
			return
		}
		if err := manager.SetActiveProvider(ctx, provider); err != nil {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
				fmt.Sprintf("Cannot switch provider: %v", err))
			return
		}
		active := router.Models.ActiveModel()
		p, _, _ := strings.Cut(active, "/")
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
			fmt.Sprintf("Provider changed to %s.", providerDisplayName(p)))
		return
	}

	// --provider with a model: set provider, then model.
	if provider != "" && model != "" {
		if manager, ok := router.Models.(gateway.ProviderModelManager); ok {
			_ = manager.SetActiveProvider(ctx, provider)
		}
	}

	// Switch model.
	if model != "" {
		g.switchModel(ctx, b, msg, router, model, global)
		return
	}

	// Neither model nor provider: delegate to inline selector.
	g.sendModelSelector(ctx, b, msg, router)
}

// switchModel activates a model and sends confirmation. When global is true
// the new selection is persisted across restarts.
func (g *Gateway) switchModel(
	ctx context.Context,
	b *bot.Bot,
	msg *models.Message,
	router *gateway.Router,
	model string,
	global bool,
) {
	if err := router.Models.SetActiveModel(ctx, model); err != nil {
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
			fmt.Sprintf("Cannot switch: %v", err))
		return
	}
	active := router.Models.ActiveModel()
	p, _, _ := strings.Cut(active, "/")
	result := fmt.Sprintf("Model changed to %s.", modelDisplayName(active, p))

	if global {
		if router.ModelPersist != nil {
			if err := router.ModelPersist(ctx, active); err != nil {
				g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
					fmt.Sprintf("Model switched to %s but persistence failed: %v",
						modelDisplayName(active, p), err))
				return
			}
			result += " (persisted globally)"
		} else {
			result += " (global persistence is not configured)"
		}
	}
	g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, result)
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
		ReplyMarkup: g.modelSelectorKeyboard(router.Models),
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
		return "Select a model:"
	}
	provider, _, ok := strings.Cut(active, "/")
	if !ok {
		return "Select a model:\nActive: " + active
	}
	return "Select a model:\nCurrent Provider: " + providerDisplayName(provider) +
		"\nActive: " + modelDisplayName(active, provider)
}

func (g *Gateway) modelSelectorKeyboard(manager gateway.ModelManager) *models.InlineKeyboardMarkup {
	active := manager.ActiveModel()
	available := selectableModels(manager)
	activeProvider := ""
	if providerManager, ok := manager.(gateway.ProviderModelManager); ok {
		activeProvider = providerManager.ActiveProvider()
	}
	rows := make([][]models.InlineKeyboardButton, 0, len(available))
	for _, model := range available {
		label := modelDisplayName(model, activeProvider)
		if model == active {
			label = "✓ " + label
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         label,
			CallbackData: g.modelCallbackToken(model),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// modelCallbackToken records a stable model identity for an inline button.
// A positional index is unsafe: provider changes can reorder the selectable
// list after Telegram has rendered the keyboard.
func (g *Gateway) modelCallbackToken(model string) string {
	sum := sha256.Sum256([]byte(model))
	// 192 bits keeps the callback well below Telegram's 64-byte limit while
	// making a collision infeasible for a configured model catalog.
	token := fmt.Sprintf("%s%x", modelCallbackPrefix, sum[:24])
	g.modelMu.Lock()
	g.modelCallbacks[token] = model
	g.modelMu.Unlock()
	return token
}

func (g *Gateway) modelForCallback(token string) (string, bool) {
	g.modelMu.RLock()
	defer g.modelMu.RUnlock()
	model, ok := g.modelCallbacks[token]
	return model, ok
}

// modelDisplayName removes routing/provider prefixes from a model reference.
// The selected provider is already shown in the selector heading; repeating it
// in every button makes provider-aware selection needlessly noisy.
func modelDisplayName(ref, provider string) string {
	name := strings.TrimPrefix(ref, provider+"/")
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	return name
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

	selected, ok := g.modelForCallback(query.Data)
	if !ok {
		g.answerModelCallback(ctx, b, query.ID, "That model selection is no longer valid.", true)
		return
	}
	if err := router.Models.SetActiveModel(ctx, selected); err != nil {
		g.answerModelCallback(ctx, b, query.ID, fmt.Sprintf("Cannot switch: %v", err), true)
		return
	}

	provider, _, _ := strings.Cut(selected, "/")
	g.answerModelCallback(ctx, b, query.ID,
		"The model has been changed to: "+modelDisplayName(selected, provider), false)
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
		ReplyMarkup: g.modelSelectorKeyboard(manager),
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
