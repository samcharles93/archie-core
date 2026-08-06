package curator

import (
	"context"
	"slices"

	"github.com/samcharles93/archie-core/internal/events"
)

// WakeOnPrimaryInput forwards primary-input events from the in-process bus
// to the runtime as best-effort nudges (archie-core-035). Only the kinds in
// inputKinds wake curators — the trigger-accounting invariant: curator-
// produced activity (curator_run, curator_action, curator_error, and any
// future derived-write kind) never appears on this path, so a curator's own
// output cannot feed its own trigger. The subscription is bounded and the
// bus drops on overflow, so a slow curator can never backpressure the chat
// publisher; a dropped event only delays a run to the next check-in. The
// forwarder runs until ctx is cancelled or the bus closes.
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
