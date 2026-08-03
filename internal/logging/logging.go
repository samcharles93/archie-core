// Package logging builds archied's logger.
//
// archied previously wrote only to stderr, so under systemd journald held the
// sole copy and running by hand left nothing at all. A file sink gives a
// durable record independent of the supervisor, and is what lets the dashboard
// show anything from before it connected.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Options configures the logger. The zero value is a stderr-only JSON logger
// at info level, which is what archied did before file logging existed.
type Options struct {
	// File is the log file path. Empty disables file logging.
	File string
	// MaxSizeMB rotates the file once it exceeds this size. Zero selects
	// DefaultMaxSizeMB.
	MaxSizeMB int
	// Keep is how many rotated files to retain. Zero selects DefaultKeep.
	Keep int
	// Level is "debug", "info", "warn" or "error". Empty selects info.
	Level string
	// Stderr also writes to stderr. Kept on by default so journald and an
	// interactive terminal still see output when a file is configured.
	Stderr bool
}

// Rotation defaults. Sized so a busy daemon keeps roughly a week of history
// without an unbounded file.
const (
	DefaultMaxSizeMB = 20
	DefaultKeep      = 5
)

// New builds a logger and returns it with a closer for any file sink. The
// closer is a no-op when only stderr is in use.
//
// A file that cannot be opened is reported but never fatal: losing the
// durable copy is worse than losing the daemon, but not by enough to refuse
// to start.
func New(opts Options) (*slog.Logger, io.Closer, error) {
	level := parseLevel(opts.Level)
	handlerOpts := &slog.HandlerOptions{Level: level}

	if opts.File == "" {
		return slog.New(slog.NewJSONHandler(os.Stderr, handlerOpts)), noopCloser{}, nil
	}

	w, err := newRotatingFile(opts.File, opts.MaxSizeMB, opts.Keep)
	if err != nil {
		return slog.New(slog.NewJSONHandler(os.Stderr, handlerOpts)), noopCloser{},
			fmt.Errorf("logging: open %s: %w", opts.File, err)
	}

	var sink io.Writer = w
	if opts.Stderr {
		sink = io.MultiWriter(os.Stderr, w)
	}
	return slog.New(slog.NewJSONHandler(sink, handlerOpts)), w, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// rotatingFile is a size-rotating io.WriteCloser.
//
// Deliberately hand-rolled rather than pulling in a rotation dependency: the
// requirement is one file, a size cap and a retention count, and that is
// materially less code than auditing and tracking a third-party module.
type rotatingFile struct {
	path    string
	maxSize int64
	keep    int

	mu   sync.Mutex
	f    *os.File
	size int64
}

func newRotatingFile(path string, maxSizeMB, keep int) (*rotatingFile, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = DefaultMaxSizeMB
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	r := &rotatingFile{
		path:    path,
		maxSize: int64(maxSizeMB) * 1024 * 1024,
		keep:    keep,
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	r.f, r.size = f, info.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size+int64(len(p)) > r.maxSize {
		// A failed rotation must not lose the line: fall through and keep
		// writing to the current file rather than dropping it.
		_ = r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate renames the live file to .1, shifting existing generations down and
// discarding anything past keep.
func (r *rotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	// Oldest first, so each rename lands on a free slot.
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, r.keep))
	for i := r.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	_ = os.Rename(r.path, r.path+".1")
	return r.open()
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	return r.f.Close()
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }
