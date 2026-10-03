package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// telegramMediaSender sends attachments through the media Bot API methods,
// using the bot it was built with. A URL is fetched by Telegram; a Path is
// uploaded. FileIDs are not accepted.
type telegramMediaSender struct {
	bot      *bot.Bot
	chatID   int64
	threadID int
}

var (
	_ messaging.MediaSender        = (*telegramMediaSender)(nil)
	_ messaging.CapabilityReporter = (*telegramMediaSender)(nil)
)

// NewMediaSender returns a MediaSender bound to one chat and to the bot
// instance of the launch that created it. Construct it per launch, from
// the same b that handled the update, never from a stored field.
func (g *Gateway) NewMediaSender(b *bot.Bot, chatID int64, threadID int) messaging.MediaSender {
	return &telegramMediaSender{bot: b, chatID: chatID, threadID: threadID}
}

// Capabilities reports Media support. Telegram implements every media
// kind MediaAttachment can describe, so this is unconditionally true.
func (s *telegramMediaSender) Capabilities() messaging.AdapterCapabilities {
	return messaging.AdapterCapabilities{Media: true}
}

// errNoMedia and errNoSource are the two ways an event can be
// undeliverable before any request is made.
var (
	errNoMedia = errors.New("message event carries no media attachment")
	// errNoSource replaces the former errNoURL: an attachment is now
	// deliverable with either a URL or a local Path, so having neither  --
	// not having no URL  --  is what makes it undeliverable.
	errNoSource = errors.New("media attachment has neither a URL nor a local path")
)

// Bot API upload ceilings, in bytes. Photos are capped far lower than
// everything else, and exceeding either is a 400 from Telegram with a
// message the operator never sees; checking here turns that into a
// reported failure with the actual size in it.
const (
	maxPhotoUploadBytes int64 = 10 * 1024 * 1024
	maxFileUploadBytes  int64 = 50 * 1024 * 1024
)

// SendMedia delivers the event's first attachment, captioned with the
// event text. Only the first is sent: a caption belongs to one file, and
// batching several into a media group is a distinct API with its own
// caption semantics.
func (s *telegramMediaSender) SendMedia(ctx context.Context, event messaging.MessageEvent) (messaging.SendResult, error) {
	if len(event.Media) == 0 {
		return invalidMedia(errNoMedia)
	}
	att := event.Media[0]
	if att.URL == "" && att.Path == "" {
		return invalidMedia(fmt.Errorf("%w (type %q)", errNoSource, att.Type))
	}

	file, closeFile, err := s.source(att)
	if err != nil {
		return invalidMedia(err)
	}
	defer closeFile()

	msg, err := s.dispatch(ctx, att, file, event.Text)
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
	return messaging.SendResult{Success: true, MessageID: id}, nil
}

var (
	errUnsupportedMediaType = errors.New("unsupported media type")
	errNotRegularFile       = errors.New("not a regular file")
	errTooLarge             = errors.New("file exceeds the Telegram upload limit")
)

// source returns the Bot API file for att and a release function. It refuses
// a local file over the size limit.
func (s *telegramMediaSender) source(att messaging.MediaAttachment) (models.InputFile, func(), error) {
	noop := func() {}
	if att.Path == "" {
		return &models.InputFileString{Data: att.URL}, noop, nil
	}

	info, err := os.Stat(att.Path)
	if err != nil {
		return nil, noop, fmt.Errorf("attach %s: %w", att.Path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, noop, fmt.Errorf("attach %s: %w", att.Path, errNotRegularFile)
	}
	if limit := uploadLimit(att.Type); info.Size() > limit {
		return nil, noop, fmt.Errorf("attach %s: %w: %d bytes exceeds %d",
			att.Path, errTooLarge, info.Size(), limit)
	}

	f, err := os.Open(att.Path)
	if err != nil {
		return nil, noop, fmt.Errorf("attach %s: %w", att.Path, err)
	}

	name := att.FileName
	if name == "" {
		name = filepath.Base(att.Path)
	}
	return &models.InputFileUpload{Filename: name, Data: f}, func() { _ = f.Close() }, nil
}

// uploadLimit reports the Bot API ceiling for a media kind. Photos have
// their own, much lower one; everything else shares the general file
// limit.
func uploadLimit(mediaType string) int64 {
	if mediaType == "image" {
		return maxPhotoUploadBytes
	}
	return maxFileUploadBytes
}

// dispatch routes an attachment to the Bot API method for its kind. The
// media type, not the MessageEvent type, decides: the event type describes
// the message while the attachment describes the file, and Telegram
// rejects a video sent through sendPhoto.
func (s *telegramMediaSender) dispatch(ctx context.Context, att messaging.MediaAttachment, file models.InputFile, caption string) (*models.Message, error) {
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
func invalidMedia(err error) (messaging.SendResult, error) {
	return messaging.SendResult{
		Success:   false,
		Retryable: false,
		Error:     err,
		ErrorCode: "invalid_message",
	}, err
}

// classifySendError maps a Bot API error to a SendResult. Unrecognised errors
// are retryable.
func classifySendError(err error) messaging.SendResult {
	res := messaging.SendResult{Success: false, Error: err}

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
