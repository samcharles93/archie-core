package messaging

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Turns runs chat turns serially per session on their own goroutines, so
// the channel's event loop stays free, and keeps the running turn
// cancellable. Queues are unbounded.
type Turns struct {
	log *slog.Logger

	mu    sync.Mutex
	lanes map[string]*lane
}

// queued is one pending turn with its own cancellable context.
type queued struct {
	run    func()
	cancel context.CancelFunc
}

// lane is one session's serial worker; one mutex guards its backlog, running
// cancel and liveness.
type lane struct {
	mu      sync.Mutex
	pending []queued
	// cancel stops the turn currently running in this lane. It is nil
	// whenever the lane is idle.
	cancel context.CancelFunc
	// working reports whether a worker goroutine is draining this lane.
	// The worker exits when the backlog empties, so this is what tells
	// Submit whether to start a new one.
	working bool
}

// NewTurns returns an idle dispatcher.
func NewTurns(log *slog.Logger) *Turns {
	return &Turns{log: log, lanes: make(map[string]*lane)}
}

// Submit queues run for session and returns. run's context is cancelled by
// Stop.
func (t *Turns) Submit(ctx context.Context, session string, run func(context.Context)) {
	turnCtx, cancel := context.WithCancel(ctx)

	t.mu.Lock()
	l, ok := t.lanes[session]
	if !ok {
		l = &lane{}
		t.lanes[session] = l
	}
	l.mu.Lock()
	l.pending = append(l.pending, queued{run: func() { run(turnCtx) }, cancel: cancel})
	depth := len(l.pending)
	start := !l.working
	if start {
		l.working = true
	}
	l.mu.Unlock()
	t.mu.Unlock()

	if start {
		go t.serve(session, l)
	}
	if depth > 1 {
		t.log.Info("chat turn queued", "session", session, "queue_depth", depth)
	}
}

// Stop cancels the session's running turn and drops its queue, reporting
// whether a turn was cancelled and how many were dropped.
func (t *Turns) Stop(session string) (cancelled bool, dropped int) {
	l := t.lane(session)
	if l == nil {
		return false, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	dropped = len(l.pending)
	for i, q := range l.pending {
		q.cancel()
		l.pending[i] = queued{}
	}
	l.pending = nil
	if l.cancel != nil {
		l.cancel()
		cancelled = true
	}
	return cancelled, dropped
}

// Running reports whether the session currently has a turn in flight.
func (t *Turns) Running(session string) bool {
	l := t.lane(session)
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cancel != nil
}

// Queued reports how many turns are waiting behind the running one.
func (t *Turns) Queued(session string) int {
	l := t.lane(session)
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending)
}

// lane returns the session's active lane, or nil if it has never been used or
// its completed lane has already retired.
func (t *Turns) lane(session string) *lane {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lanes[session]
}

// serve drains the lane's backlog one turn at a time and exits once it is
// empty. Submit starts a fresh worker when more arrives, so an idle session
// costs no goroutine.
func (t *Turns) serve(session string, l *lane) {
	log := t.log.With("session", session)
	for {
		next, ok := l.begin()
		if !ok {
			if t.retire(session, l) {
				return
			}
			continue
		}
		l.runOne(next, log)
	}
}

// retire removes an idle lane while holding the same two locks Submit uses to
// find and append to it. Without that joint critical section, Submit could find
// the old lane immediately before cleanup deleted it and strand the new turn on
// a worker that had already exited.
func (t *Turns) retire(session string, l *lane) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	current, ok := t.lanes[session]
	if !ok || current != l {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pending) != 0 {
		return false
	}
	l.working = false
	delete(t.lanes, session)
	return true
}

// begin pops the oldest queued turn and publishes its cancel atomically. It
// reports false when the backlog is empty.
func (l *lane) begin() (queued, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.pending) == 0 {
		// The owning Turns retires the lane under both the map and lane
		// locks. Changing working here would race a concurrent Submit.
		return queued{}, false
	}

	next := l.pending[0]
	// Clear the vacated slot so a completed turn's closure -- and whatever
	// it captured -- does not stay reachable through the backing array.
	l.pending[0] = queued{}
	l.pending = l.pending[1:]

	l.cancel = next.cancel
	return next, true
}

// runOne runs one turn, recovering panics, and clears its cancel afterwards.
func (l *lane) runOne(next queued, log *slog.Logger) {
	defer next.cancel()

	defer func() {
		l.mu.Lock()
		l.cancel = nil
		l.mu.Unlock()

		if r := recover(); r != nil {
			log.Error("chat turn panicked", "panic", r, "stack", string(debug.Stack()))
		}
	}()

	next.run()
}
