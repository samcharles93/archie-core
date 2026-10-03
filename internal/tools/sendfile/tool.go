// Package sendfile provides send_file: it validates a host file under the
// read tool's path policy and returns it for the turn's channel to upload.
package sendfile

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/builtin"
)

// ToolName is the registry name of the file-delivery tool.
const ToolName = "send_file"

// Upload limits in bytes, matching the Telegram Bot API.
const (
	MaxUploadBytes      int64 = 50 * 1024 * 1024
	MaxImageUploadBytes int64 = 10 * 1024 * 1024
)

// uploadLimit reports the ceiling for a media kind. It mirrors the
// telegram sender's own limits by design: the two must agree, or the early
// check either lets through what the send rejects or refuses what it would
// have accepted.
func uploadLimit(mediaType string) int64 {
	if mediaType == "image" {
		return MaxImageUploadBytes
	}
	return MaxUploadBytes
}

// Tool returns send_file rooted at workspace, or nil when workspace is
// empty.
func Tool(workspace string) *tools.ToolEntry {
	if workspace == "" {
		return nil
	}

	return &tools.ToolEntry{
		Name:    ToolName,
		Toolset: "media",
		Description: "Send a file from this host to the user as an attachment in the current chat. " +
			"Use it for files you produced or found locally -- logs, transcripts, screenshots, archives -- " +
			"instead of describing a path or offering a link. The file is uploaded, so it must exist on " +
			fmt.Sprintf("this host and be no larger than %d MB.", MaxUploadBytes/(1024*1024)),
		Classification: tools.ClassIdempotent,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to send. Relative paths resolve against the workspace.",
				},
				"caption": map[string]any{
					"type":        "string",
					"description": "Optional short caption shown with the attachment.",
				},
			},
			"required": []any{"path"},
		},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			path, _ := input["path"].(string)
			caption, _ := input["caption"].(string)
			return prepare(workspace, path, caption)
		},
	}
}

// errNotRegularFile reports a target that exists but cannot be uploaded as
// a file  --  a directory, a device, a socket.
var errNotRegularFile = errors.New("not a regular file")

// prepare validates the request and returns the result describing the
// delivery. Every return path other than the final one is an error: a
// caller must never be able to read "sent" out of a failed preparation.
func prepare(workspace, path, caption string) (tools.MultimodalResult, error) {
	if strings.TrimSpace(path) == "" {
		return tools.MultimodalResult{}, fmt.Errorf("%s: path is required", ToolName)
	}

	resolved, err := builtin.ResolveReadable(workspace, path)
	if err != nil {
		return tools.MultimodalResult{}, fmt.Errorf("%s: %w", ToolName, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return tools.MultimodalResult{}, fmt.Errorf("%s: %w", ToolName, err)
	}
	if !info.Mode().IsRegular() {
		return tools.MultimodalResult{}, fmt.Errorf("%s: %s: %w", ToolName, resolved, errNotRegularFile)
	}
	mediaType := MediaType(resolved)
	if limit := uploadLimit(mediaType); info.Size() > limit {
		return tools.MultimodalResult{}, fmt.Errorf("%s: %s is %d bytes, over the %d byte upload limit for %s",
			ToolName, resolved, info.Size(), limit, mediaType)
	}
	// Opened, not just stat'd: an unreadable file is a failure the model
	// must see now rather than one the channel discovers after the turn
	// has already reported success.
	f, err := os.Open(resolved)
	if err != nil {
		return tools.MultimodalResult{}, fmt.Errorf("%s: %w", ToolName, err)
	}
	_ = f.Close()

	name := filepath.Base(resolved)
	summary := fmt.Sprintf("Sending %s (%d bytes) to the chat as an attachment.", name, info.Size())
	if caption != "" {
		summary = fmt.Sprintf("%s Caption: %s", summary, caption)
	}
	return tools.MultimodalResult{
		IsMultimodal: true,
		Summary:      summary,
		URLs: []tools.MediaRef{{
			Type:     mediaType,
			Path:     resolved,
			FileName: name,
		}},
	}, nil
}

// photoFormats are the image types photo endpoints accept.
var photoFormats = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// MediaType maps a filename to image, video, audio or document; unknown is
// document.
func MediaType(path string) string {
	mt, _, _ := strings.Cut(mime.TypeByExtension(strings.ToLower(filepath.Ext(path))), ";")
	mt = strings.TrimSpace(mt)
	switch {
	case photoFormats[mt]:
		return "image"
	case strings.HasPrefix(mt, "video/"):
		return "video"
	case strings.HasPrefix(mt, "audio/"):
		return "audio"
	default:
		return "document"
	}
}
