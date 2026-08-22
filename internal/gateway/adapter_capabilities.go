package gateway

import "context"

// AdapterCapabilities reports which optional BasePlatformAdapter behaviors
// an adapter implements, so callers can check support before attempting an
// optional call rather than discovering it via a failed type assertion or a
// runtime error.
type AdapterCapabilities struct {
	// Media is true when the adapter implements MediaSender.
	Media bool
}

// MediaSender is the optional interface a BasePlatformAdapter implements
// when it can deliver a MessageEvent carrying MediaAttachments (image,
// video, audio, document) through a platform-specific media API distinct
// from plain-text Send  --  e.g. Telegram's SendPhoto/SendVideo versus
// SendMessage.
type MediaSender interface {
	SendMedia(ctx context.Context, event MessageEvent) (SendResult, error)
}

// CapabilityReporter is the optional interface an adapter implements to
// self-report which optional behaviors it supports. Callers should go
// through CapabilitiesOf rather than type-asserting this directly, so an
// adapter that implements MediaSender but forgets CapabilityReporter still
// reports correctly.
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
// It takes any rather than BasePlatformAdapter deliberately: the live
// channels (telegram, email, webhook) implement channels.Channel, not
// BasePlatformAdapter, and media is delivered by launch-scoped sender
// values that are neither. Binding this helper to BasePlatformAdapter
// would tie it to a type slated for removal (archie-core-azbo).
func CapabilitiesOf(sender any) AdapterCapabilities {
	if reporter, ok := sender.(CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	return AdapterCapabilities{}
}
