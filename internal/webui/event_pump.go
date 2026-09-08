package webui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

// DefaultEventPollInterval is how often the UI process asks the State Store
// for events it has not seen. It bounds activity-feed latency; the feed
// reports task lifecycle history, and an operator action already gets its own
// synchronous HTTP response, so a second is not felt.
const DefaultEventPollInterval = time.Second

// Page sizes. Priming walks the whole table once and wants few round trips;
// steady-state ticks expect nothing or a handful and want small messages.
const (
	eventPumpPrimePageSize = 5000
	eventPumpPageSize      = 200
)

// eventPump is the UI process's substitute for the daemon's in-process event
// bus. The daemon hands persisted events to Server.Broadcast from its own bus
// subscription (internal/app/archied/main.go's persistAndBroadcastEvents); a
// process that owns no bus reads the same events back out of the State Store
// instead, over the EventsSince cursor.
//
// Polling is sufficient rather than a compromise, for the reasons recorded in
// docs/architecture/migration-decisions.md ("Dashboard live event delivery"):
// the store serialises writes on a single connection, so the id cursor is
// assigned in commit order and cannot skip; sseStream.drain treats a broadcast
// as a wakeup and re-reads the gap from the store, so nothing here changes SSE
// semantics; and reading the table catches writers that never touch a bus,
// including the audit event ArchiveTask writes inside its transaction.
type eventPump struct {
	store     eventReader
	log       func(string, ...any)
	broadcast func(events.Event)
	// watermark is the highest event id already handed to broadcast. It only
	// ever moves forward, and only past an event that was delivered.
	watermark int64
}

// eventReader is the one method the pump needs, so a test can drive it
// without a whole TaskStore.
type eventReader interface {
	EventsSince(ctx context.Context, sinceID int64, limit int) ([]events.Event, error)
}

func (s *Server) newEventPump() *eventPump {
	return &eventPump{store: s.Store, log: s.logf, broadcast: s.Broadcast}
}

// PumpEvents delivers events persisted after it starts to every connected SSE
// client, until ctx ends. It is the UI process's only live-delivery path;
// history is served by each client's own catch-up, not by this.
func (s *Server) PumpEvents(ctx context.Context, interval time.Duration) error {
	if s.Store == nil {
		return errors.New("event pump needs a store to read events from")
	}
	if interval <= 0 {
		interval = DefaultEventPollInterval
	}
	pump := s.newEventPump()
	if err := pump.prime(ctx); err != nil {
		return fmt.Errorf("prime event pump: %w", err)
	}
	pump.run(ctx, interval)
	return nil
}

// prime advances the watermark past everything already persisted, without
// delivering any of it. Without this the pump would replay the entire events
// table into every browser the first time it ticks; catch-up already serves a
// client the history it asked for.
func (p *eventPump) prime(ctx context.Context) error {
	for {
		batch, err := p.store.EventsSince(ctx, p.watermark, eventPumpPrimePageSize)
		if err != nil {
			return err
		}
		before := p.watermark
		for _, e := range batch {
			if e.ID > p.watermark {
				p.watermark = e.ID
			}
		}
		// A short page is the end of the table. The watermark check is the
		// guard against a full page that advanced nothing, which would
		// otherwise spin forever.
		if len(batch) < eventPumpPrimePageSize || p.watermark == before {
			return nil
		}
	}
}

// deliver broadcasts every event after the watermark and reports how many.
// A fetch that fails leaves the watermark where it was, so the next tick
// retries the same range: a State Store restart costs latency, not events.
func (p *eventPump) deliver(ctx context.Context) (int, error) {
	delivered := 0
	for {
		batch, err := p.store.EventsSince(ctx, p.watermark, eventPumpPageSize)
		if err != nil {
			return delivered, err
		}
		before := p.watermark
		for _, e := range batch {
			if e.ID <= p.watermark {
				continue
			}
			p.broadcast(e)
			p.watermark = e.ID
			delivered++
		}
		if len(batch) < eventPumpPageSize || p.watermark == before {
			return delivered, nil
		}
	}
}

// run ticks until ctx ends. A failed tick is logged and retried on the next
// one rather than ending the pump: the feed is a view, and losing it for a
// poll interval is better than losing it for the life of the process.
func (p *eventPump) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.deliver(ctx); err != nil && ctx.Err() == nil {
				p.log("event pump fetch failed", "since", p.watermark, "err", err)
			}
		}
	}
}
