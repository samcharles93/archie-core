package telegram

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-telegram/bot"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// telegramMessageDeleter deletes messages through the bot it was built with.
type telegramMessageDeleter struct {
	bot    *bot.Bot
	chatID int64
}

var (
	_ messaging.MessageDeleter     = (*telegramMessageDeleter)(nil)
	_ messaging.CapabilityReporter = (*telegramMessageDeleter)(nil)
)

// NewMessageDeleter returns a MessageDeleter bound to one chat and to the bot
// instance of the launch that created it. Construct it per launch, from the
// same b that handled the update.
func (g *Gateway) NewMessageDeleter(b *bot.Bot, chatID int64) messaging.MessageDeleter {
	return &telegramMessageDeleter{bot: b, chatID: chatID}
}

// Capabilities reports Delete support. Telegram can remove any message the
// bot sent in a chat it administers, so this is unconditionally true.
func (d *telegramMessageDeleter) Capabilities() messaging.AdapterCapabilities {
	return messaging.AdapterCapabilities{Delete: true}
}

// DeleteMessage removes the message event names. The event's ID is the
// Telegram message ID the send returned; a non-numeric one cannot address a
// platform message, so it is reported rather than sent to the API as a
// doomed request.
func (d *telegramMessageDeleter) DeleteMessage(ctx context.Context, event messaging.MessageEvent) error {
	id, err := strconv.Atoi(event.ID)
	if err != nil {
		return fmt.Errorf("telegram delete: message id %q is not numeric: %w", event.ID, err)
	}
	if _, err := d.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    d.chatID,
		MessageID: id,
	}); err != nil {
		return fmt.Errorf("telegram delete message %d: %w", id, err)
	}
	return nil
}
