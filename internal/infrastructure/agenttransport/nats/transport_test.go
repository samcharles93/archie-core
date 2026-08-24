package nats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

func TestTransportContainsNoSharedTaskWorkerTopology(t *testing.T) {
	source, err := os.ReadFile("transport.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"QueueSubscribe", "taskRunSubjectWildcard", "taskRunQueueGroup"} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("transport.go contains forbidden shared-worker mechanism %q", forbidden)
		}
	}
}

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

// Full-task handoff and all worker RPC use core NATS. The worker must connect
// to a broker with JetStream disabled; requiring a durable consumer would mean
// the deleted per-stage transport still owns startup.
func TestConnectRequiresOnlyCoreNATS(t *testing.T) {
	srv := natstest.RunServer(&server.Options{Port: -1, Authorization: "worker-secret"})
	t.Cleanup(srv.Shutdown)

	transport, err := Connect(t.Context(), Config{URL: srv.ClientURL(), Token: "worker-secret"}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Connect to core-only NATS: %v", err)
	}
	transport.Close()
}

func TestSubscribeTasksServesOnlyBootTaskSubject(t *testing.T) {
	transport, conn := testTransport(t)
	calls := 0
	subscription, err := transport.SubscribeTasks(t.Context(), 42, func(_ context.Context, request taskrun.Request) (*taskrun.Response, error) {
		calls++
		if request.Task.ID != 42 {
			t.Fatalf("task ID = %d, want 42", request.Task.ID)
		}
		return &taskrun.Response{Status: "done"}, nil
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })

	payload, err := json.Marshal(taskrun.Request{Task: &store.Task{ID: 42}, WorktreeGrant: "grant"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := conn.Request(SubjectForTask(42), payload, time.Second)
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
}

func TestSubscribeTasksOwnsWireErrors(t *testing.T) {
	transport, conn := testTransport(t)
	handlerCalls := 0
	subscription, err := transport.SubscribeTasks(t.Context(), 9, func(context.Context, taskrun.Request) (*taskrun.Response, error) {
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
			name:       "boot mismatch",
			subject:    SubjectForTask(9),
			bootTaskID: 42,
			requestID:  9,
			want:       "validate taskrun request: task ID 9 does not match boot task ID 42",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport, conn := testTransport(t)
			handlerCalls := 0
			subscription, err := conn.Subscribe(test.subject, func(message *natsio.Msg) {
				transport.handleTask(t.Context(), message, test.bootTaskID, func(context.Context, taskrun.Request) (*taskrun.Response, error) {
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
	flushErr := errors.New("flush failed")
	cleanupErr := errors.New("unsubscribe failed")
	tests := []struct {
		name             string
		subscribeErr     error
		flushErr         error
		cleanupErr       error
		wantErr          error
		wantUnsubscribes int
		wantCleanupLog   bool
	}{
		{name: "direct subscribe", subscribeErr: subscribeErr, wantErr: subscribeErr},
		{name: "flush cleanup succeeds", flushErr: flushErr, wantErr: flushErr, wantUnsubscribes: 1},
		{name: "flush cleanup fails", flushErr: flushErr, cleanupErr: cleanupErr, wantErr: flushErr, wantUnsubscribes: 1, wantCleanupLog: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sub := &recordingSDKSubscription{unsubscribeErr: test.cleanupErr}
			transport := &Transport{
				subscribe: func(string, natsio.MsgHandler) (sdkSubscription, error) {
					return sub, test.subscribeErr
				},
				flush: func(timeout time.Duration) error {
					if timeout != taskSubscriptionFlushTimeout {
						t.Fatalf("flush timeout = %s, want %s", timeout, taskSubscriptionFlushTimeout)
					}
					return test.flushErr
				},
			}
			var output bytes.Buffer
			_, err := transport.SubscribeTasks(t.Context(), 42, func(context.Context, taskrun.Request) (*taskrun.Response, error) {
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
