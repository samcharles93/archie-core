package messaging

// MaxInboundAttachmentBytes is the largest attachment carried to the Gateway.
const MaxInboundAttachmentBytes = 20 << 20

// Media types. MediaTypeVoice is recorded speech, which is transcribed;
// MediaTypeAudio is other audio.
const (
	MediaTypeImage    = "image"
	MediaTypeVideo    = "video"
	MediaTypeAudio    = "audio"
	MediaTypeDocument = "document"
	MediaTypeVoice    = "voice"
)

// MediaAttachment describes a file attached to a message. Pointer fields
// distinguish zero from unknown.
type MediaAttachment struct {
	// Type is "image", "video", "audio", "voice", or "document".
	Type string `json:"type"`

	// FileID is the platform-assigned identifier for download APIs.
	FileID string `json:"file_id"`

	// URL is a direct download URL when available. May be empty for
	// platforms that require a download API call.
	URL string `json:"url,omitempty"`

	// Path is a local file to upload. Exactly one of URL and Path is used for an
	// outbound send.
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

	// Data holds the attachment bytes in-process on the inbound path. Never
	// persisted.
	Data []byte `json:"data,omitempty"`
}
