package messaging

import "context"

// SendResult captures the outcome of a platform media delivery.
type SendResult struct {
	MessageID string
	Success   bool
	Retryable bool
	Error     error
	ErrorCode string
}

// AdapterCapabilities reports which optional delivery behaviors a sender
// implements.
type AdapterCapabilities struct {
	Media bool
}

// MediaSender delivers a MessageEvent carrying MediaAttachments through a
// platform-specific media API.
type MediaSender interface {
	SendMedia(ctx context.Context, event MessageEvent) (SendResult, error)
}

// CapabilityReporter is the optional interface a sender implements to
// self-report which optional behaviors it supports.
type CapabilityReporter interface {
	Capabilities() AdapterCapabilities
}

// CapabilitiesOf reports which optional behaviors sender supports.
func CapabilitiesOf(sender any) AdapterCapabilities {
	if reporter, ok := sender.(CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	return AdapterCapabilities{}
}
