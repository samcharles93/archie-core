package curator

import (
	"context"
	"slices"

	"github.com/samcharles93/archie-core/internal/events"
)

// WakeOnPrimaryInput nudges the runtime for each bus event of inputKinds
// until ctx ends or the bus closes. Dropped events only delay a run.
func WakeOnPrimaryInput(ctx context.Context, bus *events.Bus, rt *Runtime, inputKinds ...string) {
	sub := bus.Subscribe(defaultWakeBuffer)
	go func() {
		defer sub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-sub.C:
				if !ok {
					return // bus closed
				}
				if slices.Contains(inputKinds, e.Kind) {
					rt.Nudge("")
				}
			}
		}
	}()
}
