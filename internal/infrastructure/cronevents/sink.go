// Package cronevents is the observability half of archied's cron/scheduling
// capability: it adapts the daemon's in-process event bus to the ticker
// engine's narrow emission contract.
//
// The domain's engine (internal/domain/scheduling) declares what it needs --
// Emit(kind, detail, data) -- and names no bus, which is what lets its timing
// behaviour be tested without one. This package is the other side of that
// seam: the bus exists (internal/events), and something has to turn one shape
// into the other.
//
// The adapter is deliberately thin, and deliberately tolerant of a missing
// bus: a scheduled run has no task, no chat turn and no workflow behind it, so
// these events are the only trace it leaves, and the engine documents a nil
// sink as a valid configuration (it still runs, it is just unobservable).
// Which leaves the operator's choice -- publish or not -- and the daemon's
// ability to answer it, as the only two inputs.
package cronevents

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
)

// Sink builds the scheduling.Sink for the configured [scheduling].events_sink
// mode, over the daemon's event bus.
//
// config.EventsSinkBus (and a non-nil bus) yields a sink that publishes each
// emitted event on the bus. "" and config.EventsSinkNone, or a nil bus,
// yield a nil sink -- the engine tolerates that by design, and returning nil
// rather than a no-op sink keeps "emission is off" visible to whoever reads
// the composition.
//
// An unrecognised mode is an error: the loader rejects such a value first, but
// a hand-built config must not silently disable observability either.
func Sink(mode string, bus *events.Bus) (scheduling.Sink, error) {
	switch mode {
	case "", config.EventsSinkNone:
		return nil, nil
	case config.EventsSinkBus:
		if bus == nil {
			return nil, nil
		}
		return busSink{bus: bus}, nil
	default:
		return nil, fmt.Errorf("cronevents: unknown events_sink %q (want %q or %q)",
			mode, config.EventsSinkBus, config.EventsSinkNone)
	}
}

// busSink publishes the engine's emission on the bus. The event carries the
// engine's own shape -- Kind, Detail and Data, exactly as emitted -- and no
// task-scoped identity, because a scheduled run has none: borrowing the
// emitting daemon's would attach a cron run's event to some unrelated task's
// timeline. Bus.Publish stamps the timestamp and fans out to bounded,
// dropping subscriber buffers, so a stalled dashboard connection cannot
// backpressure a scheduled run.
type busSink struct {
	bus *events.Bus
}

func (s busSink) Emit(kind, detail string, data map[string]any) {
	s.bus.Publish(events.Event{Kind: kind, Detail: detail, Data: data})
}

var _ scheduling.Sink = busSink{}
