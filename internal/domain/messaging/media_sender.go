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
	// Delete reports whether the sender can delete messages it sent.
	Delete bool
	// Clarify reports whether the sender can ask a question and return the
	// answer.
	Clarify bool
	// Picker reports whether the sender can present a single-choice
	// selection and carry the chosen option back (PickerRequester), on the
	// same native-or-fallback rule as Clarify.
	Picker bool
	// Approval reports whether the sender renders approval prompts through
	// the ApprovalRequester contract, so a caller can know a sender can
	// present an approve/deny prompt before a gated action needs one.
	Approval bool
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
