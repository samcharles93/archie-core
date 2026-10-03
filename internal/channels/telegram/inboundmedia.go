package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// inboundMedia pairs an inbound attachment with the textual note the
// transcript stores in its place: stored history is text-only, so the
// note is what later turns read once the attachment's bytes are gone.
type inboundMedia struct {
	attachment messaging.MediaAttachment
	// note is a bracketed, self-describing placeholder such as "[photo]"
	// or "[document: report.pdf]".
	note string
}

// extractInboundMedia maps a Telegram message onto the attachment it
// carries. A Telegram message carries at most one file (a photo is the
// set of its resolutions and collapses into one image), so the boolean is
// false exactly for messages with nothing to download.
func extractInboundMedia(msg *models.Message) (inboundMedia, bool) {
	switch {
	case len(msg.Photo) > 0:
		att := messaging.MediaAttachment{Type: "image", MIMEType: "image/jpeg"}
		for _, size := range msg.Photo {
			if largestPhoto(size, att) {
				att.FileID = size.FileID
				att.Width, att.Height = &size.Width, &size.Height
				if size.FileSize != 0 {
					att.FileSize = new(int64(size.FileSize))
				}
			}
		}
		return inboundMedia{attachment: att, note: "[photo]"}, true

	case msg.Document != nil:
		return inboundMedia{attachment: messaging.MediaAttachment{
			Type:     "document",
			FileID:   msg.Document.FileID,
			MIMEType: msg.Document.MimeType,
			FileName: msg.Document.FileName,
			FileSize: nilIfZero(msg.Document.FileSize),
		}, note: documentNote(msg.Document.FileName)}, true

	case msg.Video != nil:
		return inboundMedia{attachment: videoAttachment(msg.Video.FileID, msg.Video.MimeType, nilIfZero(msg.Video.FileSize), &msg.Video.Width, &msg.Video.Height, &msg.Video.Duration), note: "[video]"}, true

	case msg.VideoNote != nil:
		size := int64(msg.VideoNote.FileSize)
		return inboundMedia{attachment: videoAttachment(msg.VideoNote.FileID, "video/mp4", nilIfZero(size), nil, nil, &msg.VideoNote.Duration), note: "[video message]"}, true

	case msg.Audio != nil:
		return inboundMedia{attachment: messaging.MediaAttachment{
			Type:     "audio",
			FileID:   msg.Audio.FileID,
			MIMEType: msg.Audio.MimeType,
			FileName: msg.Audio.FileName,
			FileSize: nilIfZero(msg.Audio.FileSize),
			Duration: &msg.Audio.Duration,
		}, note: "[audio]"}, true

	case msg.Voice != nil:
		return inboundMedia{attachment: messaging.MediaAttachment{
			Type:     messaging.MediaTypeVoice,
			FileID:   msg.Voice.FileID,
			MIMEType: msg.Voice.MimeType,
			FileSize: nilIfZero(msg.Voice.FileSize),
			Duration: &msg.Voice.Duration,
		}, note: "[voice message]"}, true
	}
	return inboundMedia{}, false
}

// largestPhoto reports whether candidate is larger than the best size
// recorded so far in att's width/height pair.
func largestPhoto(candidate models.PhotoSize, att messaging.MediaAttachment) bool {
	if att.Width == nil {
		return true
	}
	candidateArea, bestArea := candidate.Width*candidate.Height, *att.Width**att.Height
	return candidateArea > bestArea
}

func videoAttachment(fileID, mimeType string, fileSize *int64, width, height, duration *int) messaging.MediaAttachment {
	return messaging.MediaAttachment{
		Type: "video", FileID: fileID, MIMEType: mimeType,
		FileSize: fileSize, Width: width, Height: height, Duration: duration,
	}
}

func documentNote(name string) string {
	if name == "" {
		return "[document]"
	}
	return fmt.Sprintf("[document: %s]", name)
}

func nilIfZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// maxInboundDownloadBytes is the Bot API download limit.
var maxInboundDownloadBytes int64 = messaging.MaxInboundAttachmentBytes

var errInboundMediaTooLarge = errors.New("file exceeds the bot download limit")

// downloadMedia turns a Telegram file id into the file's bytes: getFile
// resolves the transfer path, then the file is fetched from the Bot API
// server. Called on the turn lane, which is already running the rest of
// the download-and-chat work for this message.
func (g *Gateway) downloadMedia(ctx context.Context, b *bot.Bot, att messaging.MediaAttachment) ([]byte, error) {
	if att.FileID == "" {
		return nil, fmt.Errorf("media attachment carries no file id")
	}
	if att.FileSize != nil && *att.FileSize > maxInboundDownloadBytes {
		return nil, fmt.Errorf("%w: %d bytes", errInboundMediaTooLarge, *att.FileSize)
	}
	file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: att.FileID})
	if err != nil {
		return nil, fmt.Errorf("resolve telegram file: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.FileDownloadLink(file), nil)
	if err != nil {
		return nil, fmt.Errorf("download telegram file: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download telegram file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download telegram file: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInboundDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download telegram file: %w", err)
	}
	if int64(len(data)) > maxInboundDownloadBytes {
		return nil, errInboundMediaTooLarge
	}
	return data, nil
}

// turnMessageText returns the message text, or the attachment note plus
// caption for a message with a file.
func turnMessageText(msg *models.Message, media inboundMedia, hasMedia bool) string {
	if !hasMedia {
		return msg.Text
	}
	if msg.Caption == "" {
		return media.note
	}
	return media.note + "\n" + msg.Caption
}

// unsupportedMediaNotice returns the refusal for unreadable content, or ""
// for updates that need no reply.
func unsupportedMediaNotice(msg *models.Message) string {
	switch {
	case msg.Sticker != nil:
		return "I can't read stickers — send the image or file directly and I'll take a look."
	case msg.Location != nil:
		return "I can't use locations yet."
	case msg.Contact != nil:
		return "I can't use contact cards yet."
	case msg.Poll != nil:
		return "I can't use polls yet."
	}
	return ""
}

// mediaReplyLabel names an attachment the turn could not serve, in the
// sentence the sender reads.
func mediaReplyLabel(att messaging.MediaAttachment) string {
	switch {
	case att.Type == "image":
		return "I received your photo "
	case att.Type == "document" && att.FileName != "":
		return fmt.Sprintf("I received %s ", att.FileName)
	case att.Type == "document":
		return "I received your file "
	case att.Type == "video":
		return "I received your video "
	default:
		return "I received your audio attachment "
	}
}
