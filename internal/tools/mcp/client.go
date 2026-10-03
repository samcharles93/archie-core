package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/samcharles93/archie-core/internal/tools"
)

// protocolVersion is the MCP protocol version this client speaks.
const protocolVersion = "2024-11-05"

// Transport sends requests and notifications to an MCP server.
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
	// callMu serializes tools/call to this server. Nil allows concurrent calls.
	callMu *sync.Mutex
	// samplingHandler answers server-initiated sampling/createMessage
	// requests. Set once at construction, so reading it from a transport
	// reader goroutine needs no lock. Nil means the method is refused.
	samplingHandler SamplingHandler
}

// ClientOption adjusts a Client at construction.
type ClientOption func(*Client)

// WithSamplingHandler answers server-initiated sampling/createMessage
// requests from the supplied handler. Without it the client answers the
// method with a JSON-RPC method-not-found error, per the MCP spec -- never
// a silent drop.
func WithSamplingHandler(handler SamplingHandler) ClientOption {
	return func(c *Client) { c.samplingHandler = handler }
}

// NewClient builds a Client over transport. parallelToolCalls allows
// concurrent tools/call. The client handles server-initiated requests when
// the transport supports them.
func NewClient(transport Transport, serverName string, parallelToolCalls bool, opts ...ClientOption) *Client {
	c := &Client{transport: transport, serverName: serverName}
	if !parallelToolCalls {
		c.callMu = &sync.Mutex{}
	}
	for _, opt := range opts {
		opt(c)
	}
	if router, ok := transport.(serverRequestRouter); ok {
		router.SetServerRequestHandler(c.handleServerRequest)
	}
	return c
}

// handleServerRequest answers sampling/createMessage and returns
// method-not-found for anything else.
func (c *Client) handleServerRequest(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *ErrorData) {
	if method != "sampling/createMessage" {
		return nil, &ErrorData{Code: ErrCodeMethodNotFound, Message: "method not found: " + method}
	}
	if c.samplingHandler == nil {
		return nil, &ErrorData{
			Code:    ErrCodeMethodNotFound,
			Message: "sampling/createMessage is not supported: no sampling handler is configured",
		}
	}
	var req SamplingRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &ErrorData{Code: ErrCodeInvalidParams, Message: "invalid sampling/createMessage params: " + err.Error()}
	}
	result, err := c.samplingHandler(ctx, req)
	if err != nil {
		return nil, &ErrorData{Code: ErrCodeInternal, Message: err.Error()}
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, &ErrorData{Code: ErrCodeInternal, Message: "marshal sampling result: " + err.Error()}
	}
	return body, nil
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

// CallToolResult is a tools/call result. IsError reports a tool-level
// failure.
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

// CallTool calls a tool. A Go error means the call failed; a tool failure is
// CallToolResult.IsError.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (CallToolResult, error) {
	if c.callMu != nil {
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
