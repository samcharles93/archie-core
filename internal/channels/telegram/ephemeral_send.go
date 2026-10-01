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

// sendEphemeral delivers text and retracts it after statusNoticeTTL.
//
// The retraction is best-effort by contract: a platform that cannot delete
// leaves the notice in place (channels.EphemeralSender logs the degrade), and
// a failed send is logged rather than returned, because a status notice must
// never take the operation it narrates down with it.
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

// sendTextMessage delivers one message and returns its Bot API message ID.
//
// It returns 0 for text that must be split across messages: a retraction
// addresses exactly one message, so a reply that does not fit in one is left
// intact rather than half-deleted.
func (g *Gateway) sendTextMessage(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int, text string) int {
	blocks := markdownToBlocks(text)
	if blockTextLen(blocks) > messageMaxLen {
		g.sendMessage(ctx, b, chatID, messageThreadID, text)
		return 0
	}
	return g.sendBlocks(ctx, b, chatID, messageThreadID, blocks, "send ephemeral message failed")
}
