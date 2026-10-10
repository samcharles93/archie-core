// Package mcp adapts one MCP transport to the typed tool-provider family.
package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/lifecycle"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
	protocol "github.com/samcharles93/archie-core/internal/tools/mcp"
)

const cleanupTimeout = 10 * time.Second

// LifecycleTransport is the narrow MCP transport lifecycle used by Provider.
type LifecycleTransport interface {
	protocol.Transport
	Start(context.Context) error
	Stop(context.Context) error
	State() protocol.TransportState
}

// Provider owns one configured MCP server and its discovered tools.
type Provider struct {
	mu sync.RWMutex

	name      string
	segment   string
	transport LifecycleTransport
	client    *protocol.Client

	// parallelToolCalls is the configured server's opt-in to concurrent
	// tools/call. It is handed to the client Start builds: the client is
	// what serializes calls, so the flag has to reach it or the server
	// stays serialized.
	parallelToolCalls bool

	// mediaDir is the root directory binary content blocks (images, audio,
	// resource blobs) are written under, created lazily on the first
	// multimodal tool result. It is removed by Stop.
	mediaDir string
	// mediaCallSeq disambiguates concurrent calls to the same tool so
	// their written media files never share a directory.
	mediaCallSeq atomic.Int64

	// samplingHandler answers server-initiated sampling/createMessage
	// requests. Nil leaves the method refused, which is the clean failure
	// for a server that asks without a sampling handler configured.
	samplingHandler protocol.SamplingHandler
}

// Option adjusts a Provider at construction.
type Option func(*Provider)

// WithSamplingHandler answers server-initiated sampling/createMessage
// requests from the supplied handler. It is the daemon's configured model
// path, so an MCP server's sampling request is answered by the same model
// a chat turn uses rather than a second provider client.
func WithSamplingHandler(handler protocol.SamplingHandler) Option {
	return func(p *Provider) { p.samplingHandler = handler }
}

// New creates an MCP tool provider. parallelToolCalls mirrors the
// configured server's own flag: false (the default) has the provider's
// client serialize tools/call, true lets them overlap.
func New(name string, transport LifecycleTransport, parallelToolCalls bool, opts ...Option) *Provider {
	p := &Provider{
		name:              strings.TrimSpace(name),
		segment:           sanitizeToolSegment(name),
		transport:         transport,
		parallelToolCalls: parallelToolCalls,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Manifest declares the MCP provider's tool capability.
func (p *Provider) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "mcp." + sanitizeManifestSegment(p.name),
		Name:         "MCP tools: " + p.name,
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"tools"},
		Permissions:  []plugin.Permission{"process"},
	}
}

// Start starts the transport and performs the MCP handshake.
func (p *Provider) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.name == "" || p.segment == "" {
		return errors.New("MCP tool provider name is required")
	}
	if lifecycle.IsNil(p.transport) {
		return errors.New("MCP tool provider transport is nil")
	}
	if err := p.Manifest().Validate(); err != nil {
		return fmt.Errorf("MCP tool provider manifest: %w", err)
	}

	p.mu.RLock()
	started := p.client != nil && p.transport.State() == protocol.StateRunning
	p.mu.RUnlock()
	if started {
		return nil
	}

	if err := p.transport.Start(ctx); err != nil {
		return fmt.Errorf("start MCP server %q: %w", p.name, err)
	}
	client := protocol.NewClient(p.transport, p.name, p.parallelToolCalls,
		protocol.WithSamplingHandler(p.samplingHandler))
	if _, err := client.Initialize(ctx); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		stopErr := p.transport.Stop(cleanupCtx)
		cancel()
		return errors.Join(err, stopErr)
	}
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
	return nil
}

// Discover lists and adapts executable MCP tools.
func (p *Provider) Discover(ctx context.Context) ([]tools.ToolEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("MCP tool provider %q is not started", p.name)
	}
	schemas, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]tools.ToolEntry, 0, len(schemas))
	names := make(map[string]string, len(schemas))
	for _, schema := range schemas {
		segment := sanitizeToolSegment(schema.Name)
		if segment == "" {
			return nil, fmt.Errorf("MCP server %q advertised invalid empty tool name %q", p.name, schema.Name)
		}
		publicName := "mcp." + p.segment + "." + segment
		if original, exists := names[publicName]; exists {
			return nil, fmt.Errorf(
				"MCP server %q tool names %q and %q both map to %q",
				p.name,
				original,
				schema.Name,
				publicName,
			)
		}
		names[publicName] = schema.Name
		entries = append(entries, tools.ToolEntry{
			Name:        publicName,
			Toolset:     "mcp",
			Schema:      schema.InputSchema,
			Description: schema.Description,
			Handler:     p.handlerFor(client, schema.Name),
			CheckFn: func() bool {
				return p.transport.State() == protocol.StateRunning
			},
		})
	}
	return entries, nil
}

// Stop stops the MCP transport and removes any media files written during
// this provider's lifetime.
func (p *Provider) Stop(ctx context.Context) error {
	p.mu.Lock()
	p.client = nil
	mediaDir := p.mediaDir
	p.mediaDir = ""
	p.mu.Unlock()
	if mediaDir != "" {
		if err := os.RemoveAll(mediaDir); err != nil {
			return fmt.Errorf("remove MCP media dir %q: %w", mediaDir, err)
		}
	}
	if lifecycle.IsNil(p.transport) {
		return nil
	}
	if err := p.transport.Stop(ctx); err != nil {
		return fmt.Errorf("stop MCP server %q: %w", p.name, err)
	}
	return nil
}

func (p *Provider) handlerFor(client *protocol.Client, originalName string) tools.Handler {
	return func(ctx context.Context, input map[string]any) (any, error) {
		result, err := client.CallTool(ctx, originalName, input)
		if err != nil {
			return nil, err
		}
		var text strings.Builder
		var media []protocol.ContentBlock
		for _, block := range result.Content {
			switch {
			case block.Type == "resource" && block.Resource != nil && block.Resource.Blob != "":
				// A resource can carry both inline text and a binary blob
				// at once; keep the text in the summary and still treat
				// the blob as media.
				if block.Resource.Text != "" {
					text.WriteString(block.Resource.Text)
				}
				media = append(media, block)
			case block.Type == "resource" && block.Resource != nil:
				text.WriteString(block.Resource.Text)
			case block.Data != "":
				media = append(media, block)
			case block.Type == "" || block.Type == "text":
				text.WriteString(block.Text)
			default:
				fmt.Fprintf(&text, "[unhandled content block type %q]", block.Type)
			}
		}
		if result.IsError {
			return nil, fmt.Errorf("MCP tool %q reported an error: %s", originalName, text.String())
		}
		if len(media) == 0 {
			return text.String(), nil
		}
		return p.writeMultimodalResult(originalName, text.String(), media)
	}
}

// maxMediaBlockBase64Bytes caps one media block's base64 size.
const maxMediaBlockBase64Bytes = 64 << 20 // 64MiB

// writeMultimodalResult writes each media block under
// mediaDir/<tool>/<call>/ and returns a result naming the files. Bad base64
// is an error.
func (p *Provider) writeMultimodalResult(toolName, summaryText string, media []protocol.ContentBlock) (tools.MultimodalResult, error) {
	result := tools.MultimodalResult{IsMultimodal: true, Summary: summaryText}
	root, err := p.mediaRoot()
	if err != nil {
		return tools.MultimodalResult{}, err
	}

	// The tool name comes from the server; refuse path traversal.
	if err := rejectPathTraversal(toolName); err != nil {
		return tools.MultimodalResult{}, fmt.Errorf("MCP tool name %q is not safe as a path component: %w", toolName, err)
	}

	// Each call gets its own subdirectory so concurrent calls to the same
	// tool never write to the same index-based filename and clobber or
	// interleave each other's output.
	callSeq := p.mediaCallSeq.Add(1)
	subdir := filepath.Join(root, toolName, strconv.FormatInt(callSeq, 10))
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		return tools.MultimodalResult{}, fmt.Errorf("MCP tool %s: create media dir: %w", toolName, err)
	}

	files := make([]string, 0, len(media))
	refs := make([]tools.MediaRef, 0, len(media))
	for i, block := range media {
		data := block.Data
		mimeType := block.MimeType
		if block.Resource != nil {
			data = block.Resource.Blob
			mimeType = block.Resource.MimeType
		}
		if len(data) > maxMediaBlockBase64Bytes {
			return tools.MultimodalResult{}, fmt.Errorf("MCP tool %s: media block %d (%d bytes base64) exceeds the %d byte limit",
				toolName, i, len(data), maxMediaBlockBase64Bytes)
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return tools.MultimodalResult{}, fmt.Errorf("MCP tool %s: decode media block %d: %w", toolName, i, err)
		}
		ext := extensionForMimeType(mimeType)
		name := fmt.Sprintf("%d%s", i, ext)
		path := filepath.Join(subdir, name)
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return tools.MultimodalResult{}, fmt.Errorf("MCP tool %s: write media block %d: %w", toolName, i, err)
		}
		files = append(files, path)
		refs = append(refs, tools.MediaRef{
			Type:     mediaTypeForMimeType(mimeType),
			Path:     path,
			FileName: name,
		})
	}

	result.SubdirHint = subdir
	result.Files = files
	result.URLs = refs
	return result, nil
}

// mediaRoot returns the media directory, creating it on first use.
func (p *Provider) mediaRoot() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mediaDir != "" {
		return p.mediaDir, nil
	}
	dir, err := os.MkdirTemp("", "archie-mcp-media-")
	if err != nil {
		return "", fmt.Errorf("MCP provider %q: create media root: %w", p.name, err)
	}
	p.mediaDir = dir
	return dir, nil
}

// rejectPathTraversal returns an error if name contains a path separator or
// a "." component (covers both ".." and a bare "."), i.e. anything that
// would let it escape or alter the directory it's joined into. Tool names
// have no legitimate reason to contain path structure.
func rejectPathTraversal(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if strings.ContainsAny(name, `/\`) {
		return errors.New("contains a path separator")
	}
	// No separators at this point, so name is a single path component --
	// only "." and ".." are themselves traversal-meaningful.
	if name == "." || name == ".." {
		return errors.New("is a \".\" or \"..\" path component")
	}
	return nil
}

// extensionForMimeType maps common MCP media MIME types to a file
// extension. Unrecognized types get no extension -- the file is still
// written and its path returned; a missing extension doesn't stop the
// content from being usable, it's just not identifiable by filename alone.
func extensionForMimeType(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/wav", "audio/wave", "audio/x-wav":
		return ".wav"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "video/mp4":
		return ".mp4"
	case "application/pdf":
		return ".pdf"
	default:
		return ""
	}
}

// mediaTypeForMimeType maps a MIME type to image, video, audio or document.
// Only photo formats count as image.
func mediaTypeForMimeType(mimeType string) string {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return "image"
	}
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	default:
		return "document"
	}
}

func sanitizeManifestSegment(value string) string {
	return sanitizeSegment(value, '-')
}

func sanitizeToolSegment(value string) string {
	return sanitizeSegment(value, '_')
}

func sanitizeSegment(value string, separator rune) string {
	var out strings.Builder
	pendingSeparator := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if isASCIILetter(char) || isASCIIDigit(char) {
			if pendingSeparator && out.Len() > 0 {
				out.WriteRune(separator)
			}
			out.WriteRune(char)
			pendingSeparator = false
			continue
		}
		pendingSeparator = true
	}
	sanitized := out.String()
	if sanitized == "" {
		return ""
	}
	if !isASCIILetter(rune(sanitized[0])) {
		return "x" + string(separator) + sanitized
	}
	return sanitized
}

func isASCIILetter(char rune) bool {
	return char >= 'a' && char <= 'z'
}

func isASCIIDigit(char rune) bool {
	return char >= '0' && char <= '9'
}
