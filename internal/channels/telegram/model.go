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

const (
	modelCallbackPrefix     = "model:"
	modelPageCallbackPrefix = "modelpage:"
	modelBackCallback       = "modelback"
	modelCancelCallback     = "modelcancel"
	modelNoopCallback       = "modelnoop"
	modelPageSize           = 8
)

type modelPage struct {
	Provider string
	Page     int
}

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
	provider, fields := splitProviderFlag(strings.Fields(rest))
	var modelParts []string
	for _, f := range fields {
		switch f {
		case "--global":
			global = true
		case "--session":
			session = true
		case "--refresh":
			refresh = true
		default:
			modelParts = append(modelParts, f)
		}
	}
	model = strings.Join(modelParts, " ")
	return model, provider, global, session, refresh
}

// splitProviderFlag extracts --provider=<value> or --provider <value> and
// returns the provider along with the fields that remain. It is the only place
// the surviving fields are accumulated: an earlier version handed a shared
// slice to the helper and appended to it again in the caller's loop, so every
// model name was doubled ("/model gpt-4o" asked for "gpt-4o gpt-4o"). A
// space-separated provider value is removed with its flag rather than left in
// the remainder, where it would be joined into the model name.
func splitProviderFlag(fields []string) (provider string, remaining []string) {
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if !strings.HasPrefix(f, "--provider") {
			remaining = append(remaining, f)
			continue
		}
		if after, ok := strings.CutPrefix(f, "--provider="); ok {
			provider = after
			continue
		}
		// --provider <value> (space-separated)
		if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
			provider = fields[i+1]
			i++
		}
	}
	return provider, remaining
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

	// A provider-qualified direct selection is one atomic model mutation.
	if provider != "" && model != "" {
		if !strings.HasPrefix(model, provider+"/") {
			model = provider + "/" + model
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

func modelSelectorTextForProvider(provider string, page, count int) string {
	start, end := pageBounds(page, count)
	return fmt.Sprintf("⚙ Model Configuration\n\nProvider: %s (%d–%d of %d)\nSelect a model:",
		providerDisplayName(provider), start+1, end, count)
}

func (g *Gateway) modelSelectorKeyboard(manager gateway.ModelManager) *models.InlineKeyboardMarkup {
	provider := ""
	if providerManager, ok := manager.(gateway.ProviderModelManager); ok {
		provider = providerManager.ActiveProvider()
	}
	return g.modelSelectorKeyboardForProvider(manager, provider)
}

func (g *Gateway) modelSelectorKeyboardForProvider(manager gateway.ModelManager, provider string) *models.InlineKeyboardMarkup {
	return g.modelSelectorKeyboardPage(manager, provider, 0)
}

func (g *Gateway) modelSelectorKeyboardPage(manager gateway.ModelManager, provider string, page int) *models.InlineKeyboardMarkup {
	active := manager.ActiveModel()
	available := manager.Models()
	if providerManager, ok := manager.(gateway.ProviderModelManager); ok {
		available = providerManager.ModelsForProvider(provider)
	}
	start, end := pageBounds(page, len(available))
	buttons := make([]models.InlineKeyboardButton, 0, end-start)
	for _, model := range available[start:end] {
		label := modelDisplayName(model, provider)
		if model == active {
			label = "✓ " + label
		}
		buttons = append(buttons, models.InlineKeyboardButton{
			Text:         label,
			CallbackData: g.modelCallbackToken(model),
		})
	}
	rows := make([][]models.InlineKeyboardButton, 0, (len(buttons)+1)/2+2)
	for i := 0; i < len(buttons); i += 2 {
		rows = append(rows, buttons[i:min(i+2, len(buttons))])
	}
	pages := max(1, (len(available)+modelPageSize-1)/modelPageSize)
	if pages > 1 {
		var nav []models.InlineKeyboardButton
		if page > 0 {
			nav = append(nav, models.InlineKeyboardButton{
				Text: "◀ Prev", CallbackData: g.modelPageCallbackToken(provider, page-1),
			})
		}
		nav = append(nav, models.InlineKeyboardButton{
			Text: fmt.Sprintf("%d/%d", page+1, pages), CallbackData: modelNoopCallback,
		})
		if page+1 < pages {
			nav = append(nav, models.InlineKeyboardButton{
				Text: "Next ▶", CallbackData: g.modelPageCallbackToken(provider, page+1),
			})
		}
		rows = append(rows, nav)
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "◀ Back", CallbackData: modelBackCallback},
		{Text: "✗ Cancel", CallbackData: modelCancelCallback},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func pageBounds(page, count int) (int, int) {
	pages := max(1, (count+modelPageSize-1)/modelPageSize)
	page = max(0, min(page, pages-1))
	start := page * modelPageSize
	return start, min(start+modelPageSize, count)
}

func (g *Gateway) modelPageCallbackToken(provider string, page int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%d", provider, page))
	token := fmt.Sprintf("%s%x", modelPageCallbackPrefix, sum[:24])
	g.modelMu.Lock()
	g.modelPages[token] = modelPage{Provider: provider, Page: page}
	g.modelMu.Unlock()
	return token
}

func (g *Gateway) modelPageForCallback(token string) (modelPage, bool) {
	g.modelMu.RLock()
	defer g.modelMu.RUnlock()
	page, ok := g.modelPages[token]
	return page, ok
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
	g.editModelPicker(ctx, b, query, modelSelectionConfirmation(router.Models, selected), nil)
}

func modelSelectionConfirmation(manager gateway.ModelManager, ref string) string {
	provider, _, _ := strings.Cut(ref, "/")
	var b strings.Builder
	fmt.Fprintf(&b, "Model switched to %s\nProvider: %s",
		modelDisplayName(ref, provider), providerDisplayName(provider))
	detailed, ok := manager.(gateway.DetailedModelManager)
	if !ok {
		b.WriteString("\n\n_(active for this daemon process; resets on restart)_")
		return b.String()
	}
	details, ok := detailed.ModelDetails(ref)
	if !ok {
		b.WriteString("\n\n_(active for this daemon process; resets on restart)_")
		return b.String()
	}
	if details.ContextWindow > 0 {
		fmt.Fprintf(&b, "\nContext: %d tokens", details.ContextWindow)
	}
	if details.MaxOutputTokens > 0 {
		fmt.Fprintf(&b, "\nMax output: %d tokens", details.MaxOutputTokens)
	}
	var capabilities []string
	if details.Reasoning {
		capabilities = append(capabilities, "reasoning")
	}
	if details.Tools {
		capabilities = append(capabilities, "tools")
	}
	for _, modality := range details.InputModalities {
		if modality != "text" {
			capabilities = append(capabilities, modality)
		}
	}
	if details.Attachment {
		capabilities = append(capabilities, "attachments")
	}
	if details.Structured {
		capabilities = append(capabilities, "structured output")
	}
	if len(capabilities) > 0 {
		fmt.Fprintf(&b, "\nCapabilities: %s", strings.Join(capabilities, ", "))
	}
	b.WriteString("\n\n_(active for this daemon process; resets on restart)_")
	return b.String()
}

func (g *Gateway) handleModelPageCallback(
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
		g.answerModelCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}
	manager, ok := router.Models.(gateway.ProviderModelManager)
	if !ok {
		g.answerModelCallback(ctx, b, query.ID, "Model switching is not configured.", true)
		return
	}
	page, ok := g.modelPageForCallback(query.Data)
	if !ok {
		g.answerModelCallback(ctx, b, query.ID, "That model page is no longer valid.", true)
		return
	}
	models := manager.ModelsForProvider(page.Provider)
	if page.Page < 0 || page.Page*modelPageSize >= len(models) {
		g.answerModelCallback(ctx, b, query.ID, "That model page is no longer valid.", true)
		return
	}
	g.answerModelCallback(ctx, b, query.ID, "", false)
	g.editModelPicker(ctx, b, query,
		modelSelectorTextForProvider(page.Provider, page.Page, len(manager.ModelsForProvider(page.Provider))),
		g.modelSelectorKeyboardPage(manager, page.Provider, page.Page))
}

func (g *Gateway) handleModelBackCallback(
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
		g.answerModelCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}
	manager, ok := router.Models.(gateway.ProviderModelManager)
	if !ok {
		g.answerModelCallback(ctx, b, query.ID, "Model switching is not configured.", true)
		return
	}
	g.answerModelCallback(ctx, b, query.ID, "", false)
	g.editModelPicker(ctx, b, query,
		providerSelectorText(manager.ActiveModel(), manager.ActiveProvider()),
		g.providerSelectorKeyboard(manager))
}

func (g *Gateway) handleModelCancelCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	query := update.CallbackQuery
	if query == nil {
		return
	}
	if !g.isSenderAllowed(query.From.ID) {
		g.answerModelCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}
	g.answerModelCallback(ctx, b, query.ID, "", false)
	g.editModelPicker(ctx, b, query, "Model selection cancelled.", nil)
}

func (g *Gateway) handleModelNoopCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	query := update.CallbackQuery
	if query == nil {
		return
	}
	if !g.isSenderAllowed(query.From.ID) {
		g.answerModelCallback(ctx, b, query.ID, "You are not authorised to use this bot.", true)
		return
	}
	g.answerModelCallback(ctx, b, query.ID, "", false)
}

func (g *Gateway) editModelPicker(
	ctx context.Context,
	b *bot.Bot,
	query *models.CallbackQuery,
	text string,
	markup *models.InlineKeyboardMarkup,
) {
	params := &bot.EditMessageTextParams{Text: text, ReplyMarkup: markup}
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
		g.log.Warn("update model picker failed", "error", err)
	}
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
