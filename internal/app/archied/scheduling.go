package archied

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/crondelivery"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronevents"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

// schedulingConfig translates external input into the domain-owned engine
// configuration and connects the selected event infrastructure.
func schedulingConfig(input configuration.SchedulingInput, bus *events.Bus) (scheduling.EngineConfig, error) {
	cfg := scheduling.DefaultEngineConfig()
	if input.Interval != 0 {
		cfg.Interval = input.Interval.Std()
	}
	if input.MaxParallel != 0 {
		cfg.MaxParallel = input.MaxParallel
	}
	if input.JobTimeout != 0 {
		cfg.JobTimeout = input.JobTimeout.Std()
	}

	switch input.EventsSink {
	case "", configuration.EventsSinkNone:
	case configuration.EventsSinkBus:
		cfg.Events = cronevents.New(bus)
	default:
		return scheduling.EngineConfig{}, fmt.Errorf("%w: scheduling.events_sink %q (want %q or %q)",
			configuration.ErrInvalidInput, input.EventsSink, configuration.EventsSinkBus, configuration.EventsSinkNone)
	}
	if err := cfg.Validate(); err != nil {
		return scheduling.EngineConfig{}, fmt.Errorf("%w: %w", configuration.ErrInvalidInput, err)
	}
	return cfg, nil
}

// setupScheduling builds the cron/scheduling composition site: a job store,
// a delivery router over the kind runners this composition root can back
// with a real production dependency, and the ticker engine itself. It does
// not start the engine -- startServices does that alongside every other
// subsystem, and the corresponding Stop is registered here via addCleanup so
// shutdown ordering matches every other daemon-lifecycle subsystem.
//
// Only the "workflow" kind is wired: it rides gateway.TaskCreator, the same
// non-forge-backed task creation path chat's /spawn uses, which is the one
// production capability here that does not require inventing a channel
// courier or a synthetic forge owner/repo. A deployment with no chat task
// creator configured (no [chat] identity profiles) leaves the engine
// unstarted rather than running with no reachable job kind.
func (b *boot) setupScheduling() error {
	cfg, err := schedulingConfig(b.doc.Scheduling, b.bus)
	if err != nil {
		return fmt.Errorf("scheduling: %w", err)
	}

	if b.chatTasks == nil {
		b.log.Warn("scheduling: no chat task creator configured; ticker engine not started")
		return nil
	}

	storePath := filepath.Join(filepath.Dir(b.cfg.DBPath), "cron", "jobs.json")
	store, err := cronstore.Open(storePath)
	if err != nil {
		return fmt.Errorf("scheduling: open job store: %w", err)
	}
	b.addCleanup(func() {
		if err := store.Close(); err != nil {
			b.log.Error("scheduling store close", "err", err)
		}
	})

	workflowRunner, err := crondelivery.NewWorkflowTask(store, spawnTaskSubmitter{creator: b.chatTasks, identity: b.defaultChatIdentity})
	if err != nil {
		return fmt.Errorf("scheduling: build workflow runner: %w", err)
	}
	router, err := crondelivery.NewRouter(store, map[string]scheduling.Runner{cronstore.KindWorkflow: workflowRunner}, cfg.Events)
	if err != nil {
		return fmt.Errorf("scheduling: build router: %w", err)
	}
	engine, err := scheduling.NewEngine(store, router, cfg)
	if err != nil {
		return fmt.Errorf("scheduling: build engine: %w", err)
	}

	b.schedulingEngine = engine
	b.addCleanup(shutdownSchedulingEngine(engine, b.log))
	return nil
}

// shutdownSchedulingEngine returns a cleanup that stops the ticker engine.
// The stop path derives its own timeout budget from Background rather than
// the boot context, which is already cancelled by the time shutdown runs --
// the same reasoning as shutdownCuratorRuntime.
//
//nolint:contextcheck
func shutdownSchedulingEngine(e *scheduling.Engine, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.Stop(stopCtx); err != nil {
			log.Error("scheduling engine shutdown", "err", err)
		}
	}
}

// spawnTaskSubmitter adapts gateway.TaskCreator to crondelivery.TaskSubmitter.
// The job's own id is only a dispatch key to the ticker engine, not an archie
// identity, so it is dropped rather than threaded into the spawn request;
// identity is fixed at construction to the daemon's default chat identity.
type spawnTaskSubmitter struct {
	creator  gateway.TaskCreator
	identity string
}

func (s spawnTaskSubmitter) Submit(ctx context.Context, _, title, body string) error {
	_, err := s.creator.CreateTask(ctx, gateway.SpawnRequest{Title: title, Body: body, Identity: s.identity})
	return err
}
