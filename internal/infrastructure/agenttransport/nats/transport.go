// Package nats implements the agent worker's broker and RPC boundaries over NATS.
// Raw NATS SDK types and wire encoding stay in this infrastructure package.
package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	busnats "github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

// Config contains the process and deployment inputs for the worker transport.
// Stream subjects, filtering, and delivery timing are transport invariants.
type Config struct {
	URL          string
	Token        string
	ConsumerName string
}

const (
	stagePollTimeout             = 5 * time.Second
	stageAckWait                 = 30 * time.Minute
	taskSubscriptionFlushTimeout = 5 * time.Second
)

// ErrCoreConnectionUnavailable identifies failure to obtain the core-NATS
// connection after the durable bus connected successfully.
var ErrCoreConnectionUnavailable = errors.New("core NATS connection unavailable")

type sdkSubscription interface {
	Unsubscribe() error
}

// Transport owns the worker's durable event bus and its shared core-NATS connection.
type Transport struct {
	bus  *busnats.Client
	conn *natsio.Conn

	subscribe      func(string, natsio.MsgHandler) (sdkSubscription, error)
	queueSubscribe func(string, string, natsio.MsgHandler) (sdkSubscription, error)
	flush          func(time.Duration) error
}

type busConnector func(context.Context, busnats.Config, *slog.Logger) (*busnats.Client, error)

// Connect establishes both worker transports over one NATS connection.
func Connect(ctx context.Context, config Config, log *slog.Logger) (*Transport, error) {
	return connect(ctx, config, log, busnats.Connect)
}

func connect(ctx context.Context, config Config, log *slog.Logger, connectBus busConnector) (*Transport, error) {
	bus, err := connectBus(ctx, busnats.Config{
		URL:           config.URL,
		Token:         config.Token,
		Subjects:      []string{workintake.SubjectTaskWildcard, agentexec.SubjectAgentWildcard},
		ConsumerName:  config.ConsumerName,
		FilterSubject: agentexec.SubjectAgentWildcard,
		PollTimeout:   stagePollTimeout,
		AckWait:       stageAckWait,
	}, log)
	if err != nil {
		return nil, err
	}
	return transportFromBus(bus)
}

func transportFromBus(bus *busnats.Client) (*Transport, error) {
	conn, err := bus.CoreConn()
	if err != nil {
		bus.Close()
		return nil, fmt.Errorf("%w: %w", ErrCoreConnectionUnavailable, err)
	}
	return &Transport{
		bus:  bus,
		conn: conn,
		subscribe: func(subject string, handler natsio.MsgHandler) (sdkSubscription, error) {
			return conn.Subscribe(subject, handler)
		},
		queueSubscribe: func(subject, queue string, handler natsio.MsgHandler) (sdkSubscription, error) {
			return conn.QueueSubscribe(subject, queue, handler)
		},
		flush: conn.FlushTimeout,
	}, nil
}

// Close releases all broker resources.
func (t *Transport) Close() { t.bus.Close() }

type stageMessage struct {
	wire         eventbus.Message
	request      agentexec.AgentRequestMessage
	requestErr   error
	replyAddress string
	encode       func(any) ([]byte, error)
	respond      func(context.Context, string, []byte) error
}

var _ agentexec.StageMessage = (*stageMessage)(nil)

// FetchStage receives and decodes the next durable single-stage request while
// retaining its reply inbox and acknowledgement controls inside infrastructure.
func (t *Transport) FetchStage(ctx context.Context) (agentexec.StageMessage, error) {
	wire, err := t.bus.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	return newStageMessage(wire, t.bus.Respond), nil
}

func newStageMessage(wire eventbus.Message, respond func(context.Context, string, []byte) error) agentexec.StageMessage {
	message := &stageMessage{wire: wire, encode: json.Marshal, respond: respond}
	if err := json.Unmarshal(wire.Data(), &message.request); err != nil {
		message.requestErr = fmt.Errorf("decode request: %w", err)
		return message
	}
	var err error
	message.replyAddress, err = wire.ReplyAddress()
	if err != nil {
		message.requestErr = fmt.Errorf("stage request on %s: %w", wire.Subject(), err)
	}
	return message
}

func (m *stageMessage) Request() (agentexec.AgentRequestMessage, error) {
	return m.request, m.requestErr
}

func (m *stageMessage) Respond(ctx context.Context, response *agentexec.AgentResponseEnvelope) error {
	data, err := m.encode(response)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := m.respond(ctx, m.replyAddress, data); err != nil {
		return fmt.Errorf("publish response: %w", err)
	}
	return nil
}

func (m *stageMessage) Ack() error { return m.wire.Ack() }
func (m *stageMessage) Nak() error { return m.wire.Nak() }

func (m *stageMessage) LogAttributes() []any {
	if m.replyAddress == "" {
		return nil
	}
	return []any{"reply_to", m.replyAddress}
}

// LogPublisher returns the narrow fire-and-forget capability used by system logs.
func (t *Transport) LogPublisher() agentexec.LogPublisher { return t.conn }

// EventPublisher returns the narrow fire-and-forget capability used to ship
// a task's workflow events (stage progress, outcome, parking) back to the
// daemon over NATS.
func (t *Transport) EventPublisher() agentexec.EventPublisher { return t.conn }

// RemoteTrees is the proxied half of workflow.Trees.
type RemoteTrees interface {
	Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error)
	Push(ctx context.Context, owner, repo string, issue int, branch string) error
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
func (t *Transport) Trees(identity string, timeout time.Duration) RemoteTrees {
	return &worktreerpc.Client{Conn: t.conn, Timeout: timeout, Identity: identity}
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
	taskRunSubjectPrefix   = "archie.taskrun."
	taskRunSubjectWildcard = taskRunSubjectPrefix + ">"
	taskRunQueueGroup      = "archie-taskrun-workers"
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

// SubscribeTasks selects a dedicated task subject when a boot ID is present,
// otherwise the shared worker queue. Wire decoding and inbox replies remain here.
func (t *Transport) SubscribeTasks(ctx context.Context, taskID int64, dedicated bool, handler TaskHandler, log *slog.Logger) (Subscription, error) {
	callback := func(msg *natsio.Msg) { t.handleTask(ctx, msg, taskID, dedicated, handler, log) }
	subscribe := t.subscribe
	if subscribe == nil {
		subscribe = func(subject string, handler natsio.MsgHandler) (sdkSubscription, error) {
			return t.conn.Subscribe(subject, handler)
		}
	}
	queueSubscribe := t.queueSubscribe
	if queueSubscribe == nil {
		queueSubscribe = func(subject, queue string, handler natsio.MsgHandler) (sdkSubscription, error) {
			return t.conn.QueueSubscribe(subject, queue, handler)
		}
	}
	flush := t.flush
	if flush == nil {
		flush = t.conn.FlushTimeout
	}

	var (
		sub sdkSubscription
		err error
	)
	if dedicated {
		log.Info("taskrun: dedicated per-task subscription", "task", taskID)
		sub, err = subscribe(SubjectForTask(taskID), callback)
	} else {
		log.Info("taskrun: shared queue-group subscription (no .git/task.json found)")
		sub, err = queueSubscribe(taskRunSubjectWildcard, taskRunQueueGroup, callback)
	}
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
	dedicated bool,
	handler TaskHandler,
	log *slog.Logger,
) {
	var request taskrun.Request
	if err := json.Unmarshal(msg.Data, &request); err != nil {
		log.Error("taskrun decode failed", "err", err)
		t.respondTask(msg, nil, fmt.Errorf("decode taskrun request: %w", err), log)
		return
	}
	if err := validateTaskRequest(msg.Subject, request, bootTaskID, dedicated); err != nil {
		log.Error("taskrun validation failed", "subject", msg.Subject, "err", err)
		t.respondTask(msg, nil, fmt.Errorf("validate taskrun request: %w", err), log)
		return
	}
	response, err := handler(ctx, request)
	t.respondTask(msg, response, err, log)
}

func validateTaskRequest(subject string, request taskrun.Request, bootTaskID int64, dedicated bool) error {
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
	if dedicated && subjectTaskID != bootTaskID {
		return fmt.Errorf("task ID %d does not match dedicated boot task ID %d", subjectTaskID, bootTaskID)
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
