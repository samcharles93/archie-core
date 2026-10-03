package telegram

import (
	"context"
	"fmt"
	"time"

	"github.com/go-telegram/bot"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// statusNoticeTTL is how long a transient status notice stays in the chat
// before it is retracted: long enough to be read, short enough not to become
// history next to the conversation it reported on.
const statusNoticeTTL = 8 * time.Second

// sendEphemeral sends text and deletes it after statusNoticeTTL. Failures are
// logged.
func (g *Gateway) sendEphemeral(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int, text string) {
	sender := g.newEphemeralSender()
	_, err := sender.Send(ctx, g.NewMessageDeleter(b, chatID), messaging.EphemeralReply{
		Event: messaging.MessageEvent{
			Type:      messaging.MsgText,
			Text:      text,
			ChannelID: fmt.Sprintf("%d", chatID),
			Platform:  "telegram",
		},
		TTL: statusNoticeTTL,
	}, func(ctx context.Context, event messaging.MessageEvent) (messaging.MessageEvent, error) {
		id := g.sendTextMessage(ctx, b, chatID, messageThreadID, event.Text)
		if id == 0 {
			// Nothing was delivered, so there is nothing to name or retract.
			return messaging.MessageEvent{}, nil
		}
		event.ID = fmt.Sprintf("%d", id)
		return event, nil
	})
	if err != nil {
		g.log.Error("ephemeral notice send failed", "error", err)
	}
}

// sendTextMessage sends one message and returns its ID, or 0 when the text
// had to be split.
func (g *Gateway) sendTextMessage(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int, text string) int {
	blocks := markdownToBlocks(text)
	if blockTextLen(blocks) > messageMaxLen {
		g.sendMessage(ctx, b, chatID, messageThreadID, text)
		return 0
	}
	return g.sendBlocks(ctx, b, chatID, messageThreadID, blocks, "send ephemeral message failed")
}
