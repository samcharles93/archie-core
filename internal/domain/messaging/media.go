package messaging

// MaxInboundAttachmentBytes is the transport ceiling for an attachment a
// channel frontend carries across the inbound wire to the process that runs
// the turn. It is a ceiling, not a policy: a frontend may refuse earlier
// (Telegram's Bot API caps getFile at 20 MB), and anything larger must
// degrade to a notice instead of failing the request that carries it.
// The Gateway's gRPC receive limit is sized from this value, so a maximal
// attachment is accepted rather than rejected as an oversized message.
const MaxInboundAttachmentBytes = 20 << 20

// Type values. MediaTypeVoice is speech the sender recorded -- a Telegram
// voice note -- as distinct from MediaTypeAudio, a music or generic audio
// file. The distinction is what tells the model-owning turn runner to
// transcribe a speech attachment before the turn: a voice note's bytes reach
// the Gateway, which owns the provider credential, but the Gateway must not
// spend a transcription call on a forwarded song. The remaining values are
// what they say.
const (
	MediaTypeImage    = "image"
	MediaTypeVideo    = "video"
	MediaTypeAudio    = "audio"
	MediaTypeDocument = "document"
	MediaTypeVoice    = "voice"
)

// MediaAttachment describes a file attached to a message. Platform-agnostic;
// platform-specific fields live in Raw.
//
// Numeric fields use pointers so that zero is distinguishable from
// "unknown" across a JSON round-trip: a 0-byte file is valid and
// different from a file whose size was never set.
type MediaAttachment struct {
	// Type is "image", "video", "audio", "voice", or "document".
	Type string `json:"type"`

	// FileID is the platform-assigned identifier for download APIs.
	FileID string `json:"file_id"`

	// URL is a direct download URL when available. May be empty for
	// platforms that require a download API call.
	URL string `json:"url,omitempty"`

	// Path is a local filesystem path on the host that produced the
	// attachment. It is the alternative to URL for outbound delivery: a
	// URL is fetched by the platform, a Path is uploaded by us. A file the
	// agent wrote has no URL and never gets one -- publishing it would
	// trade a delivery problem for a hosting and access-control one -- so
	// senders that support upload must read this.
	//
	// Exactly one of URL and Path is meaningful for an outbound send.
	Path string `json:"path,omitempty"`

	// MIMEType is the content type (e.g. "image/png", "audio/ogg").
	MIMEType string `json:"mime_type,omitempty"`

	// FileName is the original file name when known.
	FileName string `json:"file_name,omitempty"`

	// FileSize is the attachment size in bytes. nil means unknown.
	FileSize *int64 `json:"file_size,omitempty"`

	// Width is the pixel width for images and video. nil means unknown
	// or not applicable.
	Width *int `json:"width,omitempty"`

	// Height is the pixel height for images and video. nil means unknown
	// or not applicable.
	Height *int `json:"height,omitempty"`

	// Duration is the play time in seconds for audio and video.
	Duration *int `json:"duration,omitempty"`

	// Raw carries platform-specific fields the common vocabulary does not
	// cover, so adapters never need a second attachment type.
	Raw map[string]any `json:"raw,omitempty"`

	// Data carries the attachment bytes in-process. An inbound path sets
	// it after downloading the file; outbound senders never set it and
	// read Path or URL instead. It crosses the wire in one direction only:
	// the inbound request from a channel frontend to the Gateway process
	// that runs the turn, because that process never holds the platform
	// credential. It is never persisted, and the stored-history and
	// outbound-event directions strip it.
	Data []byte `json:"data,omitempty"`
}
