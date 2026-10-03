package tools

// MultimodalResult is a tool result with media written to files; Summary is
// what the model sees.
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
	// URLs lists media to deliver to the channel, each a remote URL or a local
	// Path.
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
