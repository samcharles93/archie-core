package nats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	busnats "github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

func testTransport(t *testing.T) (*Transport, *natsio.Conn) {
	t.Helper()
	srv := natstest.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	conn, err := natsio.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	return &Transport{conn: conn}, conn
}

func TestTaskRunSubjectOwnsCanonicalNATSTopology(t *testing.T) {
	if got := SubjectForTask(42); got != "archie.taskrun.42" {
		t.Fatalf("SubjectForTask(42) = %q", got)
	}

	for _, test := range []struct {
		name    string
		subject string
		wantID  int64
		wantErr bool
	}{
		{name: "canonical", subject: "archie.taskrun.42", wantID: 42},
		{name: "missing prefix", subject: "taskrun.42", wantErr: true},
		{name: "missing ID", subject: "archie.taskrun.", wantErr: true},
		{name: "nondecimal", subject: "archie.taskrun.any", wantErr: true},
		{name: "zero", subject: "archie.taskrun.0", wantErr: true},
		{name: "negative", subject: "archie.taskrun.-1", wantErr: true},
		{name: "noncanonical leading zero", subject: "archie.taskrun.042", wantErr: true},
		{name: "extra token", subject: "archie.taskrun.42.extra", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := taskIDFromSubject(test.subject)
			if test.wantErr {
				if err == nil {
					t.Fatalf("taskIDFromSubject(%q) = %d, want error", test.subject, got)
				}
				return
			}
			if err != nil || got != test.wantID {
				t.Fatalf("taskIDFromSubject(%q) = (%d, %v), want (%d, nil)", test.subject, got, err, test.wantID)
			}
		})
	}
}

func TestSubscribeTasksPreservesDedicatedAndSharedSubjects(t *testing.T) {
	tests := []struct {
		name      string
		taskID    int64
		dedicated bool
		subject   string
		requestID int64
	}{
		{name: "dedicated", taskID: 42, dedicated: true, subject: SubjectForTask(42), requestID: 42},
		{name: "shared queue", subject: SubjectForTask(7), requestID: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport, conn := testTransport(t)
			calls := 0
			subscription, err := transport.SubscribeTasks(t.Context(), test.taskID, test.dedicated, func(_ context.Context, request taskrun.Request) (*taskrun.Response, error) {
				calls++
				if request.Task.ID != test.requestID {
					t.Fatalf("task ID = %d, want %d", request.Task.ID, test.requestID)
				}
				return &taskrun.Response{Status: "done"}, nil
			}, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = subscription.Close() })

			payload, err := json.Marshal(taskrun.Request{Task: &store.Task{ID: test.requestID}, WorktreeGrant: "grant"})
			if err != nil {
				t.Fatal(err)
			}
			message, err := conn.Request(test.subject, payload, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var response taskrun.Response
			if err := json.Unmarshal(message.Data, &response); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || response.Status != "done" {
				t.Fatalf("handler/response = (%d, %q), want (1, done)", calls, response.Status)
			}
		})
	}
}

func TestSubscribeTasksOwnsWireErrors(t *testing.T) {
	transport, conn := testTransport(t)
	handlerCalls := 0
	subscription, err := transport.SubscribeTasks(t.Context(), 9, true, func(context.Context, taskrun.Request) (*taskrun.Response, error) {
		handlerCalls++
		return nil, errors.New("run failed")
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })

	for _, test := range []struct {
		name             string
		payload          []byte
		want             string
		wantHandlerCalls int
	}{
		{name: "decode", payload: []byte("not json"), want: "decode taskrun request:"},
		{name: "missing task", payload: []byte(`{}`), want: "validate taskrun request: task is required"},
		{name: "null task", payload: []byte(`{"task":null}`), want: "validate taskrun request: task is required"},
		{name: "nonpositive task ID", payload: []byte(`{"task":{"id":0}}`), want: "validate taskrun request: task ID must be positive, got 0"},
		{name: "missing worktree grant", payload: []byte(`{"task":{"id":9}}`), want: "validate taskrun request: worktree grant is required"},
		{name: "payload subject mismatch", payload: []byte(`{"task":{"id":8},"worktree_grant":"grant"}`), want: "validate taskrun request: task ID 8 does not match subject task ID 9"},
		{name: "handler", payload: []byte(`{"task":{"id":9},"worktree_grant":"grant"}`), want: "run failed", wantHandlerCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := handlerCalls
			message, err := conn.Request(SubjectForTask(9), test.payload, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var response taskrun.Response
			if err := json.Unmarshal(message.Data, &response); err != nil {
				t.Fatal(err)
			}
			if response.Error == "" || !strings.Contains(response.Error, test.want) {
				t.Fatalf("response error = %q, want containing %q", response.Error, test.want)
			}
			if got := handlerCalls - before; got != test.wantHandlerCalls {
				t.Fatalf("handler calls = %d, want %d", got, test.wantHandlerCalls)
			}
		})
	}
}

func TestHandleTaskRejectsSubjectCorrelationErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		subject    string
		bootTaskID int64
		dedicated  bool
		requestID  int64
		want       string
	}{
		{
			name:      "malformed subject",
			subject:   "archie.taskrun.invalid",
			requestID: 7,
			want:      `validate taskrun request: invalid taskrun subject "archie.taskrun.invalid"`,
		},
		{
			name:       "dedicated boot mismatch",
			subject:    SubjectForTask(9),
			bootTaskID: 42,
			dedicated:  true,
			requestID:  9,
			want:       "validate taskrun request: task ID 9 does not match dedicated boot task ID 42",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport, conn := testTransport(t)
			handlerCalls := 0
			subscription, err := conn.Subscribe(test.subject, func(message *natsio.Msg) {
				transport.handleTask(t.Context(), message, test.bootTaskID, test.dedicated, func(context.Context, taskrun.Request) (*taskrun.Response, error) {
					handlerCalls++
					return &taskrun.Response{Status: "unexpected"}, nil
				}, slog.New(slog.DiscardHandler))
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = subscription.Unsubscribe() })
			if err := conn.Flush(); err != nil {
				t.Fatal(err)
			}

			payload, err := json.Marshal(taskrun.Request{Task: &store.Task{ID: test.requestID}, WorktreeGrant: "grant"})
			if err != nil {
				t.Fatal(err)
			}
			message, err := conn.Request(test.subject, payload, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var response taskrun.Response
			if err := json.Unmarshal(message.Data, &response); err != nil {
				t.Fatal(err)
			}
			if response.Error != test.want {
				t.Fatalf("response error = %q, want %q", response.Error, test.want)
			}
			if handlerCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", handlerCalls)
			}
		})
	}
}

type recordingSDKSubscription struct {
	unsubscribeCalls int
	unsubscribeErr   error
}

func (s *recordingSDKSubscription) Unsubscribe() error {
	s.unsubscribeCalls++
	return s.unsubscribeErr
}

func TestSubscribeTasksReportsSetupFailuresAndCleansUpFlushFailure(t *testing.T) {
	subscribeErr := errors.New("subscribe failed")
	queueErr := errors.New("queue subscribe failed")
	flushErr := errors.New("flush failed")
	cleanupErr := errors.New("unsubscribe failed")
	tests := []struct {
		name             string
		dedicated        bool
		subscribeErr     error
		queueErr         error
		flushErr         error
		cleanupErr       error
		wantErr          error
		wantUnsubscribes int
		wantCleanupLog   bool
	}{
		{name: "direct subscribe", dedicated: true, subscribeErr: subscribeErr, wantErr: subscribeErr},
		{name: "queue subscribe", queueErr: queueErr, wantErr: queueErr},
		{name: "flush cleanup succeeds", dedicated: true, flushErr: flushErr, wantErr: flushErr, wantUnsubscribes: 1},
		{name: "flush cleanup fails", dedicated: true, flushErr: flushErr, cleanupErr: cleanupErr, wantErr: flushErr, wantUnsubscribes: 1, wantCleanupLog: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sub := &recordingSDKSubscription{unsubscribeErr: test.cleanupErr}
			transport := &Transport{
				subscribe: func(string, natsio.MsgHandler) (sdkSubscription, error) {
					return sub, test.subscribeErr
				},
				queueSubscribe: func(string, string, natsio.MsgHandler) (sdkSubscription, error) {
					return sub, test.queueErr
				},
				flush: func(timeout time.Duration) error {
					if timeout != taskSubscriptionFlushTimeout {
						t.Fatalf("flush timeout = %s, want %s", timeout, taskSubscriptionFlushTimeout)
					}
					return test.flushErr
				},
			}
			var output bytes.Buffer
			_, err := transport.SubscribeTasks(t.Context(), 42, test.dedicated, func(context.Context, taskrun.Request) (*taskrun.Response, error) {
				t.Fatal("handler called")
				return nil, nil
			}, slog.New(slog.NewTextHandler(&output, nil)))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if sub.unsubscribeCalls != test.wantUnsubscribes {
				t.Fatalf("unsubscribe calls = %d, want %d", sub.unsubscribeCalls, test.wantUnsubscribes)
			}
			if got := strings.Contains(output.String(), "task subscription cleanup failed after flush failure"); got != test.wantCleanupLog {
				t.Fatalf("cleanup warning logged = %v, want %v; output %q", got, test.wantCleanupLog, output.String())
			}
		})
	}
}

func TestConnectOwnsWorkerTopology(t *testing.T) {
	connectErr := errors.New("stop after config capture")
	var got busnats.Config
	_, err := connect(t.Context(), Config{
		URL:          "nats://worker.test:4222",
		Token:        "secret",
		ConsumerName: "agent-worker",
	}, slog.New(slog.DiscardHandler), func(_ context.Context, config busnats.Config, _ *slog.Logger) (*busnats.Client, error) {
		got = config
		return nil, connectErr
	})
	if !errors.Is(err, connectErr) {
		t.Fatalf("connect error = %v, want %v", err, connectErr)
	}
	if got.URL != "nats://worker.test:4222" || got.Token != "secret" || got.ConsumerName != "agent-worker" {
		t.Fatalf("deployment config = %#v", got)
	}
	wantSubjects := []string{workintake.SubjectTaskWildcard, agentexec.SubjectAgentWildcard}
	if !reflect.DeepEqual(got.Subjects, wantSubjects) {
		t.Fatalf("subjects = %v, want %v", got.Subjects, wantSubjects)
	}
	if got.FilterSubject != agentexec.SubjectAgentWildcard || got.PollTimeout != stagePollTimeout || got.AckWait != stageAckWait {
		t.Fatalf("topology config = %#v, want filter=%q poll=%s ack=%s", got, agentexec.SubjectAgentWildcard, stagePollTimeout, stageAckWait)
	}
}

type rawStageMessage struct {
	data     []byte
	subject  string
	reply    string
	replyErr error
	ackErr   error
	nakErr   error
	ackCalls int
	nakCalls int
}

func (m *rawStageMessage) Data() []byte                  { return m.data }
func (m *rawStageMessage) Subject() string               { return m.subject }
func (m *rawStageMessage) ReplyAddress() (string, error) { return m.reply, m.replyErr }
func (m *rawStageMessage) Ack() error                    { m.ackCalls++; return m.ackErr }
func (m *rawStageMessage) Nak() error                    { m.nakCalls++; return m.nakErr }

func TestStageMessageOwnsDecodeReplyAndAcknowledgement(t *testing.T) {
	replyErr := errors.New("no reply")
	for _, test := range []struct {
		name    string
		wire    *rawStageMessage
		wantErr string
	}{
		{name: "decode", wire: &rawStageMessage{data: []byte("not json"), subject: "archie.agent.bad"}, wantErr: "decode request:"},
		{name: "reply", wire: &rawStageMessage{data: []byte(`{"task_id":7}`), subject: "archie.agent.no-reply", replyErr: replyErr}, wantErr: "stage request on archie.agent.no-reply:"},
		{name: "typed request", wire: &rawStageMessage{data: []byte(`{"task_id":7,"stage":"test"}`), reply: "_INBOX.7"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := newStageMessage(test.wire, func(context.Context, string, []byte) error { return nil })
			request, err := message.Request()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Request error = %v, want containing %q", err, test.wantErr)
				}
			} else if err != nil || request.TaskID != 7 || request.Stage != "test" {
				t.Fatalf("Request = (%#v, %v), want typed task 7/test", request, err)
			}
			if err := message.Ack(); err != nil {
				t.Fatal(err)
			}
			if err := message.Nak(); err != nil {
				t.Fatal(err)
			}
			if test.wire.ackCalls != 1 || test.wire.nakCalls != 1 {
				t.Fatalf("ack/nak calls = (%d,%d), want (1,1)", test.wire.ackCalls, test.wire.nakCalls)
			}
		})
	}
}

func TestStageMessageDelegatesAcknowledgementErrors(t *testing.T) {
	ackErr := errors.New("ack failed")
	nakErr := errors.New("nak failed")
	wire := &rawStageMessage{data: []byte(`{"task_id":7}`), reply: "_INBOX.7", ackErr: ackErr, nakErr: nakErr}
	message := newStageMessage(wire, func(context.Context, string, []byte) error { return nil })
	if err := message.Ack(); !errors.Is(err, ackErr) {
		t.Fatalf("Ack error = %v, want %v", err, ackErr)
	}
	if err := message.Nak(); !errors.Is(err, nakErr) {
		t.Fatalf("Nak error = %v, want %v", err, nakErr)
	}
}

func TestStageMessageOwnsResponseEncodingAndInboxDelivery(t *testing.T) {
	encodeErr := errors.New("encode failed")
	respondErr := errors.New("respond failed")
	response := &agentexec.AgentResponseEnvelope{Result: agentexec.Result{Status: agentexec.StatusPassed}}

	wire := &rawStageMessage{data: []byte(`{"task_id":7}`), reply: "_INBOX.7"}
	message := newStageMessage(wire, func(_ context.Context, reply string, data []byte) error {
		if reply != "_INBOX.7" || !strings.Contains(string(data), `"status":"passed"`) {
			t.Fatalf("reply/data = (%q,%s)", reply, data)
		}
		return nil
	})
	if err := message.Respond(t.Context(), response); err != nil {
		t.Fatal(err)
	}

	concrete, ok := message.(*stageMessage)
	if !ok {
		t.Fatalf("message type = %T, want *stageMessage", message)
	}
	concrete.encode = func(any) ([]byte, error) { return nil, encodeErr }
	if err := concrete.Respond(t.Context(), response); !errors.Is(err, encodeErr) || !strings.Contains(err.Error(), "encode response") {
		t.Fatalf("encode error = %v", err)
	}
	concrete.encode = json.Marshal
	concrete.respond = func(context.Context, string, []byte) error { return respondErr }
	if err := concrete.Respond(t.Context(), response); !errors.Is(err, respondErr) || !strings.Contains(err.Error(), "publish response") {
		t.Fatalf("respond error = %v", err)
	}
}

func TestRPCFactoriesPreserveIdentityAndTimeout(t *testing.T) {
	transport, _ := testTransport(t)
	timeout := 60 * time.Second
	forgeClient, ok := transport.Forger("identity-a", timeout).(*forgerpc.Client)
	if !ok || forgeClient.Identity != "identity-a" || forgeClient.Timeout != timeout {
		t.Fatalf("forge client = %#v", forgeClient)
	}
	storeClient, ok := transport.Store(timeout).(*storerpc.Client)
	if !ok || storeClient.Timeout != timeout {
		t.Fatalf("store client = %#v", storeClient)
	}
	treeClient, ok := transport.Trees("identity-a", "grant-a", timeout).(*worktreerpc.Client)
	if !ok || treeClient.Identity != "identity-a" || treeClient.Grant != "grant-a" || treeClient.Timeout != timeout {
		t.Fatalf("tree client = %#v", treeClient)
	}
}

func TestTransportFromBusClassifiesUnavailableCoreConnection(t *testing.T) {
	transport, err := transportFromBus(&busnats.Client{})
	if transport != nil {
		t.Fatalf("transport = %#v, want nil", transport)
	}
	if !errors.Is(err, ErrCoreConnectionUnavailable) {
		t.Fatalf("error = %v, want ErrCoreConnectionUnavailable", err)
	}
}

func TestConnectOwnsJetStreamAndCoreConnection(t *testing.T) {
	srv := natstest.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	transport, err := Connect(t.Context(), Config{
		URL:          srv.ClientURL(),
		ConsumerName: "worker-test",
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	transport.Close()
	if _, err := transport.FetchStage(t.Context()); err == nil {
		t.Fatal("FetchStage after Close returned nil error")
	}
}
