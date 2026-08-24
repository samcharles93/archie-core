package tools

// MultimodalResult wraps a tool result that includes non-text content
// (images, audio, video, documents). Summary is the text fed back into
// the model's context; the actual media is written to files under
// SubdirHint rather than inlined as base64, since inlining media into
// the context window would bloat token usage for content the model
// cannot directly consume anyway.
type MultimodalResult struct {
	// IsMultimodal is always true for this type; present so a JSON
	// consumer can distinguish a MultimodalResult from a plain string
	// result without type-sniffing.
	IsMultimodal bool `json:"is_multimodal"`
	// Summary is the text description shown to the model  --  any text
	// content blocks from the tool result, or a default note when the
	// result is media-only.
	Summary string `json:"summary"`
	// SubdirHint is the directory the media files were written under,
	// relative to the caller's configured media root. Empty when no
	// media root was configured, so the files could not be persisted.
	SubdirHint string `json:"subdir_hint,omitempty"`
	// Files lists the full paths written under SubdirHint, in the order
	// the source content blocks appeared.
	Files []string `json:"files,omitempty"`
	// URLs lists media to deliver to the user's channel. Each ref carries
	// either a remote URL  --  a generation API's result, for example,
	// which has nowhere to go in Files because nothing was downloaded  --
	// or a local Path the channel uploads.
	//
	// The field used to mean remote-hosted media only; it now means "media
	// this result wants delivered", because a locally produced file has no
	// URL and exposing one for it was rejected. The JSON name is unchanged
	// so existing producers keep working.
	URLs []MediaRef `json:"urls,omitempty"`
}

// MediaRef is one piece of remote-hosted media within a MultimodalResult.
type MediaRef struct {
	// Type is "image", "video", "audio", or "document"  --  the same
	// vocabulary gateway.MediaAttachment.Type uses, so a caller can build
	// one directly from this without translation.
	Type string `json:"type"`
	// URL is where the media is hosted, for media the channel should
	// fetch. Empty when the ref names a local file instead.
	URL string `json:"url,omitempty"`
	// Path is an absolute local filesystem path, for media the channel
	// should upload. Empty when the ref names a hosted URL instead.
	Path string `json:"path,omitempty"`
	// FileName is the name to present the upload under. Ignored for a
	// URL ref, which carries its own name.
	FileName string `json:"file_name,omitempty"`
}
