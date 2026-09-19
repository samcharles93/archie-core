package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

const personalityCallbackPrefix = "personality:"

func isPersonalityRequest(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	command, _, _ := strings.Cut(fields[0], "@")
	return command == "/personality"
}

func (g *Gateway) handlePersonalityCommand(
	ctx context.Context,
	b *bot.Bot,
	msg *models.Message,
	client messaging.ChatContract,
) {
	snap, err := client.Snapshot(ctx)
	if err != nil || !snap.PersonasAvailable {
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
			"Personality switching is not configured.")
		return
	}

	text := strings.TrimSpace(msg.Text)
	fields := strings.Fields(text)
	if len(fields) == 1 {
		// No args: show inline keyboard.
		g.sendPersonalitySelector(ctx, b, msg, snap.Personas)
		return
	}

	// Direct switch by name.
	name := strings.ToLower(fields[1])
	ok, err := client.SetPersona(ctx, "", name)
	if err != nil || !ok {
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
			fmt.Sprintf("Unknown personality %q. Use /personality to browse.", name))
		return
	}
	g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
		fmt.Sprintf("Personality set to %q.", name))
}

func (g *Gateway) sendPersonalitySelector(
	ctx context.Context,
	b *bot.Bot,
	msg *models.Message,
	personas []string,
) {
	if len(personas) == 0 {
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID,
			"No personalities configured.")
		return
	}

	params := &bot.SendMessageParams{
		ChatID:      msg.Chat.ID,
		Text:        "Choose a personality:",
		ReplyMarkup: g.personalityKeyboard(personas),
	}
	if msg.MessageThreadID != 0 {
		params.MessageThreadID = msg.MessageThreadID
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		g.log.Error("send personality selector failed", "error", err)
	}
}

func (g *Gateway) personalityKeyboard(personas []string) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(personas))
	for _, name := range personas {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         name,
			CallbackData: personalityCallbackPrefix + name,
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (g *Gateway) handlePersonalityCallback(
	ctx context.Context,
	b *bot.Bot,
	update *models.Update,
	client messaging.ChatContract,
) {
	query := update.CallbackQuery
	if query == nil {
		return
	}
	if !g.isSenderAllowed(query.From.ID) {
		g.log.Warn("personality callback from unauthorized sender",
			"user_id", query.From.ID)
		g.answerModelCallback(ctx, b, query.ID,
			"You are not authorised to use this bot.", true)
		return
	}
	snap, err := client.Snapshot(ctx)
	if err != nil || !snap.PersonasAvailable {
		g.answerModelCallback(ctx, b, query.ID,
			"Personality switching is not configured.", true)
		return
	}

	name := strings.TrimPrefix(query.Data, personalityCallbackPrefix)
	ok, err := client.SetPersona(ctx, "", name)
	if err != nil || !ok {
		g.answerModelCallback(ctx, b, query.ID,
			fmt.Sprintf("Unknown personality %q.", name), true)
		return
	}

	g.answerModelCallback(ctx, b, query.ID,
		fmt.Sprintf("Personality set to %q.", name), false)

	// Update the selector message to acknowledge the selection.
	params := &bot.EditMessageTextParams{
		Text: fmt.Sprintf("Personality set to %q.", name),
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
		g.log.Warn("update personality selector failed", "error", err)
	}
}
