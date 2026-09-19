package gateway

import "github.com/samcharles93/archie-core/internal/domain/messaging"

// SendResult captures the outcome of a platform media delivery.
type SendResult = messaging.SendResult

// AdapterCapabilities reports which optional delivery behaviors a sender
// implements.
type AdapterCapabilities = messaging.AdapterCapabilities

// MediaSender delivers a MessageEvent carrying MediaAttachments.
type MediaSender = messaging.MediaSender

// CapabilityReporter is the optional interface a sender implements.
type CapabilityReporter = messaging.CapabilityReporter

// CapabilitiesOf reports which optional behaviors sender supports.
func CapabilitiesOf(sender any) AdapterCapabilities {
	return messaging.CapabilitiesOf(sender)
}
