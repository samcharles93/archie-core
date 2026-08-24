package storerpc

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
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

func newTestServer(t *testing.T) (*store.Store, *nats.Conn, *Client) {
	t.Helper()
	s := store.OpenTest(t)
	srv := startEmbedded(t)
	url := srv.ClientURL()

	serverConn := connect(t, url)
	rpcServer := &Server{Store: s, Log: slog.New(slog.DiscardHandler)}
	unsub, err := rpcServer.Register(serverConn)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(unsub)

	clientConn := connect(t, url)
	client := &Client{Conn: clientConn, Timeout: 2 * time.Second}

	return s, serverConn, client
}

func enqueueTask(t *testing.T, s *store.Store) *store.Task {
	t.Helper()
	ctx := context.Background()
	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "title", "body", "", ""); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}
	return task
}

func TestClientUpdatePersistsViaServer(t *testing.T) {
	s, _, client := newTestServer(t)
	task := enqueueTask(t, s)

	task.Workflow = "implement"
	task.Stage = "plan"
	task.Branch = "archie/1-test"
	task.Plan = "do the thing"
	task.Notes = "some notes"
	task.PRNumber = 42
	task.TokensUsed = 1000
	task.Iterations = 3

	if err := client.Update(context.Background(), task); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.TaskByIssue(context.Background(), "acme", "widget", 1)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Workflow != "implement" || got.Stage != "plan" || got.Branch != "archie/1-test" ||
		got.Plan != "do the thing" || got.Notes != "some notes" || got.PRNumber != 42 ||
		got.TokensUsed != 1000 || got.Iterations != 3 {
		t.Fatalf("update did not persist over RPC, got %+v", got)
	}
}

func TestClientTransitionPersistsViaServer(t *testing.T) {
	s, _, client := newTestServer(t)
	task := enqueueTask(t, s)

	if err := client.Transition(context.Background(), task.ID, store.StatusRunning, store.StatusPROpen, "opened PR"); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	got, err := s.TaskByIssue(context.Background(), "acme", "widget", 1)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Status != store.StatusPROpen {
		t.Fatalf("expected status %s, got %s", store.StatusPROpen, got.Status)
	}
}

func TestClientInsertEventPersistsViaServer(t *testing.T) {
	s, _, client := newTestServer(t)
	task := enqueueTask(t, s)
	event := events.Event{Kind: events.KindAgentFinish, TaskID: task.ID, Data: map[string]any{"tokens": 42}}

	id, err := client.InsertEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	got, err := s.TaskEvents(context.Background(), task.ID)
	if err != nil || len(got) != 1 || got[0].ID != id || got[0].Kind != events.KindAgentFinish {
		t.Fatalf("TaskEvents = (%+v, %v), want persisted agent_finish id %d", got, err, id)
	}
}

func TestClientPropagatesServerError(t *testing.T) {
	s, _, client := newTestServer(t)
	task := enqueueTask(t, s)

	// Close the store's underlying DB so the server-side call fails,
	// and confirm that failure round-trips back to the client as an error.
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	err := client.Update(context.Background(), task)
	if err == nil {
		t.Fatal("expected Update to return an error when the server-side store is closed")
	}
}

func TestClientUpdateTimesOutWithNoResponder(t *testing.T) {
	srv := startEmbedded(t)
	clientConn := connect(t, srv.ClientURL())
	client := &Client{Conn: clientConn, Timeout: 100 * time.Millisecond}

	err := client.Update(context.Background(), &store.Task{ID: 1})
	if err == nil {
		t.Fatal("expected Update to time out with no server registered")
	}
}
