// Package memory defines the pluggable memory provider architecture for
// archie-core. Providers implement the core MemoryProvider interface; optional
// behavior is exposed through sub-interfaces that the MemoryManager discovers
// at runtime via type assertion.
package memory

import "github.com/samcharles93/archie-core/internal/tools"

// MemoryProvider is the contract that every memory provider — built-in or
// external/plugin — must implement. It covers provider identity, availability,
// session-scoped initialization, and tool schema export.
type MemoryProvider interface {
	// Name returns a unique, human-readable identifier for this provider
	// (e.g. "sqlite", "filesystem", "chroma"). The name is used in
	// configuration, logging, and tool-call routing.
	Name() string

	// IsAvailable reports whether the provider is ready for use. A provider
	// may return false when a required binary is missing, a connection is
	// down, or it has not been configured.
	IsAvailable() bool

	// Initialize prepares the provider for the given session. It is called
	// once per session before any other methods. sessionID is the unique
	// identifier assigned to the current agent session.
	Initialize(sessionID string) error

	// GetToolSchemas returns the tool definitions this provider exports to
	// the agent at discovery time. The returned entries include both the
	// JSON Schema (for LLM consumption) and the Handler (for execution).
	// Return nil or an empty slice if the provider has no tools.
	GetToolSchemas() []tools.ToolEntry
}

// ── Optional sub-interfaces (discovered via type assertion) ──────────────

// SystemPromptProvider is an optional interface that providers may implement
// to contribute a block of text to the system prompt. The block is injected
// at a well-known location so the agent understands what memory tools are
// available and how to use them.
type SystemPromptProvider interface {
	// SystemPromptBlock returns a markdown-formatted block of instructions
	// that is injected into the agent's system prompt.
	SystemPromptBlock() string
}

// PrefetchProvider is an optional interface for providers that support
// anticipatory memory retrieval. When the agent is about to begin a turn,
// the MemoryManager calls Prefetch with the current user query so the
// provider can warm its caches or pre-load relevant context.
type PrefetchProvider interface {
	// Prefetch retrieves memory relevant to the given query and returns it
	// as a string suitable for injection into the agent's context. The
	// returned string may be empty if no relevant memory is found.
	Prefetch(query string) (string, error)

	// QueuePrefetch enqueues a prefetch request for asynchronous execution.
	// The provider schedules the lookup and the result is made available
	// through the provider's normal retrieval path on a subsequent call.
	QueuePrefetch(query string) error
}

// SyncTurnProvider is an optional interface for providers that persist
// conversation turns. After each assistant response, the MemoryManager
// calls SyncTurn so the provider can store the exchange for future
// retrieval.
type SyncTurnProvider interface {
	// SyncTurn stores a single user/assistant exchange. userMsg is the
	// content the user sent; assistantMsg is the content the assistant
	// produced in response.
	SyncTurn(userMsg, assistantMsg string) error
}

// ToolCallProvider is an optional interface for providers that handle
// tool calls directly. When the agent invokes a memory tool, the
// MemoryManager routes the call to the provider that owns the tool.
type ToolCallProvider interface {
	// HandleToolCall executes the named tool with the given arguments.
	// name identifies the tool (matching one of the entries returned by
	// GetToolSchemas). args contains the deserialized input parameters.
	// The return value is serialized to JSON and included in the tool
	// result sent back to the agent.
	HandleToolCall(name string, args map[string]any) (any, error)
}

// ShutdownProvider is an optional interface for providers that need to
// release resources, flush buffers, or close connections when the agent
// session ends.
type ShutdownProvider interface {
	// Shutdown gracefully tears down the provider. It is called once when
	// the MemoryManager itself is shutting down. The provider should release
	// all resources and return any final error.
	Shutdown() error
}
