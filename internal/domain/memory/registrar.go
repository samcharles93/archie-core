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
	// Events receives engine activity. Must not block.
	Events EventSink
	// Clock is injectable time. Always bound; nil at construction is
	// replaced with the system clock.
	Clock Clock
	// Log is the engine's logger. Optional.
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
