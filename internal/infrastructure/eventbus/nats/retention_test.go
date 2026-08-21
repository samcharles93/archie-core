package nats

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestWithDefaultsRetention pins the retention default: unset defaults to
// WorkQueuePolicy (task distribution), and an explicit LimitsPolicy is left
// alone. The pointer form is what lets "unset" default to WorkQueuePolicy
// when the policy's zero value (LimitsPolicy) is a valid explicit choice.
func TestWithDefaultsRetention(t *testing.T) {
	t.Run("unset defaults to work-queue", func(t *testing.T) {
		cfg := (Config{}).withDefaults()
		if cfg.Retention == nil {
			t.Fatal("withDefaults().Retention = nil, want non-nil")
		}
		if *cfg.Retention != jetstream.WorkQueuePolicy {
			t.Fatalf("withDefaults().Retention = %v, want %v", *cfg.Retention, jetstream.WorkQueuePolicy)
		}
	})

	t.Run("explicit limits is preserved", func(t *testing.T) {
		limits := jetstream.LimitsPolicy
		cfg := (Config{Retention: &limits}).withDefaults()
		if cfg.Retention == nil || *cfg.Retention != jetstream.LimitsPolicy {
			t.Fatalf("withDefaults().Retention = %v, want %v", cfg.Retention, jetstream.LimitsPolicy)
		}
	})
}

// TestConnectRetentionFanout proves the reason retention must be a Config
// field: under LimitsPolicy two independent consumers each receive every
// matching message (fan-out), the behaviour reactions need and WorkQueuePolicy
// cannot provide. This is the exact trap CLAUDE.md documents -- a second
// consumer on an overlapping filter subject under WorkQueuePolicy silently
// gets nothing.
func TestConnectRetentionFanout(t *testing.T) {
	srv := natstest.RunServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	t.Cleanup(srv.Shutdown)

	limits := jetstream.LimitsPolicy
	c, err := Connect(t.Context(), Config{
		URL:           srv.ClientURL(),
		StreamName:    "REACTIONS_FANOUT_TEST",
		Subjects:      []string{"reaction.fanout.test.>"},
		FilterSubject: "reaction.fanout.test.>",
		Retention:     &limits,
	}, discardLogger())
	if err != nil {
		t.Fatalf("Connect = %v", err)
	}
	t.Cleanup(c.Close)

	var mu sync.Mutex
	gotA := map[string]int{}
	gotB := map[string]int{}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{}, 2)
	subscriber := func(recv map[string]int) {
		defer func() { done <- struct{}{} }()
		_ = c.Subscribe(ctx, "reaction.fanout.test.>", func(m eventbus.Message) error {
			mu.Lock()
			recv[string(m.Data())]++
			mu.Unlock()
			return nil
		})
	}
	go subscriber(gotA)
	go subscriber(gotB)

	// Publish after the consumers have had a chance to be created, but the
	// Limits stream retains messages regardless of subscriber timing.
	time.Sleep(200 * time.Millisecond)
	if err := c.Publish(t.Context(), "reaction.fanout.test.1", []byte("x")); err != nil {
		t.Fatalf("Publish x = %v", err)
	}
	if err := c.Publish(t.Context(), "reaction.fanout.test.2", []byte("y")); err != nil {
		t.Fatalf("Publish y = %v", err)
	}

	// Both consumers must observe both messages; poll rather than sleep a
	// fixed duration so a slow CI runner does not flake.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		aFull := gotA["x"] >= 1 && gotA["y"] >= 1
		bFull := gotB["x"] >= 1 && gotB["y"] >= 1
		mu.Unlock()
		if aFull && bFull {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	<-done

	mu.Lock()
	defer mu.Unlock()
	for name, got := range map[string]map[string]int{"A": gotA, "B": gotB} {
		if got["x"] != 1 || got["y"] != 1 {
			t.Errorf("consumer %s received %v, want x and y once each (fan-out failed)", name, got)
		}
	}
}
