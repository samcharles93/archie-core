package archiemessaging

import (
	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/status"
)

// channelDescriptors declares the operator-facing identity of each composed
// channel. Only what an operator may see: no configuration value ever enters a
// descriptor, so the manager cannot become a second source for a token or a
// credential.
func channelDescriptors(instances []channelInstance) []status.Descriptor {
	descriptors := make([]status.Descriptor, 0, len(instances))
	for _, instance := range instances {
		descriptors = append(descriptors, status.Descriptor{
			ID:         instance.name,
			Name:       instance.name,
			Configured: true,
			// Telegram is the only front-end that carries a reload seam today
			// (archiemessaging.telegramFeatures rewires it on request). Declaring
			// the others would invite the dashboard to offer a button that
			// answers "reloaded" and changed nothing.
			ReloadSupported: instance.name == "telegram",
		})
	}
	return descriptors
}

// lifecycleFor returns the report a channel writes its own state through. Each
// channel gets its own closure, so one channel's failure marks only itself --
// which is the reason the manager is keyed by channel rather than being one flag
// on the service.
//
// Before this, Start handed every channel an empty channels.Lifecycle{}, so
// nothing recorded starting, running or failed anywhere and
// internal/channels/status.Manager had no production writer at all
// (archie-core-8cda.6.8). The channels have always reported: all three call
// ReportStarting and ReportRunning on whichever lifecycle they are handed.
func (s *Service) lifecycleFor(id string) channels.Lifecycle {
	return channels.Lifecycle{
		Starting: func() { s.status.MarkStarting(id) },
		Running:  func() { s.status.MarkRunning(id) },
	}
}

// ChannelStatus reports the last known state of every composed channel. It is the
// producer side of the dashboard surface: whatever carries this to the UI reads
// here rather than reconstructing it, and the manager stays the one place channel
// lifecycle is recorded.
func (s *Service) ChannelStatus() []status.Status {
	return s.status.Snapshot()
}
