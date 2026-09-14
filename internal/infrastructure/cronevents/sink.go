// Package cronevents adapts the daemon's event bus to scheduling.Sink.
package cronevents

import (
	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
)

// New returns a scheduling sink over bus. A nil bus yields a nil sink.
func New(bus *events.Bus) scheduling.Sink {
	if bus == nil {
		return nil
	}
	return busSink{bus: bus}
}

type busSink struct {
	bus *events.Bus
}

func (s busSink) Emit(kind, detail string, data map[string]any) {
	s.bus.Publish(events.Event{Kind: kind, Detail: detail, Data: data})
}

var _ scheduling.Sink = busSink{}
