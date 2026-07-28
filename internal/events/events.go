// Package events is archied's in-process observability stream. It uses one
// event type, mutex fan-out, and bounded per-subscriber buffers that DROP on
// overflow, so a stalled dashboard connection can never apply backpressure to
// the task engine.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Kind values. Data carries kind-specific fields.
const (
	KindTaskQueued  = "task_queued"
	KindStageStart  = "stage_start"
	KindStageFinish = "stage_finish" // data: duration_ms, error
	KindAgentFinish = "agent_finish" // data: status, stop_reason, tokens, iterations, model
	KindParked      = "parked"       // data: reason
	KindOutcome     = "outcome"      // data: status, detail
	KindPRMerged    = "pr_merged"
	KindPRRejected  = "pr_rejected"
	KindTaskDead    = "task_dead"
	KindLog         = "log" // data: level, msg
)

// Event is the single wire type: task lifecycle, stage progress, agent
// stats, and log lines all flow through it  --  every consumer (SQLite
// sink, SSE fan-out, future aggregators) is just another subscriber.
type Event struct {
	ID       int64          `json:"id,omitempty"` // set by the store sink
	At       time.Time      `json:"at"`
	Kind     string         `json:"kind"`
	TaskID   int64          `json:"task_id,omitempty"`
	Repo     string         `json:"repo,omitempty"`
	Issue    int            `json:"issue,omitempty"`
	Workflow string         `json:"workflow,omitempty"`
	Stage    string         `json:"stage,omitempty"`
	Detail   string         `json:"detail,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// Sub is one subscriber: a bounded channel plus a drop counter.
type Sub struct {
	C       <-chan Event
	c       chan Event
	dropped atomic.Int64
	bus     *Bus
}

// Dropped reports how many events this subscriber missed to overflow.
func (s *Sub) Dropped() int64 { return s.dropped.Load() }

// Close detaches the subscriber and closes its channel.
func (s *Sub) Close() { s.bus.unsubscribe(s) }

// Bus fans events out to subscribers in publish order.
type Bus struct {
	mu     sync.Mutex
	subs   []*Sub
	closed bool
}

func NewBus() *Bus { return &Bus{} }

// Subscribe registers a subscriber with the given buffer size.
func (b *Bus) Subscribe(buffer int) *Sub {
	if buffer <= 0 {
		buffer = 64
	}
	s := &Sub{c: make(chan Event, buffer), bus: b}
	s.C = s.c
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(s.c)
		return s
	}
	b.subs = append(b.subs, s)
	return s
}

// Publish stamps and delivers the event to every subscriber. Full
// subscriber buffers drop the event (counted) rather than blocking.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, s := range b.subs {
		select {
		case s.c <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

func (b *Bus) unsubscribe(sub *Sub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s == sub {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			close(s.c)
			return
		}
	}
}

// Close detaches and closes all subscribers; further publishes no-op.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, s := range b.subs {
		close(s.c)
	}
	b.subs = nil
}
