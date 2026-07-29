package daemon

import (
	"context"
	"sort"
	"sync"
)

// RunningTask identifies a task the daemon is currently executing.
type RunningTask struct {
	ID       int64
	Identity string
}

// runningTasks tracks the tasks currently executing so they can be stopped
// on request.
//
// A task's cancel function is the only handle that reaches work already in
// flight. Without it the store can record that a task was cancelled while
// the task carries on -- which is what /cancel did: it refused outright
// when a task was running, so the one case worth interrupting was the one
// case it would not touch.
type runningTasks struct {
	mu      sync.Mutex
	entries map[int64]*runningEntry
}

type runningEntry struct {
	identity string
	cancel   context.CancelFunc
}

// begin registers a task as running and returns a context that [stop] can
// cancel, together with the function that retires it.
func (r *runningTasks) begin(ctx context.Context, id int64, identity string) (context.Context, func()) {
	taskCtx, cancel := context.WithCancel(ctx)

	r.mu.Lock()
	if r.entries == nil {
		r.entries = make(map[int64]*runningEntry)
	}
	r.entries[id] = &runningEntry{identity: identity, cancel: cancel}
	r.mu.Unlock()

	return taskCtx, func() {
		r.mu.Lock()
		delete(r.entries, id)
		r.mu.Unlock()
		cancel()
	}
}

// stop cancels one running task, reporting whether it was running.
func (r *runningTasks) stop(id int64) bool {
	r.mu.Lock()
	entry, ok := r.entries[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	entry.cancel()
	return true
}

// stopAll cancels every running task belonging to identity, or all of them
// when identity is empty. It returns the IDs it cancelled, in order.
func (r *runningTasks) stopAll(identity string) []int64 {
	r.mu.Lock()
	var (
		ids     []int64
		cancels []context.CancelFunc
	)
	for id, entry := range r.entries {
		if identity != "" && entry.identity != identity {
			continue
		}
		ids = append(ids, id)
		cancels = append(cancels, entry.cancel)
	}
	r.mu.Unlock()

	// Cancel outside the lock: a cancel can synchronously unwind a task
	// whose cleanup calls back in here to retire itself.
	for _, cancel := range cancels {
		cancel()
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// list returns the tasks currently running, for reporting.
func (r *runningTasks) list() []RunningTask {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]RunningTask, 0, len(r.entries))
	for id, entry := range r.entries {
		out = append(out, RunningTask{ID: id, Identity: entry.identity})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CancelTask stops a task that is currently executing, reporting whether
// it was running. A task that is not running is not an error here: the
// caller decides what a miss means.
func (d *Daemon) CancelTask(id int64) bool {
	return d.running.stop(id)
}

// CancelRunningTasks stops every task running for identity, or all of them
// when identity is empty, and returns the IDs it stopped.
func (d *Daemon) CancelRunningTasks(identity string) []int64 {
	return d.running.stopAll(identity)
}

// RunningTasks lists the tasks currently executing.
func (d *Daemon) RunningTasks() []RunningTask {
	return d.running.list()
}
