package logging

import (
	"context"
	"log/slog"
	"os"
	"sync"
)

// TaskRegistry manages one open log destination per currently-running task
// attempt, shared between whatever starts and ends a task's run (Open,
// Close) and whatever receives that task's remotely-shipped log records
// (Write) -- in the daemon, two different goroutines: task dispatch, and
// the NATS system-log subscription handler.
//
// The zero value is not usable; construct with NewTaskRegistry. A nil
// *TaskRegistry is safe to call every method on and always behaves as
// "task logging is not configured" -- the same "optional, nil means
// disabled" convention every other composition-wired field in Daemon
// follows, so callers never need a separate nil check before using one.
type TaskRegistry struct {
	baseDir string
	feed    *Feed
	opts    TaskSinkOptions

	mu   sync.Mutex
	open map[int64]*registeredSink
}

type registeredSink struct {
	sink   *TaskSink
	logger *slog.Logger
}

// NewTaskRegistry builds a registry writing under baseDir, mirroring every
// write into feed (may be nil to skip mirroring) the same way a locally
// logged entry would appear on the dashboard.
func NewTaskRegistry(baseDir string, feed *Feed, opts TaskSinkOptions) *TaskRegistry {
	return &TaskRegistry{baseDir: baseDir, feed: feed, opts: opts, open: make(map[int64]*registeredSink)}
}

// Open starts logging for a task attempt. A previous attempt already open
// under the same task ID is closed first: a retry reuses the task ID at a
// higher Attempt, and the old attempt's sink must stop receiving lines
// under its name rather than silently keep taking them.
func (r *TaskRegistry) Open(taskID int64, attempt int) error {
	if r == nil {
		return nil
	}
	sink, err := NewTaskSink(r.baseDir, taskID, attempt, r.opts)
	if err != nil {
		return err
	}
	logger := slog.New(NewFeedHandler(sink.Logger().Handler(), r.feed))

	r.mu.Lock()
	prev := r.open[taskID]
	r.open[taskID] = &registeredSink{sink: sink, logger: logger}
	r.mu.Unlock()

	if prev != nil {
		return prev.sink.Close()
	}
	return nil
}

// Close stops logging for a task and releases its file handle. Safe to call
// for a task with no open sink -- every caller ending a task's run calls
// this unconditionally, whether or not Open ever succeeded for it.
func (r *TaskRegistry) Close(taskID int64) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	entry, ok := r.open[taskID]
	delete(r.open, taskID)
	r.mu.Unlock()
	if !ok {
		return nil
	}
	return entry.sink.Close()
}

// Write appends one remotely-shipped entry to taskID's open sink, mirroring
// it into the live dashboard feed the same way a locally-written log would
// be. Reports false for a task with no open sink -- not an error: a system
// log message is fire-and-forget best effort, and a late or duplicate
// delivery after the task finished (or one this daemon instance never
// dispatched) is expected, not exceptional.
func (r *TaskRegistry) Write(taskID int64, entry Entry) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	got, ok := r.open[taskID]
	r.mu.Unlock()
	if !ok {
		return false
	}

	var level slog.Level
	_ = level.UnmarshalText([]byte(entry.Level)) // unparseable falls back to Info, slog.Level's zero value

	attrs := make([]slog.Attr, 0, len(entry.Fields))
	for k, v := range entry.Fields {
		attrs = append(attrs, slog.Any(k, v))
	}
	got.logger.LogAttrs(context.Background(), level, entry.Message, attrs...)
	return true
}

// Remove deletes taskID's entire log directory -- every attempt, every
// rotated generation -- so task logs don't accumulate forever once a task
// is archived. Safe to call whether or not a sink is currently open (Close
// first regardless: this only removes files, it does not stop writes to an
// open sink) and whether or not the directory exists at all.
func (r *TaskRegistry) Remove(taskID int64) error {
	if r == nil {
		return nil
	}
	return os.RemoveAll(TaskLogDir(r.baseDir, taskID))
}
