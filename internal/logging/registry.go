package logging

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

// receivedAtField records when a shipped entry was received.
const receivedAtField = "received_at"

// TaskRegistry holds one open log sink per running task attempt. Construct
// with NewTaskRegistry; every method is safe on nil, meaning disabled.
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

// Write appends a shipped entry to taskID's open sink and the live feed,
// keeping its event time and recording received_at. It returns false when no
// sink is open. ctx cancellation is ignored.
func (r *TaskRegistry) Write(ctx context.Context, taskID int64, entry Entry) bool {
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

	// Use the entry's own time, or now if it has none.
	received := time.Now()
	happened := entry.Time
	if happened.IsZero() {
		happened = received
	}

	attrs := make([]slog.Attr, 0, len(entry.Fields)+1)
	for k, v := range entry.Fields {
		attrs = append(attrs, slog.Any(k, v))
	}
	attrs = append(attrs, slog.Time(receivedAtField, received))

	// Build the record directly to keep its time, checking Enabled first.
	record := slog.NewRecord(happened, level, entry.Message, 0)
	record.AddAttrs(attrs...)
	handler := got.logger.Handler()
	if !handler.Enabled(ctx, level) {
		return true
	}
	_ = handler.Handle(ctx, record)
	return true
}

// Path returns one attempt's log file path, or "" on a nil registry.
func (r *TaskRegistry) Path(taskID int64, attempt int) string {
	if r == nil {
		return ""
	}
	return TaskLogPath(r.baseDir, taskID, attempt)
}

// Remove deletes taskID's whole log directory.
func (r *TaskRegistry) Remove(taskID int64) error {
	if r == nil {
		return nil
	}
	return os.RemoveAll(TaskLogDir(r.baseDir, taskID))
}
