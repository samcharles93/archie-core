package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
// An attachment is delivered one of two ways, chosen by what it carries:
// a URL is handed to Telegram to FETCH, while a Path is UPLOADED from this
// host. Delivery used to be URL-only, which meant a locally produced file
// was passed off as a URL Telegram could not fetch and the send did
// nothing useful while reporting success.
//
// A MediaAttachment.FileID is still not accepted: it identifies a file on
// the platform it was uploaded from and is meaningless as an outbound
// handle here.
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
func (s *telegramMediaSender) SendMedia(ctx context.Context, event gateway.MessageEvent) (gateway.SendResult, error) {
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
	return gateway.SendResult{Success: true, MessageID: id}, nil
}

var (
	errUnsupportedMediaType = errors.New("unsupported media type")
	errNotRegularFile       = errors.New("not a regular file")
	errTooLarge             = errors.New("file exceeds the Telegram upload limit")
)

// source builds the Bot API file value for att, and a function to release
// whatever it holds open.
//
// A URL becomes an InputFileString, which tells Telegram to fetch it. A
// local path becomes an InputFileUpload carrying an open *os.File, which
// the library streams as multipart form data  --  the only way bytes that
// exist solely on this host can reach a chat.
//
// The size check happens here rather than being left to Telegram: over the
// limit the API answers a bad request whose text nobody reads, and the
// operator sees an attachment that never arrived. The returned error is
// classified invalid_message by the caller, so it is not retried  --  a
// file does not get smaller on a second attempt.
func (s *telegramMediaSender) source(att gateway.MediaAttachment) (models.InputFile, func(), error) {
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
func (s *telegramMediaSender) dispatch(ctx context.Context, att gateway.MediaAttachment, file models.InputFile, caption string) (*models.Message, error) {
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
