package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/samcharles93/archie-core/internal/tools"
)

// protocolVersion is the MCP protocol version this client speaks.
const protocolVersion = "2024-11-05"

// Transport is the narrow interface Client depends on  --  satisfied by
// [*StdioTransport]. Tests substitute a fake to avoid spawning a real
// subprocess. Notify is separate from Send because a notification (no
// "id") never gets a JSON-RPC response  --  a transport whose Send always
// waits for a correlated reply would hang forever if asked to send one.
type Transport interface {
	Send(ctx context.Context, body []byte) ([]byte, error)
	Notify(ctx context.Context, body []byte) error
}

// Client speaks the MCP JSON-RPC protocol (initialize, tools/list,
// tools/call) over a [Transport]. It does not manage the transport's
// lifecycle (Start/Stop)  --  callers start the transport first, then
// build a Client on top of it.
type Client struct {
	transport  Transport
	serverName string
	nextID     atomic.Int64
	// mediaDir is the root directory binary content blocks (images,
	// audio, resource blobs) are written under, in a per-tool
	// subdirectory. Empty means media is reported but not persisted  --
	// see [WithMediaDir].
	mediaDir string
	// mediaCallSeq disambiguates concurrent calls to the same tool so
	// their written media files never share a directory.
	mediaCallSeq atomic.Int64
	// callMu serializes tools/call requests to this server unless
	// parallelToolCalls is set. nil (the default) means serialized  --
	// most MCP servers are single-threaded processes and don't expect or
	// handle concurrent requests safely.
	callMu            *sync.Mutex
	parallelToolCalls bool
}

// ClientOption configures optional Client behavior.
type ClientOption func(*Client)

// WithMediaDir sets the root directory for writing binary content
// (images, audio, resource blobs) a tool call returns, instead of
// inlining base64 data into the model's context. Each tool's media
// files are written under dir/<toolName>/.
func WithMediaDir(dir string) ClientOption {
	return func(c *Client) { c.mediaDir = dir }
}

// WithParallelToolCalls opts this server into concurrent tool calls.
// By default (false), CallTool serializes all calls to a given Client
// one at a time  --  the safe default, since most MCP servers are
// single-threaded subprocesses that don't expect concurrent requests.
// Set true only for servers documented or known to handle concurrent
// requests safely. Serialization is per-Client (per-server); it never
// blocks calls to a different server.
func WithParallelToolCalls(parallel bool) ClientOption {
	return func(c *Client) { c.parallelToolCalls = parallel }
}

// NewClient builds a Client over transport. serverName identifies this
// MCP server in registered tool names (e.g. "github" produces
// "mcp.github.search_repos") and in log/error messages; it need not
// match the server's own self-reported name.
func NewClient(transport Transport, serverName string, opts ...ClientOption) *Client {
	c := &Client{transport: transport, serverName: serverName, callMu: &sync.Mutex{}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// InitializeResult is the server's response to initialize.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
	Capabilities    Capabilities `json:"capabilities"`
}

// ServerInfo identifies the MCP server implementation.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capabilities describes what the server supports. Fields are omitted
// (left as raw presence checks) rather than fully modeled  --  archie-core
// only needs tool discovery today.
type Capabilities struct {
	Tools json.RawMessage `json:"tools,omitempty"`
}

// ToolSchema is one tool as advertised by tools/list.
type ToolSchema struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	InputSchema tools.JSONSchema `json:"inputSchema"`
}

// ContentBlock is one block of a tools/call result. Per the MCP spec a
// block is one of: text (Text set), image/audio (Data+MimeType set), or
// resource (Resource set, itself either inline text or a base64 blob).
type ContentBlock struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	Data     string           `json:"data,omitempty"` // base64, for type "image"/"audio"
	MimeType string           `json:"mimeType,omitempty"`
	Resource *ResourceContent `json:"resource,omitempty"`
}

// ResourceContent is an embedded resource, for a ContentBlock of type
// "resource". Exactly one of Text or Blob is set: Text for inline
// textual resources (treated as plain text, not media), Blob
// (base64-encoded) for binary resources (treated as media).
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// CallToolResult is the result of tools/call. IsError reports a
// tool-level failure (the call reached the tool, which then failed) as
// distinct from a transport or JSON-RPC protocol error  --  the MCP spec
// requires tool failures to be reported this way so the calling LLM sees
// the failure content instead of a bare error.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// Initialize performs the MCP handshake: sends initialize, then the
// notifications/initialized notification the spec requires after a
// successful initialize response. Must be called once before ListTools
// or CallTool.
func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "archie-core", "version": "1.0.0"},
	}
	var result InitializeResult
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return InitializeResult{}, fmt.Errorf("mcp: initialize %s: %w", c.serverName, err)
	}
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return InitializeResult{}, fmt.Errorf("mcp: notifications/initialized %s: %w", c.serverName, err)
	}
	return result, nil
}

// ListTools returns every tool the server advertises, following the
// cursor-based pagination the spec defines until the server stops
// returning nextCursor.
func (c *Client) ListTools(ctx context.Context) ([]ToolSchema, error) {
	var all []ToolSchema
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools      []ToolSchema `json:"tools"`
			NextCursor string       `json:"nextCursor,omitempty"`
		}
		if err := c.call(ctx, "tools/list", params, &page); err != nil {
			return nil, fmt.Errorf("mcp: tools/list %s: %w", c.serverName, err)
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// CallTool invokes a tool by name with the given arguments. A non-nil
// error means the call could not be completed at all (transport failure,
// malformed response, unknown method). A tool that ran and failed is
// reported via CallToolResult.IsError, not a Go error  --  see
// [CallToolResult].
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (CallToolResult, error) {
	if !c.parallelToolCalls {
		c.callMu.Lock()
		defer c.callMu.Unlock()
	}

	params := map[string]any{"name": name, "arguments": arguments}
	var result CallToolResult
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return CallToolResult{}, fmt.Errorf("mcp: tools/call %s.%s: %w", c.serverName, name, err)
	}
	return result, nil
}

// RegisterTools calls ListTools and registers each discovered tool into
// reg as "mcp.<serverName>.<toolName>", with a Handler that calls
// CallTool and surfaces CallToolResult.IsError as a Go error (a registry
// Handler has no other channel to report tool-level failure through).
// Returns the number of tools registered; a tool whose name collides
// with an existing registry entry is skipped and logged by the caller
// via the returned count being less than len(tools).
func (c *Client) RegisterTools(ctx context.Context, reg *tools.Registry) (int, error) {
	toolList, err := c.ListTools(ctx)
	if err != nil {
		return 0, err
	}
	registered := 0
	for _, ts := range toolList {
		entry := tools.ToolEntry{
			Name:        fmt.Sprintf("mcp.%s.%s", c.serverName, ts.Name),
			Toolset:     "mcp",
			Schema:      ts.InputSchema,
			Description: ts.Description,
			Handler:     c.handlerFor(ts.Name),
		}
		if err := reg.Register(entry); err != nil {
			continue
		}
		registered++
	}
	return registered, nil
}

// handlerFor returns a tools.Handler that calls toolName via CallTool.
// A result whose content is text-only returns a plain string (backward
// compatible with callers that expect simple tool output). A result
// carrying image/audio/resource-blob content returns a
// [tools.MultimodalResult] instead of inlining base64 data into the
// model's context  --  see [WithMediaDir].
func (c *Client) handlerFor(toolName string) tools.Handler {
	return func(ctx context.Context, input map[string]any) (any, error) {
		result, err := c.CallTool(ctx, toolName, input)
		if err != nil {
			return nil, err
		}

		var sb strings.Builder
		var media []ContentBlock
		for _, block := range result.Content {
			switch {
			case block.Type == "resource" && block.Resource != nil && block.Resource.Blob != "":
				// A resource can carry both inline text and a binary blob
				// at once (the spec doesn't forbid it); keep the text in
				// the summary and still treat the blob as media.
				if block.Resource.Text != "" {
					sb.WriteString(block.Resource.Text)
				}
				media = append(media, block)
			case block.Type == "resource" && block.Resource != nil:
				sb.WriteString(block.Resource.Text)
			case block.Data != "":
				media = append(media, block)
			case block.Type == "" || block.Type == "text":
				sb.WriteString(block.Text)
			default:
				// An unrecognized content type (a future MCP block kind
				// this client doesn't model yet). Note it rather than
				// silently dropping it so a caller reading Summary can
				// tell content was omitted.
				fmt.Fprintf(&sb, "[unhandled content block type %q]", block.Type)
			}
		}
		text := sb.String()

		if result.IsError {
			return nil, fmt.Errorf("mcp: tool %s reported an error: %s", toolName, text)
		}
		if len(media) == 0 {
			return text, nil
		}
		return c.writeMultimodalResult(toolName, text, media)
	}
}

// maxMediaBlockBase64Bytes caps the base64-encoded size of a single
// media content block before it is decoded and written to disk. An MCP
// server is a semi-trusted external process; without a cap, one
// misbehaving or malicious response could force the daemon to allocate
// and persist an unbounded amount of data. ~48MB decoded (base64 is
// ~4/3 the size of the decoded payload).
const maxMediaBlockBase64Bytes = 64 << 20 // 64MiB

// writeMultimodalResult decodes and, when mediaDir is configured, writes
// each media block to disk under mediaDir/<sanitized toolName>/<call
// sequence>/, returning the resulting envelope. Malformed base64 data is
// a hard error  --  the server sent a content block it claimed was media
// but wasn't decodable.
func (c *Client) writeMultimodalResult(toolName, summaryText string, media []ContentBlock) (tools.MultimodalResult, error) {
	result := tools.MultimodalResult{IsMultimodal: true, Summary: summaryText}
	if c.mediaDir == "" {
		return result, nil
	}

	// toolName is server-supplied (it comes from the MCP server's own
	// tools/list response, not from caller-controlled input) and is used
	// to build a filesystem path below. Never trust it: a malicious
	// server could otherwise advertise a tool name like "../../etc/cron.d/x"
	// to escape mediaDir. Reject any path separator or "." component
	// outright rather than trying to sanitize it into something safe.
	if err := rejectPathTraversal(toolName); err != nil {
		return tools.MultimodalResult{}, fmt.Errorf("mcp: tool name %q is not safe as a path component: %w", toolName, err)
	}

	// Each call gets its own subdirectory so concurrent calls to the same
	// tool (CallTool is safe for concurrent use  --  see the atomic id
	// generator in call()) never write to the same index-based filename
	// and clobber or interleave each other's output.
	callSeq := c.mediaCallSeq.Add(1)
	subdir := filepath.Join(c.mediaDir, toolName, strconv.FormatInt(callSeq, 10))
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		return tools.MultimodalResult{}, fmt.Errorf("mcp: tool %s: create media dir: %w", toolName, err)
	}

	files := make([]string, 0, len(media))
	for i, block := range media {
		data := block.Data
		mimeType := block.MimeType
		if block.Resource != nil {
			data = block.Resource.Blob
			mimeType = block.Resource.MimeType
		}
		if len(data) > maxMediaBlockBase64Bytes {
			return tools.MultimodalResult{}, fmt.Errorf("mcp: tool %s: media block %d (%d bytes base64) exceeds the %d byte limit",
				toolName, i, len(data), maxMediaBlockBase64Bytes)
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return tools.MultimodalResult{}, fmt.Errorf("mcp: tool %s: decode media block %d: %w", toolName, i, err)
		}
		path := filepath.Join(subdir, fmt.Sprintf("%d%s", i, extensionForMimeType(mimeType)))
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return tools.MultimodalResult{}, fmt.Errorf("mcp: tool %s: write media block %d: %w", toolName, i, err)
		}
		files = append(files, path)
	}

	result.SubdirHint = subdir
	result.Files = files
	return result, nil
}

// rejectPathTraversal returns an error if name contains a path separator
// or a "." component (covers both ".." and a bare "."), i.e. anything
// that would let it escape or alter the directory it's joined into.
// Tool names have no legitimate reason to contain path structure.
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
// extension. Unrecognized types get no extension  --  the file is still
// written and its path returned; a missing extension doesn't stop the
// content from being usable, it's just not identifiable by filename
// alone.
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

// call sends a JSON-RPC request and decodes its result into out. It
// returns an error for a transport failure, a JSON-RPC error response,
// or a malformed result payload.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	respBody, err := c.transport.Send(ctx, body)
	if err != nil {
		return err
	}
	var resp Message
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("unmarshal result: %w", err)
	}
	return nil
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *Client) notify(ctx context.Context, method string, params any) error {
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	return c.transport.Notify(ctx, body)
}
