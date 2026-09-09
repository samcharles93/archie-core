package messaging

// MediaAttachment describes a file attached to a message. Platform-agnostic;
// platform-specific fields live in Raw.
//
// Numeric fields use pointers so that zero is distinguishable from
// "unknown" across a JSON round-trip: a 0-byte file is valid and
// different from a file whose size was never set.
type MediaAttachment struct {
	// Type is "image", "video", "audio", or "document".
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
}
