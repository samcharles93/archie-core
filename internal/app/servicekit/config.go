package servicekit

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/logging"
)

// Resolve reads the file config. A failure is printed to stderr because the
// file log destination is itself configuration that has not been read yet.
func Resolve(log *slog.Logger, cfgPath, overlayPath string) (*configuration.Loader, *configuration.Document, error) {
	loader := configuration.New(log)
	doc, err := loader.Resolve(cfgPath, overlayPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, err
	}
	// Unknown TOML keys are otherwise discarded silently, so a typo would
	// quietly do nothing.
	if len(doc.UnknownKeys) > 0 {
		log.Warn("config file has unrecognised keys; check for typos", "keys", doc.UnknownKeys)
	}
	return loader, doc, nil
}

// Logs is a service's configured log and the task log registry beside it.
type Logs struct {
	Log      *slog.Logger
	Feed     *logging.Feed
	TaskLogs *logging.TaskRegistry
	Closer   io.Closer
}

// Logging opens the configured log for component. A file log that cannot be
// opened is reported and logging continues on stderr.
func Logging(cfg config.Config, component string) Logs {
	feed := logging.NewFeed(1000)
	taskLogs := logging.NewTaskRegistry(filepath.Join(cfg.StateDir, "logs", "tasks"), feed, logging.TaskSinkOptions{})
	fileLog, closer, err := logging.New(logging.Options{
		File:      cfg.Log.File,
		MaxSizeMB: cfg.Log.MaxSizeMB,
		Keep:      cfg.Log.Keep,
		Level:     cfg.Log.Level,
		Stderr:    !cfg.Log.Quiet,
		Feed:      feed,
	})
	log := fileLog.With("component", component)
	if err != nil {
		log.Error("file logging disabled", "err", err)
	} else if cfg.Log.File != "" {
		log.Info("logging to file", "path", cfg.Log.File)
	}
	return Logs{Log: log, Feed: feed, TaskLogs: taskLogs, Closer: closer}
}

// Diagnostics captures a service's stderr log for its recent-log contract.
func Diagnostics(log *slog.Logger, service string) (*slog.Logger, *logging.Feed) {
	feed := logging.NewFeed(1000)
	return slog.New(logging.NewFeedHandler(log.Handler(), feed)).With("service", service), feed
}
