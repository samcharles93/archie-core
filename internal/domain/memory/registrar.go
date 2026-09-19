package memory

import (
	"log/slog"
	"time"
)

// Registrar is the narrow typed host access an engine receives at
// registration. Fields are typed contracts declared here — this package
// never names who implements them, and the daemon itself is never part of
// it.
type Registrar struct {
	// Events publishes engine activity (writes, forgets, failures) for
	// observability. Implementations must be non-blocking and bounded (drop
	// on overflow), matching the curator family's EventSink contract, so an
	// engine can never apply backpressure to a chat turn or the daemon.
	// Always bound.
	Events EventSink
	// Clock is injectable time. Always bound; nil at construction is
	// replaced with the system clock.
	Clock Clock
	// Log is the engine-scoped diagnostic logger, optional so domain tests
	// and embedders can construct a registrar without a logging sink (the
	// curator family's Registrar carries the same field for the same
	// reason). An engine emits here what it cannot emit as an event: the
	// content scanner's warn level, which allows the write and is expected
	// to leave a record of what it saw.
	Log *slog.Logger
}

// EventSink publishes engine activity. Mirrors curator.EventSink: emission
// side only, non-blocking and bounded.
type EventSink interface {
	Emit(kind, detail string, data map[string]any)
}

// Clock is injectable time: Now for timestamps.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
