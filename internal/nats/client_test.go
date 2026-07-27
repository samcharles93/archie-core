package nats

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go/jetstream"
)

// startEmbedded starts an embedded NATS server with JetStream file storage.
// The shutdown cleanup is registered AFTER t.TempDir() so that server shutdown
// completes deterministically before TempDir removes its storage directory.
// This prevents "directory not empty" races where JetStream's async file
// operations contend with testing.TempDir's os.RemoveAll.
func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	// Call TempDir FIRST so its internal cleanup is registered before shutdown.
	// LIFO cleanup order ensures shutdown runs before TempDir removal.
	jsDir := t.TempDir()
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: jsDir}); err != nil {
		t.Fatalf("enable jetstream: %v", err)
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv
}

// startEmbeddedWithToken starts an embedded NATS server with token auth.
// Same deterministic cleanup ordering as startEmbedded.
func startEmbeddedWithToken(t *testing.T, token string) *server.Server {
	t.Helper()
	opts := natssrv.DefaultTestOptions
	opts.Port = -1
	opts.Authorization = token
	srv := natssrv.RunServer(&opts)
	// TempDir before Cleanup  --  shutdown runs before TempDir removal.
	jsDir := t.TempDir()
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: jsDir}); err != nil {
		t.Fatalf("enable jetstream: %v", err)
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv
}

func TestConnectUsesToken(t *testing.T) {
	const token = "test-nats-token"
	srv := startEmbeddedWithToken(t, token)

	client, err := Connect(
		context.Background(),
		srv.ClientURL(),
		token,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("Connect with token: %v", err)
	}
	client.Close()
}

func TestConnectAndPublish(t *testing.T) {
	srv := startEmbedded(t)
	url := srv.ClientURL()

	ctx := context.Background()
	client, err := Connect(ctx, url, "", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Publish a task.
	if err := client.PublishTask(ctx, "acme", "todo", 1, "fix bug", "body text", "bug", ""); err != nil {
		t.Fatal(err)
	}

	// Fetch it back.
	msg, err := client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected a message")
	}

	tm, err := DecodeTask(msg)
	if err != nil {
		t.Fatal(err)
	}
	if tm.Owner != "acme" || tm.Repo != "todo" || tm.Number != 1 || tm.Title != "fix bug" || tm.Body != "body text" {
		t.Fatalf("unexpected task: %+v", tm)
	}
	_ = msg.Ack()
}

func TestDedup(t *testing.T) {
	srv := startEmbedded(t)
	url := srv.ClientURL()

	ctx := context.Background()
	client, err := Connect(ctx, url, "", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Publish one task and verify it's consumable.
	if err := client.PublishTask(ctx, "acme", "app", 1, "first", "body", "bug", ""); err != nil {
		t.Fatal(err)
	}

	msg, err := client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("first message not received")
	}
	_ = msg.Ack()

	// Second publish with same Msg-Id should be deduped.
	if err := client.PublishTask(ctx, "acme", "app", 1, "second", "body-v2", "bug", ""); err != nil {
		t.Fatal(err)
	}

	msg2, err := client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg2 != nil {
		_ = msg2.Ack()
		t.Fatal("expected no second message (dedup)")
	}
}

func TestSubjectRouting(t *testing.T) {
	tests := []struct {
		labels []string
		want   string
	}{
		{[]string{"bug"}, SubjectTaskBug},
		{[]string{"enhancement"}, SubjectTaskDefault},
		{[]string{"feature"}, SubjectTaskFeature},
		{[]string{"bootstrap"}, SubjectTaskBootstrap},
		{[]string{"bug", "feature"}, SubjectTaskBug}, // first match wins
		{[]string{"enhancement", "bug"}, SubjectTaskBug},
		{nil, SubjectTaskDefault},
		{[]string{}, SubjectTaskDefault},
	}
	for _, tt := range tests {
		got := SubjectForLabels(tt.labels)
		if got != tt.want {
			t.Errorf("SubjectForLabels(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

// --- mock types for Fetch error propagation tests ---

// errSentinel is a distinguishable error used in mock MessageBatch.Error().
var errSentinel = errors.New("nats: consumer deleted")

// mockMessageBatch implements jetstream.MessageBatch.
type mockMessageBatch struct {
	msgs <-chan jetstream.Msg
	err  error
}

func (m *mockMessageBatch) Messages() <-chan jetstream.Msg { return m.msgs }
func (m *mockMessageBatch) Error() error                   { return m.err }

// mockConsumer implements jetstream.Consumer.
// Only Fetch is exercised by Client.Fetch; the remaining methods panic to
// flag accidental usage.
type mockConsumer struct {
	batch jetstream.MessageBatch
}

func (m *mockConsumer) Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	return m.batch, nil
}

func (m *mockConsumer) FetchBytes(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	panic("FetchBytes not implemented")
}

func (m *mockConsumer) FetchNoWait(int) (jetstream.MessageBatch, error) {
	panic("FetchNoWait not implemented")
}

func (m *mockConsumer) Consume(jetstream.MessageHandler, ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error) {
	panic("Consume not implemented")
}

func (m *mockConsumer) Messages(...jetstream.PullMessagesOpt) (jetstream.MessagesContext, error) {
	panic("Messages not implemented")
}

func (m *mockConsumer) Next(...jetstream.FetchOpt) (jetstream.Msg, error) {
	panic("Next not implemented")
}

func (m *mockConsumer) Info(context.Context) (*jetstream.ConsumerInfo, error) {
	panic("Info not implemented")
}

func (m *mockConsumer) CachedInfo() *jetstream.ConsumerInfo {
	panic("CachedInfo not implemented")
}

func TestFetchPropagatesBatchErrorWhenNoMessages(t *testing.T) {
	// Reproduces issue #35: when JetStream reports an error by closing the
	// Messages() channel with zero messages and setting a non-nil batch error,
	// Client.Fetch must propagate that error instead of returning (nil, nil).
	ch := make(chan jetstream.Msg)
	close(ch) // zero messages

	batch := &mockMessageBatch{msgs: ch, err: errSentinel}
	cons := &mockConsumer{batch: batch}

	client := &Client{
		consumer: cons,
		log:      slog.New(slog.DiscardHandler),
	}

	msg, err := client.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected an error from Fetch when batch has an error and no messages, got nil")
	}
	if msg != nil {
		t.Fatalf("expected nil message when batch has an error, got %v", msg)
	}
	if !errors.Is(err, errSentinel) {
		t.Fatalf("expected errSentinel, got %v", err)
	}
}

func TestFetchPropagatesBatchErrorEvenWhenLastMessageSeen(t *testing.T) {
	// Companion: when the batch delivers a message but also has a non-nil
	// error (the existing inside-loop check), we confirm the existing path
	// still works. This is not the reported bug but guards against
	// regressions.
	ch := make(chan jetstream.Msg, 1)
	ch <- nil // nil message triggers the msg==nil continue
	close(ch)

	batch := &mockMessageBatch{msgs: ch, err: errSentinel}
	cons := &mockConsumer{batch: batch}

	client := &Client{
		consumer: cons,
		log:      slog.New(slog.DiscardHandler),
	}

	msg, err := client.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected an error from Fetch when batch has an error, got nil")
	}
	if msg != nil {
		t.Fatalf("expected nil message when batch has an error, got %v", msg)
	}
	if !errors.Is(err, errSentinel) {
		t.Fatalf("expected errSentinel, got %v", err)
	}
}
