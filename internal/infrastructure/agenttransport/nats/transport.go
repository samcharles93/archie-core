// Package nats implements the agent worker's broker and RPC boundaries over NATS.
// Raw NATS SDK types and wire encoding stay in this infrastructure package.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

// Config contains the broker endpoint and credential for the worker transport.
type Config struct {
	URL   string
	Token string
}

const (
	taskSubscriptionFlushTimeout = 5 * time.Second
)

type sdkSubscription interface {
	Unsubscribe() error
}

// Transport owns the worker's core-NATS connection. Full-task handoff, RPC,
// logs, and events are all request/reply or fire-and-forget core subjects; the
// worker has no JetStream consumer.
type Transport struct {
	conn *natsio.Conn

	subscribe func(string, natsio.MsgHandler) (sdkSubscription, error)
	flush     func(time.Duration) error
}

// Connect establishes the worker's core-NATS connection.
func Connect(ctx context.Context, config Config, log *slog.Logger) (*Transport, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := []natsio.Option{natsio.Name("archie-agent")}
	if config.Token != "" {
		options = append(options, natsio.Token(config.Token))
	}
	conn, err := natsio.Connect(config.URL, options...)
	if err != nil {
		return nil, fmt.Errorf("connect core NATS: %w", err)
	}
	if err := ctx.Err(); err != nil {
		conn.Close()
		return nil, err
	}
	log.Info("worker transport connected", "url", conn.ConnectedUrl())
	return &Transport{
		conn: conn,
		subscribe: func(subject string, handler natsio.MsgHandler) (sdkSubscription, error) {
			return conn.Subscribe(subject, handler)
		},
		flush: conn.FlushTimeout,
	}, nil
}

// Close releases all broker resources.
func (t *Transport) Close() { t.conn.Close() }

// LogPublisher returns the narrow fire-and-forget capability used by system logs.
func (t *Transport) LogPublisher() agentexec.LogPublisher { return t.conn }

// EventPublisher returns the narrow fire-and-forget capability used to ship
// a task's workflow events (stage progress, outcome, parking) back to the
// daemon over NATS.
func (t *Transport) EventPublisher() agentexec.EventPublisher { return t.conn }

// RemoteTrees is the proxied half of workflow.Trees.
type RemoteTrees interface {
	Push(ctx context.Context) error
}

// The top-level forgerpc, storerpc, and worktreerpc packages remain legacy
// infrastructure with an open final destination. This adapter is their single
// worker composition point until that broader migration is approved.

// Forger constructs the identity-scoped forge RPC client.
func (t *Transport) Forger(identity string, timeout time.Duration) workflow.Forger {
	return &forgerpc.Client{Conn: t.conn, Timeout: timeout, Identity: identity}
}

// Store constructs the workflow store RPC client.
func (t *Transport) Store(timeout time.Duration) store.WorkflowStore {
	return &storerpc.Client{Conn: t.conn, Timeout: timeout}
}

// Trees constructs the identity-scoped worktree RPC client.
func (t *Transport) Trees(identity, grant string, timeout time.Duration) RemoteTrees {
	return &worktreerpc.Client{Conn: t.conn, Timeout: timeout, Identity: identity, Grant: grant}
}

// TaskHandler executes one decoded full-task request.
type TaskHandler func(context.Context, taskrun.Request) (*taskrun.Response, error)

// Subscription is a taskrun subscription owned by the worker lifecycle.
type Subscription interface {
	Close() error
}

type subscription struct{ sub sdkSubscription }

func (s subscription) Close() error { return s.sub.Unsubscribe() }

const (
	taskRunSubjectPrefix = "archie.taskrun."
)

// SubjectForTask returns the canonical core-NATS subject for one full-task handoff.
func SubjectForTask(taskID int64) string {
	return fmt.Sprintf("%s%d", taskRunSubjectPrefix, taskID)
}

func taskIDFromSubject(subject string) (int64, error) {
	value, ok := strings.CutPrefix(subject, taskRunSubjectPrefix)
	if !ok || value == "" {
		return 0, fmt.Errorf("invalid taskrun subject %q", subject)
	}
	taskID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || taskID <= 0 || SubjectForTask(taskID) != subject {
		return 0, fmt.Errorf("invalid taskrun subject %q", subject)
	}
	return taskID, nil
}

// SubscribeTasks serves the one task named by the worker's mandatory boot
// metadata. Wire decoding, task correlation, and inbox replies remain here.
func (t *Transport) SubscribeTasks(ctx context.Context, taskID int64, handler TaskHandler, log *slog.Logger) (Subscription, error) {
	callback := func(msg *natsio.Msg) { t.handleTask(ctx, msg, taskID, handler, log) }
	subscribe := t.subscribe
	if subscribe == nil {
		subscribe = func(subject string, handler natsio.MsgHandler) (sdkSubscription, error) {
			return t.conn.Subscribe(subject, handler)
		}
	}
	flush := t.flush
	if flush == nil {
		flush = t.conn.FlushTimeout
	}

	log.Info("taskrun: dedicated per-task subscription", "task", taskID)
	sub, err := subscribe(SubjectForTask(taskID), callback)
	if err != nil {
		return nil, err
	}
	if err := flush(taskSubscriptionFlushTimeout); err != nil {
		if cleanupErr := sub.Unsubscribe(); cleanupErr != nil {
			log.Warn("task subscription cleanup failed after flush failure", "err", cleanupErr)
		}
		return nil, fmt.Errorf("flush task subscription: %w", err)
	}
	return subscription{sub: sub}, nil
}

func (t *Transport) handleTask(
	ctx context.Context,
	msg *natsio.Msg,
	bootTaskID int64,
	handler TaskHandler,
	log *slog.Logger,
) {
	var request taskrun.Request
	if err := json.Unmarshal(msg.Data, &request); err != nil {
		log.Error("taskrun decode failed", "err", err)
		t.respondTask(msg, nil, fmt.Errorf("decode taskrun request: %w", err), log)
		return
	}
	if err := validateTaskRequest(msg.Subject, request, bootTaskID); err != nil {
		log.Error("taskrun validation failed", "subject", msg.Subject, "err", err)
		t.respondTask(msg, nil, fmt.Errorf("validate taskrun request: %w", err), log)
		return
	}
	response, err := handler(ctx, request)
	t.respondTask(msg, response, err, log)
}

func validateTaskRequest(subject string, request taskrun.Request, bootTaskID int64) error {
	subjectTaskID, err := taskIDFromSubject(subject)
	if err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if request.Task.ID != subjectTaskID {
		return fmt.Errorf("task ID %d does not match subject task ID %d", request.Task.ID, subjectTaskID)
	}
	if subjectTaskID != bootTaskID {
		return fmt.Errorf("task ID %d does not match boot task ID %d", subjectTaskID, bootTaskID)
	}
	return nil
}

func (*Transport) respondTask(msg *natsio.Msg, response *taskrun.Response, runErr error, log *slog.Logger) {
	if response == nil {
		response = &taskrun.Response{}
	}
	if runErr != nil {
		response.Error = runErr.Error()
	}
	data, err := json.Marshal(response)
	if err != nil {
		log.Error("taskrun encode response failed", "err", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		log.Error("taskrun respond failed", "err", err)
	}
}
