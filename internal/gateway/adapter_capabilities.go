package gateway

import "context"

// SendResult captures the outcome of a platform media delivery. It classifies
// failures so callers can apply retry policy without inspecting
// platform-specific error types.
type SendResult struct {
	// MessageID is the platform-assigned identifier of the sent message.
	// Empty when the send failed before the platform acknowledged it.
	MessageID string

	// Success is true when the platform accepted the message.
	Success bool

	// Retryable is true when the failure is transient and the caller may
	// retry with backoff.
	Retryable bool

	// Error is the underlying error when Success is false.
	Error error

	// ErrorCode classifies the failure for observability and retry policy.
	// Current values are "rate_limited", "network", "auth",
	// "invalid_message", and "internal"; success uses the empty string.
	ErrorCode string
}

// AdapterCapabilities reports which optional delivery behaviors a sender
// implements, so callers can check support before attempting an optional call.
type AdapterCapabilities struct {
	// Media is true when the sender implements MediaSender.
	Media bool
}

// MediaSender delivers a MessageEvent carrying MediaAttachments (image,
// video, audio, document) through a platform-specific media API. Senders are
// launch-scoped delivery values, separate from a channel's connection
// lifecycle; for example, Telegram binds one to a bot launch and chat.
type MediaSender interface {
	SendMedia(ctx context.Context, event MessageEvent) (SendResult, error)
}

// CapabilityReporter is the optional interface a sender implements to
// self-report which optional behaviors it supports. Callers should go
// through CapabilitiesOf rather than type-asserting this directly, so an
// implementation's capability declaration is checked consistently.
type CapabilityReporter interface {
	Capabilities() AdapterCapabilities
}

// CapabilitiesOf reports which optional behaviors sender supports. One that
// implements CapabilityReporter is asked directly; one that doesn't is
// treated as reporting the zero value regardless of which optional
// interfaces it actually implements under the hood, which is itself the
// contract every Capabilities() implementation must honor  --  see the
// regression coverage in adapter_capabilities_test.go.
//
// It takes any deliberately: media is delivered by launch-scoped sender
// values, not by a channel lifecycle interface.
func CapabilitiesOf(sender any) AdapterCapabilities {
	if reporter, ok := sender.(CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	return AdapterCapabilities{}
}
