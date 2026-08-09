package logging

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
)

// TaskLogPath returns the log file path for one task attempt under baseDir
// (typically <state_dir>/logs/tasks). Each task gets its own subdirectory so
// an attempt's rotated generations (attempt-N.jsonl.1, .2, ...) stay grouped
// with that task rather than interleaved with every other task's.
func TaskLogPath(baseDir string, taskID int64, attempt int) string {
	return filepath.Join(baseDir, strconv.FormatInt(taskID, 10), fmt.Sprintf("attempt-%d.jsonl", attempt))
}

// TaskSinkOptions configures a per-task log sink. The zero value selects the
// same defaults as the daemon-wide log: DefaultMaxSizeMB, DefaultKeep, and
// info level.
type TaskSinkOptions struct {
	// MaxSizeMB rotates the file once it exceeds this size. Zero selects
	// DefaultMaxSizeMB.
	MaxSizeMB int
	// Keep is how many rotated files to retain. Zero selects DefaultKeep.
	Keep int
	// Level is "debug", "info", "warn" or "error". Empty selects info.
	Level string
}

// TaskSink is a per-task, per-attempt log destination. It writes the same
// on-disk JSONL shape as the daemon-wide log (see Entry, Tail, Query,
// Components), so a task's history is readable with the same reader --
// there is deliberately no second log format to keep in sync.
type TaskSink struct {
	logger *slog.Logger
	file   *rotatingFile
}

// NewTaskSink opens the JSONL sink for one task attempt under baseDir,
// creating its directory if needed, with the same size-based rotation as
// the daemon-wide log.
//
// Unlike New (the daemon-wide logger), a task sink that fails to open
// returns the error rather than falling back to stderr: New degrades
// because losing the daemon over a broken log path is worse than losing the
// durable copy, but there is no equivalent daemon-level fallback for one
// task's log -- the caller (a workflow stage, or the daemon's NATS log
// consumer) is better placed to decide what a missing sink means for that
// task than this package is.
func NewTaskSink(baseDir string, taskID int64, attempt int, opts TaskSinkOptions) (*TaskSink, error) {
	path := TaskLogPath(baseDir, taskID, attempt)
	w, err := newRotatingFile(path, opts.MaxSizeMB, opts.Keep)
	if err != nil {
		return nil, fmt.Errorf("logging: open task sink %s: %w", path, err)
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(opts.Level)})
	return &TaskSink{logger: slog.New(handler), file: w}, nil
}

// Logger returns the slog.Logger writing to this sink.
func (s *TaskSink) Logger() *slog.Logger { return s.logger }

// Close closes the underlying file.
func (s *TaskSink) Close() error { return s.file.Close() }
