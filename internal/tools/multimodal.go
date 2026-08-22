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
	// URLs lists media that lives at a remote, already-hosted URL rather
	// than local bytes  --  a generation API's result, for example, which
	// has nowhere to go in Files: there is no SubdirHint to write it under
	// because nothing was downloaded.
	URLs []MediaRef `json:"urls,omitempty"`
}

// MediaRef is one piece of remote-hosted media within a MultimodalResult.
type MediaRef struct {
	// Type is "image", "video", "audio", or "document"  --  the same
	// vocabulary gateway.MediaAttachment.Type uses, so a caller can build
	// one directly from this without translation.
	Type string `json:"type"`
	// URL is where the media is hosted.
	URL string `json:"url"`
}
