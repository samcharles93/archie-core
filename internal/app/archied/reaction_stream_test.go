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

// TestConnectNATSCreatesReactionStream pins the two JetStream streams the
// daemon must provision alongside each other: ARCHIE_TASKS (work-queue task
// distribution) and ARCHIE_REACTIONS (fan-out reaction delivery). Each case
// asserts the ACTUAL stream configuration the embedded broker reports, not
// just the Config struct that composed it.
//
// ARCHIE_REACTIONS must be a LimitsPolicy stream so two consumers on
// overlapping filter subjects both receive a reaction (the trap documented in
// CLAUDE.md), and it must carry a finite retention limit so acknowledged
// reactions do not accumulate without bound. ARCHIE_TASKS must remain a
// WorkQueuePolicy stream with no age/count/byte limit: acked messages are
// discarded, so its only bound is outstanding work, and imposing a limit
// could silently drop undelivered tasks.
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

	streamCases := []struct {
		name       string
		stream     string
		retention  jetstream.RetentionPolicy
		subjects   []string
		wantMaxAge time.Duration
	}{
		{
			name:       "reactions fan-out with finite retention",
			stream:     "ARCHIE_REACTIONS",
			retention:  jetstream.LimitsPolicy,
			subjects:   []string{"archie.reaction.>"},
			wantMaxAge: reactionStreamMaxAge,
		},
		{
			name:      "tasks work-queue unchanged and unbounded",
			stream:    "ARCHIE_TASKS",
			retention: jetstream.WorkQueuePolicy,
			subjects:  []string{"archie.task.>"},
			// wantMaxAge zero asserts ARCHIE_TASKS stays unbounded by age.
		},
	}
	for _, tc := range streamCases {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := js.Stream(t.Context(), tc.stream)
			if err != nil {
				t.Fatalf("js.Stream(%s) = %v", tc.stream, err)
			}
			info, err := stream.Info(t.Context())
			if err != nil {
				t.Fatalf("stream.Info(%s) = %v", tc.stream, err)
			}
			if info.Config.Retention != tc.retention {
				t.Fatalf("%s retention = %v, want %v", tc.stream, info.Config.Retention, tc.retention)
			}
			for _, subject := range tc.subjects {
				if !slices.Contains(info.Config.Subjects, subject) {
					t.Errorf("%s subjects = %v, want to contain %s", tc.stream, info.Config.Subjects, subject)
				}
			}
			if info.Config.MaxAge != tc.wantMaxAge {
				t.Fatalf("%s MaxAge = %v, want %v", tc.stream, info.Config.MaxAge, tc.wantMaxAge)
			}
		})
	}

	// Fan-out proof: two consumers on the same overlapping filter subject
	// both receive the one published message. A work-queue stream would
	// deliver it to only one of them.
	reactionStream, err := js.Stream(t.Context(), "ARCHIE_REACTIONS")
	if err != nil {
		t.Fatalf("js.Stream(ARCHIE_REACTIONS) = %v", err)
	}
	if _, err := js.PublishMsg(t.Context(), &natsio.Msg{
		Subject: "archie.reaction.test",
		Data:    []byte("x"),
	}); err != nil {
		t.Fatalf("publish to archie.reaction.* = %v, want it to land in ARCHIE_REACTIONS", err)
	}

	got := make(chan string, 2)
	fetch := func() {
		consumer, err := reactionStream.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
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
