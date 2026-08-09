package gateway

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Turns serialises chat turns per session and keeps the running turn
// cancellable.
//
// Channel event loops are typically single-threaded -- go-telegram/bot,
// for one, runs a single worker that calls handlers synchronously. Running
// a turn on that goroutine blocks delivery of every subsequent update,
// including the command meant to stop it: /stop would sit unread in the
// update channel until the turn it was meant to kill had already finished.
//
// Submitting to Turns hands the work to a per-session goroutine and returns
// immediately, so the event loop stays free and control commands are always
// serviceable. Ordering is preserved by the lane being serial, not by the
// event loop being blocked.
//
// Queues are unbounded and never shed. A dropped message is a silent
// failure that surfaces much later as an agent that ignored something,
// which costs far more to diagnose than the memory a backlog occupies.
// [Turns.Stop] is the way a backlog is cleared -- deliberately, by the
// person who created it.
type Turns struct {
	log *slog.Logger

	mu    sync.Mutex
	lanes map[string]*lane
}

// queued is one pending turn: the work, already bound to its own
// cancellable context, and the cancel that releases it.
//
// The context is derived when the turn is submitted rather than when it
// starts running, which means a turn has a live cancel handle for its whole
// time in the backlog. Stop can therefore cancel queued turns rather than
// merely dropping them, and each turn stays tied to the caller that asked
// for it -- it dies when that caller's context does.
type queued struct {
	run    func()
	cancel context.CancelFunc
}

// lane is one session's serial worker.
//
// A single mutex guards the backlog, the running turn's cancel, and whether
// a worker is live, so that Stop observes them together. Splitting them
// would leave a window where a turn finishes between the backlog being
// cleared and the cancel being called, letting a queued turn start after a
// stop.
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

// Submit schedules run for the given session and returns without waiting
// for it. run receives a context derived from ctx that [Turns.Stop] can
// cancel.
//
// Submit never blocks and never rejects: messages queue.
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

// Stop cancels the session's running turn and discards everything queued
// behind it. It reports whether a turn was actually cancelled and how many
// queued turns were dropped.
//
// Dropping the queue is intentional. "Stop" means stop, not "stop this one
// and immediately begin the next thing I said" -- starting the queued turn
// would look to the sender exactly like the stop had failed.
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

// begin pops the oldest queued turn and publishes its cancel in the same
// critical section. It reports false when the backlog is empty; the owning
// Turns then retires the worker and map entry under both locks.
//
// The pop and the publish must happen together. Were the turn popped first
// and its cancel published after, a Stop landing in between would see an
// empty backlog and no running turn, report that nothing was happening, and
// let the turn it should have killed start regardless.
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

// runOne executes one turn, always retracting its cancel afterwards so a
// later Stop cannot cancel an already-finished turn.
//
// Panics are contained here rather than by the channel's own recovery
// middleware: that middleware wraps the event-loop goroutine, and the turn
// no longer runs on it. Without this, one bad turn would take the lane's
// goroutine down and the session would silently stop answering forever.
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
