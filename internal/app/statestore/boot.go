package statestore

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/sdnotify"
	"github.com/samcharles93/archie-core/internal/secret"
)

// server is the State Store process: the one owner of the task and
// control-plane tables.
type server struct {
	cfg     config.Config
	log     *slog.Logger
	logFeed *logging.Feed
	// stderrLog keeps an offline command off cfg.Log.File: a diagnosis must
	// not create, append to or rotate the deployment's log.
	stderrLog bool

	secrets  *secret.Registry
	taskLogs *logging.TaskRegistry
	pg       *pgxpool.Pool
	st       storecontract.TaskStore
	eda      eventCaptureStore

	cleanups []func()
}

func newServer() *server {
	return &server{log: slog.New(slog.NewJSONHandler(os.Stderr, nil))}
}

func (b *server) addCleanup(fn func()) { b.cleanups = append(b.cleanups, fn) }

func (b *server) cleanup() {
	for _, fn := range slices.Backward(b.cleanups) {
		fn()
	}
}

func (b *server) loadConfig(_ context.Context, cfgPath, overlayPath string) error {
	_, doc, err := servicekit.Resolve(b.log, cfgPath, overlayPath)
	if err != nil {
		return err
	}
	b.cfg = doc.Config
	if b.stderrLog {
		return nil
	}
	logs := servicekit.Logging(b.cfg, "state-store")
	b.log, b.taskLogs, b.logFeed = logs.Log, logs.TaskLogs, logs.Feed
	b.addCleanup(func() { _ = logs.Closer.Close() })
	return nil
}

func (b *server) serveHealth(ctx context.Context, addr string, mux http.Handler, label string) error {
	stop, err := servicekit.ServeHealth(ctx, addr, mux, label, b.log)
	if err != nil {
		return err
	}
	b.addCleanup(stop)
	return nil
}

func (b *server) announceReady() { sdnotify.Ready(b.log) }
