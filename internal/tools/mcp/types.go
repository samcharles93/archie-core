// Package mcp implements the Model Context Protocol (MCP) transport layer.
//
// The stdio transport manages an MCP server subprocess over stdin/stdout
// using JSON-RPC 2.0 message framing with Content-Length headers (the same
// framing used by LSP). This is the foundational transport that most local
// MCP servers use.
package mcp

import "encoding/json"

// TransportState represents the lifecycle state of a transport.
type TransportState int

const (
	// StateStopped is the initial and final state — no subprocess running.
	StateStopped TransportState = iota
	// StateStarting means a subprocess has been spawned and is being
	// initialised. The transport is not yet usable for requests.
	StateStarting
	// StateRunning means the subprocess is healthy and ready to handle requests.
	StateRunning
	// StateStopping means the subprocess is being shut down gracefully.
	StateStopping
	// StateError means the subprocess has failed and retries have been
	// exhausted (or the error was unrecoverable). A manual Stop or Start
	// is required to leave this state.
	StateError
)

func (s TransportState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// Message is a JSON-RPC 2.0 message. Exactly one of Method, Result, or
// Error should be set depending on whether this is a request, response,
// or notification.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ErrorData      `json:"error,omitempty"`
}

// ErrorData represents a JSON-RPC 2.0 error object.
type ErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// IsRequest returns true when this is a request message (has an ID and method).
func (m Message) IsRequest() bool {
	return len(m.ID) > 0 && m.Method != ""
}

// IsResponse returns true when this is a response message (has an ID but no method).
func (m Message) IsResponse() bool {
	return len(m.ID) > 0 && m.Method == ""
}

// IsNotification returns true when this is a notification (no ID, has method).
func (m Message) IsNotification() bool {
	return len(m.ID) == 0 && m.Method != ""
}
