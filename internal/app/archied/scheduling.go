package archied

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/crondelivery"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronevents"
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
// The "chat" kind delivers through the Messaging Service. The "workflow" kind
// rides gateway.TaskCreator, the path chat's /spawn uses, so it is wired only
// when [chat] identity profiles configure one.
func (b *boot) setupScheduling() error {
	cfg, err := schedulingConfig(b.doc.Scheduling, b.bus)
	if err != nil {
		return fmt.Errorf("scheduling: %w", err)
	}

	store := scheduleResourceStore{client: b.controlPlane}

	chatRunner, err := crondelivery.NewChatCourier(store, func(ctx context.Context, target scheduling.Target, text string) error {
		return b.messaging.Deliver(ctx, target.Channel, target.ChatID, text)
	})
	if err != nil {
		return fmt.Errorf("scheduling: build chat runner: %w", err)
	}
	runners := map[string]scheduling.Runner{scheduling.KindChat: chatRunner}
	if b.chatTasks != nil {
		workflowRunner, err := crondelivery.NewWorkflowTask(store, spawnTaskSubmitter{creator: b.chatTasks, identity: b.defaultChatIdentity})
		if err != nil {
			return fmt.Errorf("scheduling: build workflow runner: %w", err)
		}
		runners[scheduling.KindWorkflow] = workflowRunner
	}
	router, err := crondelivery.NewRouter(store, runners, cfg.Events)
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

type scheduleResourceStore struct {
	client *controlplane.Client
}

// The schedules document behind the control plane is the production
// RouterStore: it resolves a job id to its spec and records a completed run.
// The assertions this replaces used to pin the retired file-backed store;
// crondelivery's contract comments name this type as the implementation.
var (
	_ crondelivery.SpecLookup  = scheduleResourceStore{}
	_ crondelivery.RunRecorder = scheduleResourceStore{}
	_ crondelivery.RouterStore = scheduleResourceStore{}
)

func (s scheduleResourceStore) Get(ctx context.Context, id string) (scheduling.JobSpec, bool, error) {
	jobs, _, err := s.client.Schedules(ctx)
	if err != nil {
		return scheduling.JobSpec{}, false, err
	}
	for _, job := range jobs {
		if job.ID == id {
			return job, true, nil
		}
	}
	return scheduling.JobSpec{}, false, nil
}

func (s scheduleResourceStore) Due(ctx context.Context, now time.Time) ([]scheduling.Job, error) {
	jobs, _, err := s.client.Schedules(ctx)
	if err != nil {
		return nil, err
	}
	due := make([]scheduling.Job, 0, len(jobs))
	for _, job := range jobs {
		if !job.NextRun.After(now) {
			due = append(due, scheduling.Job{ID: job.ID, Pool: scheduling.Pool(job.Pool), Detail: job.Detail})
		}
	}
	return due, nil
}

func (s scheduleResourceStore) MarkRun(ctx context.Context, id string, runAt time.Time) error {
	for range 4 {
		jobs, version, err := s.client.Schedules(ctx)
		if err != nil {
			return err
		}
		found := false
		for index := range jobs {
			if jobs[index].ID != id {
				continue
			}
			found = true
			if jobs[index].Schedule.Resolved().Kind == scheduling.ScheduleOnce {
				jobs = append(jobs[:index], jobs[index+1:]...)
				break
			}
			next, nextErr := jobs[index].Schedule.NextRun(runAt)
			if nextErr != nil {
				return nextErr
			}
			at := runAt.UTC()
			jobs[index].LastRun = &at
			jobs[index].NextRun = next.UTC()
			break
		}
		if !found {
			return fmt.Errorf("%w: id %q", scheduling.ErrJobNotFound, id)
		}
		_, err = s.client.ReplaceSchedules(ctx, jobs, version, "system:scheduler", "scheduler", fmt.Sprintf("schedule-run:%s:%d", id, runAt.UnixNano()))
		if err == nil {
			return nil
		}
		if !errors.Is(err, controlplane.ErrVersionConflict) {
			return err
		}
	}
	return controlplane.ErrVersionConflict
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
