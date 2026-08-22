// Package agentworker composes and runs Archie's NATS-connected agent worker.
package agentworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/agentboot"
	"github.com/samcharles93/archie-core/internal/infrastructure/agentgit"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskrun"
)

const rpcTimeout = 60 * time.Second

// StartupError reports which worker startup operation failed. The application
// logs the operation-specific failure once; the command only maps it to exit 1.
type StartupError struct {
	Operation string
	Err       error
}

func (e *StartupError) Error() string { return e.Operation + ": " + e.Err.Error() }
func (e *StartupError) Unwrap() error { return e.Err }

// Settings are process-derived inputs for one worker runtime.
type Settings struct {
	NATSURL   string
	NATSToken string
	Consumer  string
	WorkDir   string
}

type workerTransport interface {
	agentexec.StageConsumer
	Close()
	LogPublisher() agentexec.LogPublisher
	EventPublisher() agentexec.EventPublisher
	SubscribeTasks(context.Context, int64, bool, agentnats.TaskHandler, *slog.Logger) (agentnats.Subscription, error)
	Forger(string, time.Duration) workflow.Forger
	Store(time.Duration) store.WorkflowStore
	Trees(string, string, time.Duration) agentnats.RemoteTrees
}

type workerDependencies struct {
	connect  func(context.Context, agentnats.Config, *slog.Logger) (workerTransport, error)
	markSafe func(context.Context, string, *slog.Logger) bool
	bootID   func(string) (int64, bool, error)
	runLoop  func(context.Context, stageBus, *slog.Logger) int
}

func productionWorkerDependencies() workerDependencies {
	return workerDependencies{
		connect: func(ctx context.Context, config agentnats.Config, log *slog.Logger) (workerTransport, error) {
			return agentnats.Connect(ctx, config, log)
		},
		markSafe: agentgit.MarkSafe,
		bootID:   agentboot.TaskID,
		runLoop:  runMainLoop,
	}
}

// Run starts the worker, serves full-task and single-stage requests, and
// returns after ctx cancellation or a startup failure.
func Run(ctx context.Context, settings Settings, log *slog.Logger) error {
	return run(ctx, settings, log, productionWorkerDependencies())
}

func run(ctx context.Context, settings Settings, log *slog.Logger, dependencies workerDependencies) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	workDir := settings.WorkDir
	if workDir == "" {
		workDir = storage.WorktreeMountDir
	}

	dependencies.markSafe(ctx, workDir, log)

	transport, err := dependencies.connect(ctx, agentnats.Config{
		URL:          settings.NATSURL,
		Token:        settings.NATSToken,
		ConsumerName: settings.Consumer,
	}, log)
	if err != nil {
		operation := "nats connect failed"
		if errors.Is(err, agentnats.ErrCoreConnectionUnavailable) {
			operation = "nats connection unavailable"
		}
		log.Error(operation, "err", err)
		return &StartupError{Operation: operation, Err: err}
	}
	defer transport.Close()

	taskID, dedicated, err := dependencies.bootID(workDir)
	if err != nil {
		log.Error("task boot failed", "err", err)
		return &StartupError{Operation: "task boot failed", Err: err}
	}
	if dedicated {
		log = slog.New(agentexec.NewSystemLogHandler(log.Handler(), transport.LogPublisher(), taskID))
		log.Info("system log publisher attached", "task", taskID)
	}

	subscription, err := transport.SubscribeTasks(ctx, taskID, dedicated, func(ctx context.Context, request taskrun.Request) (*taskrun.Response, error) {
		return executeTaskRequest(ctx, request, transport, workDir, log)
	}, log)
	if err != nil {
		log.Error("taskrun subscribe failed", "err", err)
		return &StartupError{Operation: "taskrun subscribe failed", Err: err}
	}
	defer func() {
		if err := subscription.Close(); err != nil {
			log.Warn("task run unsubscribe failed", "err", err)
		}
	}()

	log.Info("archie-agent ready", "nats", settings.NATSURL, "consumer", settings.Consumer)
	dependencies.runLoop(ctx, transport, log)
	return nil
}

type taskServiceTransport interface {
	Forger(string, time.Duration) workflow.Forger
	Store(time.Duration) store.WorkflowStore
	Trees(string, string, time.Duration) agentnats.RemoteTrees
	EventPublisher() agentexec.EventPublisher
}

func executeTaskRequest(ctx context.Context, request taskrun.Request, transport taskServiceTransport, workDir string, log *slog.Logger) (*taskrun.Response, error) {
	log.Info("running task", "task", request.Task.ID, "repo", request.Repo.FullName(), "issue", request.Task.IssueNumber)
	dependencies := taskDependencies{
		forge:  transport.Forger(request.Task.Identity, rpcTimeout),
		store:  transport.Store(rpcTimeout),
		trees:  transport.Trees(request.Task.Identity, request.WorktreeGrant, rpcTimeout),
		events: transport.EventPublisher(),
	}
	response, err := runTask(ctx, request, dependencies, agentexec.DefaultRunnerFactory, workDir, log)
	if err != nil {
		log.Error("task run failed", "task", request.Task.ID, "err", err)
		return nil, err
	}
	log.Info("task run complete", "task", request.Task.ID, "status", response.Status)
	return response, nil
}
