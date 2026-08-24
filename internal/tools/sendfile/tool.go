// Package sendfile provides the send_file tool: it delivers a file that
// exists on the daemon host to the chat the turn is running in.
//
// It exists because the agent could produce a file  --  a log dump, a
// transcript, a screenshot  --  and had no way to hand it over. The media
// path it did have was fetch-by-URL, which only ever worked for remotely
// hosted assets, so a local path was accepted and silently delivered
// nothing. Exposing a public URL for local files was rejected: it trades a
// missing feature for a hosting and access-control problem.
//
// The tool does not send anything itself. It validates the file under the
// same path policy the read tool applies and returns a
// tools.MultimodalResult naming it, which the turn's channel delivers by
// upload. That keeps the tool free of any channel binding  --  the tool
// registry is process-wide, while a chat is per-turn  --  and makes every
// failure it CAN detect (unreadable, absent, refused by confinement, too
// large) a tool error the model sees, rather than a silent non-delivery.
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

// MaxUploadBytes bounds what may be handed to a channel for upload. It is
// the Telegram Bot API's 50 MB document ceiling, which is the tightest
// limit among the channels that can deliver at all; checking it here means
// an oversized file fails as a tool error the model can report instead of
// as a delivery failure it never sees. The channel enforces its own real
// limits regardless -- this is an early, visible check, not the authority.
const MaxUploadBytes int64 = 50 * 1024 * 1024

// Tool builds the registry entry rooted at workspace. Relative paths
// resolve against it, and confinement (when enabled) is measured from it,
// exactly as for the read tool.
//
// Returns nil when workspace is empty: with no root there is no policy to
// apply, and a tool that reads arbitrary host files into an outbound
// message is not something to enable by defaulting.
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
	if info.Size() > MaxUploadBytes {
		return tools.MultimodalResult{}, fmt.Errorf("%s: %s is %d bytes, over the %d byte upload limit",
			ToolName, resolved, info.Size(), MaxUploadBytes)
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
			Type:     MediaType(resolved),
			Path:     resolved,
			FileName: name,
		}},
	}, nil
}

// MediaType maps a filename onto the four-value media vocabulary
// gateway.MediaAttachment.Type uses.
//
// Anything unrecognised is a document, which is the only kind that carries
// arbitrary bytes: guessing "image" wrongly makes the platform reject the
// send, while sending an image as a document still delivers the file.
func MediaType(path string) string {
	mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	switch {
	case strings.HasPrefix(mt, "image/"):
		return "image"
	case strings.HasPrefix(mt, "video/"):
		return "video"
	case strings.HasPrefix(mt, "audio/"):
		return "audio"
	default:
		return "document"
	}
}
