package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// telegramMediaSender delivers a MessageEvent's attachment through the
// media-specific Bot API methods (sendVideo, sendPhoto, ...) rather than
// sendMessage, which cannot carry a file at all.
//
// It captures the *bot.Bot at construction rather than reading the
// Gateway's shared field, matching telegramApprover: a sender belongs to
// the launch that built it, so a /restart abandons in-flight sends with
// the outgoing bot instance instead of silently redirecting them through
// the new one. That is the same lifetime rule the per-launch turns
// registry documents.
//
// Delivery is URL-only. A MediaAttachment.FileID identifies a file on the
// platform it was uploaded from and is meaningless as an outbound handle
// here, so only URL is accepted; uploading local bytes is a separate
// concern from routing a generated asset to a chat.
type telegramMediaSender struct {
	bot      *bot.Bot
	chatID   int64
	threadID int
}

var (
	_ gateway.MediaSender        = (*telegramMediaSender)(nil)
	_ gateway.CapabilityReporter = (*telegramMediaSender)(nil)
)

// NewMediaSender returns a MediaSender bound to one chat and to the bot
// instance of the launch that created it. Construct it per launch, from
// the same b that handled the update, never from a stored field.
func (g *Gateway) NewMediaSender(b *bot.Bot, chatID int64, threadID int) gateway.MediaSender {
	return &telegramMediaSender{bot: b, chatID: chatID, threadID: threadID}
}

// Capabilities reports Media support. Telegram implements every media
// kind MediaAttachment can describe, so this is unconditionally true.
func (s *telegramMediaSender) Capabilities() gateway.AdapterCapabilities {
	return gateway.AdapterCapabilities{Media: true}
}

// errNoMedia and errNoURL are the two ways an event can be undeliverable
// before any request is made.
var (
	errNoMedia = errors.New("message event carries no media attachment")
	errNoURL   = errors.New("media attachment has no URL")
)

// SendMedia delivers the event's first attachment, captioned with the
// event text. Only the first is sent: a caption belongs to one file, and
// batching several into a media group is a distinct API with its own
// caption semantics.
func (s *telegramMediaSender) SendMedia(ctx context.Context, event gateway.MessageEvent) (gateway.SendResult, error) {
	if len(event.Media) == 0 {
		return invalidMedia(errNoMedia)
	}
	att := event.Media[0]
	if att.URL == "" {
		return invalidMedia(fmt.Errorf("%w (type %q)", errNoURL, att.Type))
	}

	msg, err := s.dispatch(ctx, att, event.Text)
	if err != nil {
		if errors.Is(err, errUnsupportedMediaType) {
			return invalidMedia(err)
		}
		res := classifySendError(err)
		res.Error = fmt.Errorf("send %s: %w", att.Type, err)
		return res, res.Error
	}

	id := ""
	if msg != nil {
		id = fmt.Sprintf("%d", msg.ID)
	}
	return gateway.SendResult{Success: true, MessageID: id}, nil
}

var errUnsupportedMediaType = errors.New("unsupported media type")

// dispatch routes an attachment to the Bot API method for its kind. The
// media type, not the MessageEvent type, decides: the event type describes
// the message while the attachment describes the file, and Telegram
// rejects a video sent through sendPhoto.
func (s *telegramMediaSender) dispatch(ctx context.Context, att gateway.MediaAttachment, caption string) (*models.Message, error) {
	file := &models.InputFileString{Data: att.URL}

	switch att.Type {
	case "video":
		p := &bot.SendVideoParams{ChatID: s.chatID, Video: file, Caption: caption}
		p.MessageThreadID = s.threadID
		if att.Width != nil {
			p.Width = *att.Width
		}
		if att.Height != nil {
			p.Height = *att.Height
		}
		if att.Duration != nil {
			p.Duration = *att.Duration
		}
		return s.bot.SendVideo(ctx, p)
	case "image":
		p := &bot.SendPhotoParams{ChatID: s.chatID, Photo: file, Caption: caption}
		p.MessageThreadID = s.threadID
		return s.bot.SendPhoto(ctx, p)
	case "audio":
		p := &bot.SendAudioParams{ChatID: s.chatID, Audio: file, Caption: caption}
		p.MessageThreadID = s.threadID
		if att.Duration != nil {
			p.Duration = *att.Duration
		}
		return s.bot.SendAudio(ctx, p)
	case "document":
		p := &bot.SendDocumentParams{ChatID: s.chatID, Document: file, Caption: caption}
		p.MessageThreadID = s.threadID
		return s.bot.SendDocument(ctx, p)
	default:
		return nil, fmt.Errorf("%w %q", errUnsupportedMediaType, att.Type)
	}
}

// invalidMedia reports a failure that was detected before any request was
// made. Never retryable: resending an event that carries nothing, or names
// a type Telegram has no method for, fails identically every time.
func invalidMedia(err error) (gateway.SendResult, error) {
	return gateway.SendResult{
		Success:   false,
		Retryable: false,
		Error:     err,
		ErrorCode: "invalid_message",
	}, err
}

// classifySendError maps a Bot API failure onto SendResult's error
// vocabulary so a caller can decide on retry without inspecting
// Telegram-specific errors.
//
// The library reports API failures as sentinel errors (bot.ErrorBadRequest
// and friends), not as typed values, so these are errors.Is checks. A
// failure matching no sentinel is a transport error or a 5xx, both of
// which may yet succeed  --  hence retryable, rather than defaulting to
// permanent and silently dropping a deliverable asset.
func classifySendError(err error) gateway.SendResult {
	res := gateway.SendResult{Success: false, Error: err}

	switch {
	case bot.IsTooManyRequestsError(err) || errors.Is(err, bot.ErrorTooManyRequests):
		res.Retryable, res.ErrorCode = true, "rate_limited"
	case errors.Is(err, bot.ErrorUnauthorized), errors.Is(err, bot.ErrorForbidden):
		res.Retryable, res.ErrorCode = false, "auth"
	case errors.Is(err, bot.ErrorBadRequest), errors.Is(err, bot.ErrorNotFound):
		res.Retryable, res.ErrorCode = false, "invalid_message"
	default:
		res.Retryable, res.ErrorCode = true, "network"
	}
	return res
}
