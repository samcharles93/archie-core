package nats

import (
	"context"
	"log/slog"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
)

func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	jsDir := t.TempDir()
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: jsDir}); err != nil {
		t.Fatalf("enable jetstream: %v", err)
	}
	return srv
}

func TestConnectAndPublish(t *testing.T) {
	srv := startEmbedded(t)
	url := srv.ClientURL()

	ctx := context.Background()
	client, err := Connect(ctx, url, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Publish a task.
	if err := client.PublishTask(ctx, "sam", "todo", 1, "fix bug", "body text", "bug"); err != nil {
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
	if tm.Owner != "sam" || tm.Repo != "todo" || tm.Number != 1 || tm.Title != "fix bug" || tm.Body != "body text" {
		t.Fatalf("unexpected task: %+v", tm)
	}
	msg.Ack()
}

func TestDedup(t *testing.T) {
	srv := startEmbedded(t)
	url := srv.ClientURL()

	ctx := context.Background()
	client, err := Connect(ctx, url, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Publish one task and verify it's consumable.
	if err := client.PublishTask(ctx, "acme", "app", 1, "first", "body", "bug"); err != nil {
		t.Fatal(err)
	}

	msg, err := client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("first message not received")
	}
	msg.Ack()

	// Second publish with same Msg-Id should be deduped.
	if err := client.PublishTask(ctx, "acme", "app", 1, "second", "body-v2", "bug"); err != nil {
		t.Fatal(err)
	}

	msg2, err := client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg2 != nil {
		msg2.Ack()
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
