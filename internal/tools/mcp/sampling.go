package mcp

import (
	"context"
	"encoding/json"
)

// JSON-RPC 2.0 error codes used when answering a server-initiated request.
// These are spec-defined and part of the MCP wire contract: the server
// distinguishes "this client cannot do that" from "the request was
// malformed" from "the handler failed" by the code, not the message.
const (
	// ErrCodeMethodNotFound is returned when this client has no idea how
	// to answer the requested method, including a sampling request when no
	// sampling handler is configured. It is never a silent drop.
	ErrCodeMethodNotFound = -32601
	// ErrCodeInvalidParams is returned when the request's params cannot be
	// decoded into the method's shape.
	ErrCodeInvalidParams = -32602
	// ErrCodeInternal is returned when the configured handler ran but
	// failed. The message carries the handler's own error text.
	ErrCodeInternal = -32603
)

// SamplingContent is one content block in a sampling message. The MCP
// sampling method sends text today; the type field is carried so an
// unsupported block is rejected explicitly rather than silently emptied.
type SamplingContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// SamplingMessage is one conversation message supplied by the MCP server.
type SamplingMessage struct {
	Role    string          `json:"role"`
	Content SamplingContent `json:"content"`
}

// SamplingRequest is the params of a sampling/createMessage request: the
// server asks this client to run a completion on its behalf.
type SamplingRequest struct {
	Messages      []SamplingMessage `json:"messages"`
	SystemPrompt  string            `json:"systemPrompt,omitempty"`
	MaxTokens     int               `json:"maxTokens,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	StopSequences []string          `json:"stopSequences,omitempty"`
}

// SamplingResult is the result of a sampling/createMessage request.
type SamplingResult struct {
	Model      string          `json:"model"`
	Role       string          `json:"role"`
	Content    SamplingContent `json:"content"`
	StopReason string          `json:"stopReason,omitempty"`
}

// SamplingHandler answers one server-initiated sampling request from a
// configured model. It returns a Go error for a refused or failed
// completion; the client maps that to a JSON-RPC internal error so the
// server sees the failure for its own request only, never a dropped
// session.
type SamplingHandler func(ctx context.Context, req SamplingRequest) (SamplingResult, error)

// ServerRequestHandler answers one request the MCP server sent to this
// client. A non-nil *ErrorData becomes a JSON-RPC error response;
// otherwise result becomes the success response. The MCP wire contract
// permits both, and a client must never drop a request it cannot answer.
type ServerRequestHandler func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *ErrorData)

// serverRequestRouter is implemented by transports that can receive
// server-initiated requests over their server→client channel. The
// stateless HTTP transport cannot (it has no persistent stream) and
// therefore does not implement it.
type serverRequestRouter interface {
	SetServerRequestHandler(handler ServerRequestHandler)
}
