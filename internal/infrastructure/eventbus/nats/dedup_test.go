package nats

import (
	"errors"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

// TestPublishUniqueSuppressesDuplicateKeyOnWorkQueueStream is the regression
// proof behind archie-core-7d5u.5 (idempotency and poll/webhook parity): a
// forge issue discovered independently by the poller and the webhook
// receiver produces two TaskEnvelopes with the identical
// workintake.TaskEnvelope.IdempotencyKey(), published to the SAME
// WorkQueuePolicy stream ARCHIE_TASKS uses in production (Config.Retention
// left unset, per TestWithDefaultsRetention). A second PublishUnique call
// with the same key inside the dedup window must be silently suppressed by
// the server, so a single consumer only ever observes one message -- never
// two tasks for one issue.
func TestPublishUniqueSuppressesDuplicateKeyOnWorkQueueStream(t *testing.T) {
	srv := natstest.RunServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	t.Cleanup(srv.Shutdown)

	c, err := Connect(t.Context(), Config{
		URL:           srv.ClientURL(),
		StreamName:    "TASKS_DEDUP_TEST",
		Subjects:      []string{"tasks.dedup.test.>"},
		FilterSubject: "tasks.dedup.test.>",
		// Retention left unset: defaults to WorkQueuePolicy, the same policy
		// ARCHIE_TASKS uses in production for task distribution.
	}, discardLogger())
	if err != nil {
		t.Fatalf("Connect = %v", err)
	}
	t.Cleanup(c.Close)

	const key = "archie:owner/repo/42"

	// Poll-discovered publish, then webhook-discovered publish of the same
	// issue moments later -- both carry the identical idempotency key.
	if err := c.PublishUnique(t.Context(), "tasks.dedup.test.issue", key, []byte("poll")); err != nil {
		t.Fatalf("PublishUnique (poll) = %v", err)
	}
	if err := c.PublishUnique(t.Context(), "tasks.dedup.test.issue", key, []byte("webhook")); err != nil {
		t.Fatalf("PublishUnique (webhook) = %v", err)
	}

	msg, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch (first) = %v", err)
	}
	if err := msg.Ack(); err != nil {
		t.Fatalf("Ack = %v", err)
	}

	// A second delivery for the same issue would be a double-dispatched
	// task. There must be none queued.
	if _, err := c.Fetch(t.Context()); err == nil {
		t.Fatal("Fetch (second) succeeded, want eventbus.ErrNoMessage -- duplicate was not suppressed")
	} else if !errors.Is(err, eventbus.ErrNoMessage) {
		t.Fatalf("Fetch (second) = %v, want eventbus.ErrNoMessage", err)
	}
}
