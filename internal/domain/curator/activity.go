package curator

import (
	"slices"
	"sync"
	"time"
)

// maxRecentActions bounds how much per-curator history activityTracker
// keeps in memory. Curator activity is inspectable, not archival -- there is
// deliberately no persistence layer here (archie-core-1786637489932-6),
// so this is a ring buffer, not a log.
const maxRecentActions = 20

// Activity is a curator's inspectable recent history: when it last ran, how
// many actions that run made, and the most recent actions across all runs.
// Recent is ordered newest first and capped at maxRecentActions.
type Activity struct {
	LastRunAt      time.Time
	LastRunActions int
	Recent         []Action
}

// activityTracker records recent curator activity in memory, fed directly
// from the runtime's own emit point (Runtime.emitRun) rather than by
// re-subscribing to the event bus -- the runtime already has the pass
// result in hand, and EventSink is fire-and-forget with no replay. Activity
// resets on daemon restart, same as every other piece of runtime state the
// curator family holds only in memory.
type activityTracker struct {
	mu   sync.Mutex
	data map[string]*Activity
}

func newActivityTracker() *activityTracker {
	return &activityTracker{data: make(map[string]*Activity)}
}

// record appends one pass's actions to the curator's history, newest first,
// and updates the last-run summary. actions may be empty (an idle pass still
// updates LastRunAt).
func (t *activityTracker) record(name string, at time.Time, actions []Action) {
	t.mu.Lock()
	defer t.mu.Unlock()

	a := t.data[name]
	if a == nil {
		a = &Activity{}
		t.data[name] = a
	}
	a.LastRunAt = at
	a.LastRunActions = len(actions)

	merged := make([]Action, 0, len(actions)+len(a.Recent))
	for _, action := range slices.Backward(actions) {
		merged = append(merged, action)
	}
	merged = append(merged, a.Recent...)
	if len(merged) > maxRecentActions {
		merged = merged[:maxRecentActions]
	}
	a.Recent = merged
}

// snapshot returns a copy of the named curator's activity, safe for the
// caller to read and mutate without racing the tracker's own writes.
func (t *activityTracker) snapshot(name string) (Activity, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	a, ok := t.data[name]
	if !ok {
		return Activity{}, false
	}
	cp := *a
	cp.Recent = append([]Action(nil), a.Recent...)
	return cp, true
}
