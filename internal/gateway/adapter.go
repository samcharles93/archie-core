package gateway

import "context"

// SendResult captures the outcome of a Send operation through a platform
// adapter. It classifies errors so callers can implement retry policies
// without inspecting platform-specific error types.
type SendResult struct {
	// MessageID is the platform-assigned identifier of the sent message.
	// Empty when the send failed before the platform acknowledged it.
	MessageID string

	// Success is true when the platform accepted the message.
	Success bool

	// Retryable is true when the error is transient — rate limits, network
	// timeouts, temporary platform outages — and the caller should retry
	// with backoff.
	Retryable bool

	// Error is the underlying error when Success is false.
	Error error

	// ErrorCode classifies the failure for observability and retry-policy
	// decisions. Values:
	//
	//   ""                — no error (Success == true)
	//   "rate_limited"    — platform rejected due to rate limiting
	//   "network"         — connection lost or timeout
	//   "auth"            — token expired or credentials invalid
	//   "invalid_message" — message content rejected by platform
	//   "internal"        — unexpected adapter or platform error
	ErrorCode string
}

// ChatInfo is static metadata about the conversation a platform adapter is
// connected to. Adapters return this from ChatInfo() so callers can display
// context without knowing platform internals.
type ChatInfo struct {
	// ID is the platform-specific chat identifier.
	ID string

	// Title is the human-readable chat name (group title, DM username,
	// channel name).
	Title string

	// Type is the chat kind: "private", "group", "channel", "supergroup".
	Type string

	// Platform names the adapter ("telegram", "discord", "web").
	Platform string
}

// BasePlatformAdapter is the interface every platform adapter must implement.
// Each implementation owns its connection lifecycle and translates
// platform-specific events into the shared MessageEvent model.
//
// The daemon instantiates one adapter per active bot identity × platform and
// calls Connect to start the event loop. Inbound messages are delivered to a
// shared handler; outbound messages flow through Send.
type BasePlatformAdapter interface {
	// Connect establishes the persistent connection and starts the inbound
	// message loop. It blocks until ctx is cancelled or the connection
	// fails irrecoverably. Implementations should reconnect transient
	// failures internally.
	Connect(ctx context.Context) error

	// Disconnect gracefully tears down the connection. After Disconnect
	// returns the adapter may be reused by calling Connect again.
	Disconnect(ctx context.Context) error

	// Send delivers a message event to the platform. The returned
	// SendResult classifies the outcome so callers can decide whether
	// to retry.
	Send(ctx context.Context, event MessageEvent) (SendResult, error)

	// ChatInfo returns static metadata about the connected conversation.
	// Useful for logging, status displays, and multi-bot dashboards.
	ChatInfo(ctx context.Context) (ChatInfo, error)
}
