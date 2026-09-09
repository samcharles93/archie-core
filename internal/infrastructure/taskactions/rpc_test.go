package taskactions

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"

	domain "github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	return srv
}

func connect(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// stubStore returns one task, or none, and fails every mutation with err.
type stubStore struct {
	task *domain.Task
	err  error
}

func (s stubStore) TaskByID(context.Context, int64) (*domain.Task, error) { return s.task, nil }

func (s stubStore) Transition(context.Context, int64, string, string, string) error { return s.err }
func (s stubStore) Requeue(context.Context, int64, string, string) error            { return s.err }
func (s stubStore) RetryTask(context.Context, int64, string, string) error          { return s.err }

func (s stubStore) ArchiveTask(context.Context, int64, string, events.Event) (int64, error) {
	return 0, s.err
}
func (s stubStore) InsertEvent(context.Context, events.Event) (int64, error) { return 0, s.err }

// TestActionErrorKeepsItsSentinelAcrossNATS: the dashboard picks its HTTP
// status from these sentinels (404, 409, 503), and this hop reduces an error
// to a JSON string. Without a kind alongside the message every failure
// arrives indistinguishable and the operator is told to check the daemon
// over a task that simply moved on.
func TestActionErrorKeepsItsSentinelAcrossNATS(t *testing.T) {
	parked := &domain.Task{ID: 7, Owner: "acme", Repo: "widget", Status: "parked"}
	running := &domain.Task{ID: 7, Owner: "acme", Repo: "widget", Status: "running"}

	for _, tc := range []struct {
		name   string
		store  stubStore
		cancel func(int64) bool
		action taskstate.Action
		want   error
	}{
		{
			name:   "missing task",
			store:  stubStore{},
			action: taskstate.ActionAbandon,
			want:   domain.ErrNotFound,
		},
		{
			name:   "action the status forbids",
			store:  stubStore{task: parked},
			action: taskstate.ActionApprove,
			want:   domain.ErrConflict,
		},
		{
			name:   "no runtime control wired",
			store:  stubStore{task: running},
			action: taskstate.ActionStop,
			want:   domain.ErrUnavailable,
		},
		{
			name:   "store refused a stale transition",
			store:  stubStore{task: running, err: store.ErrStaleTransition},
			cancel: func(int64) bool { return true },
			action: taskstate.ActionStop,
			want:   store.ErrStaleTransition,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startEmbedded(t)
			nc := connect(t, srv.ClientURL())
			service := domain.Service{Store: tc.store, CancelTask: tc.cancel, Warn: func(string, ...any) {}}
			stop, err := Register(nc, service, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("register responder: %v", err)
			}
			t.Cleanup(stop)
			if err := nc.Flush(); err != nil {
				t.Fatalf("flush: %v", err)
			}

			_, err = Client{Conn: nc}.ApplyChatTaskAction(t.Context(), nil, 7, tc.action)
			if err == nil {
				t.Fatalf("ApplyChatTaskAction(%s) error = nil, want %v", tc.action, tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v (%T) is not %v", err, err, tc.want)
			}
			// The wording the operator reads survives too, not just the class.
			direct := service.Apply(t.Context(), nil, 7, tc.action)
			if direct == nil || err.Error() != direct.Error() {
				t.Fatalf("remote message = %q, want the daemon's own %v", err.Error(), direct)
			}
		})
	}
}

// TestActionScopeCrossesNATS pins the operator scope over the same hop: a nil
// identity is an authenticated dashboard operator, and JSON has to carry that
// absence rather than an empty name. The service records which one acted, so
// the timeline is the observable.
func TestActionScopeCrossesNATS(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity *string
		want     string
	}{
		{name: "operator", want: "abandoned via the dashboard"},
		{name: "chat identity", identity: new("scout"), want: "abandoned via chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startEmbedded(t)
			nc := connect(t, srv.ClientURL())

			var published events.Event
			service := domain.Service{
				Store:   stubStore{task: &domain.Task{ID: 7, Owner: "acme", Repo: "widget", Identity: "scout", Status: "parked"}},
				Publish: func(e events.Event) { published = e },
			}
			stop, err := Register(nc, service, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("register responder: %v", err)
			}
			t.Cleanup(stop)
			if err := nc.Flush(); err != nil {
				t.Fatalf("flush: %v", err)
			}

			result, err := Client{Conn: nc}.ApplyChatTaskAction(t.Context(), tc.identity, 7, taskstate.ActionAbandon)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if result.TaskID != 7 {
				t.Fatalf("result = %+v, want task 7", result)
			}
			if published.Detail != tc.want {
				t.Fatalf("event detail = %q, want %q", published.Detail, tc.want)
			}
		})
	}
}
