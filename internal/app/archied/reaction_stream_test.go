package archied

import (
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	natsio "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestConnectNATSCreatesReactionStream pins the ARCHIE_REACTIONS stream the
// daemon must provision alongside ARCHIE_TASKS. A publish to archie.reaction.*
// must land in a fan-out (LimitsPolicy) stream: two consumers on overlapping
// filter subjects both receive it. Under the WorkQueuePolicy ARCHIE_TASKS
// keeps, the second consumer would silently receive nothing (the trap
// documented in CLAUDE.md).
//
// Before this stream existed the publish below failed with "no matching
// stream" because connectNATS bound only archie.task.>.
func TestConnectNATSCreatesReactionStream(t *testing.T) {
	b := &boot{
		cfg: config.Config{
			DBPath: filepath.Join(t.TempDir(), "archie.db"),
			NATS:   config.NATSConfig{Mode: config.NATSModeEmbedded},
		},
		log: slog.New(slog.DiscardHandler),
	}
	if err := b.connectNATS(t.Context()); err != nil {
		t.Fatalf("connectNATS = %v", err)
	}
	t.Cleanup(b.cleanup)

	conn, err := natsio.Connect(b.natsURL, natsio.Token(b.natsToken))
	if err != nil {
		t.Fatalf("raw nats connect = %v", err)
	}
	t.Cleanup(conn.Close)

	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatalf("jetstream.New = %v", err)
	}

	// Criterion 1: a publish to archie.reaction.* lands in ARCHIE_REACTIONS.
	// Before the stream existed this returned "no matching stream".
	if _, err := js.PublishMsg(t.Context(), &natsio.Msg{
		Subject: "archie.reaction.test",
		Data:    []byte("x"),
	}); err != nil {
		t.Fatalf("publish to archie.reaction.* = %v, want it to land in ARCHIE_REACTIONS", err)
	}

	stream, err := js.Stream(t.Context(), "ARCHIE_REACTIONS")
	if err != nil {
		t.Fatalf("js.Stream(ARCHIE_REACTIONS) = %v", err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("stream.Info = %v", err)
	}
	if info.Config.Retention != jetstream.LimitsPolicy {
		t.Fatalf("ARCHIE_REACTIONS retention = %v, want %v (fan-out)", info.Config.Retention, jetstream.LimitsPolicy)
	}
	if !slices.Contains(info.Config.Subjects, "archie.reaction.>") {
		t.Fatalf("ARCHIE_REACTIONS subjects = %v, want to contain archie.reaction.>", info.Config.Subjects)
	}

	// Criterion 2: two consumers on the same overlapping filter subject both
	// receive the one published message. A work-queue stream would deliver it
	// to only one of them.
	got := make(chan string, 2)
	fetch := func() {
		consumer, err := stream.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
			FilterSubject: "archie.reaction.>",
			AckPolicy:     jetstream.AckExplicitPolicy,
		})
		if err != nil {
			t.Errorf("create reaction consumer: %v", err)
			return
		}
		batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			t.Errorf("reaction consumer fetch: %v", err)
			return
		}
		for msg := range batch.Messages() {
			if msg == nil {
				continue
			}
			got <- string(msg.Data())
			_ = msg.Ack()
		}
	}
	go fetch()
	go fetch()

	var received []string
	deadline := time.After(10 * time.Second)
	for len(received) < 2 {
		select {
		case data := <-got:
			received = append(received, data)
		case <-deadline:
			t.Fatalf("fan-out: received %v, want both consumers to receive x", received)
		}
	}
	for _, data := range received {
		if data != "x" {
			t.Errorf("reaction consumer received %q, want x", data)
		}
	}
}
