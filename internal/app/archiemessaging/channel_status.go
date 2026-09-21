package archiemessaging

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
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
		Starting: func() { s.status.MarkStarting(id); s.signalPublish() },
		Running:  func() { s.status.MarkRunning(id); s.signalPublish() },
	}
}

// signalPublish asks the publisher to report the current set. The signal
// coalesces: a burst of transitions is one write, because a reader only ever
// wants the latest state of every channel.
func (s *Service) signalPublish() {
	if s.statusWriter == nil {
		return
	}
	select {
	case s.publish <- struct{}{}:
	default:
	}
}

// publishStatus writes the current set, or deletes it when the service hosts no
// channels -- an empty report is a report, and leaving the previous set standing
// would show channels that are gone.
func (s *Service) publishStatus(ctx context.Context) error {
	reported := s.ChannelStatus()
	rows := make([]storecontract.ChannelStatus, 0, len(reported))
	for _, channel := range reported {
		rows = append(rows, storecontract.ChannelStatus{
			ID:              channel.ID,
			Name:            channel.Name,
			State:           string(channel.State),
			Detail:          channel.Detail,
			Configured:      channel.Configured,
			ReloadSupported: channel.ReloadSupported,
			ObservedAt:      time.Now(),
		})
	}
	return s.statusWriter.PutChannelStatus(ctx, rows)
}

// publishLoop reports channel state until ctx ends. A failed write is logged and
// retried on the next signal: the dashboard losing channel state is worth a
// warning, and never worth failing a channel start over.
func (s *Service) publishLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.publish:
			if err := s.publishStatus(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("publishing channel status failed", "err", err)
			}
		}
	}
}

// ChannelStatus reports the last known state of every composed channel. It is the
// producer side of the dashboard surface: whatever carries this to the UI reads
// here rather than reconstructing it, and the manager stays the one place channel
// lifecycle is recorded.
func (s *Service) ChannelStatus() []status.Status {
	return s.status.Snapshot()
}
