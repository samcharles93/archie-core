package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// recordingChannelStatusStore records every report the service publishes.
type recordingChannelStatusStore struct {
	mu     sync.Mutex
	writes [][]storecontract.ChannelStatus
}

func (s *recordingChannelStatusStore) PutChannelStatus(_ context.Context, channels []storecontract.ChannelStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, append([]storecontract.ChannelStatus(nil), channels...))
	return nil
}

func (s *recordingChannelStatusStore) ChannelStatus(context.Context) ([]storecontract.ChannelStatus, error) {
	return nil, nil
}

// waitForPublishedState polls the recorded reports until one carries the channel
// in want, so the assertion races the publisher rather than the clock.
func waitForPublishedState(t *testing.T, store *recordingChannelStatusStore, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		for _, write := range store.writes {
			for _, channel := range write {
				if channel.ID == id && channel.State == want {
					store.mu.Unlock()
					return
				}
			}
		}
		store.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	t.Fatalf("no report carried %s as %q; reports were %+v", id, want, store.writes)
}

// TestServicePublishesChannelStatus is the point of archie-core-8cda.6.8: the
// report has to reach the store the dashboard reads, not stay in this process.
// Without this the surface exists and no operator ever sees it.
func TestServicePublishesChannelStatus(t *testing.T) {
	writer := &recordingChannelStatusStore{}
	srv, err := compose(t.Context(), deps{
		ChannelStatus: writer,
		Config: ResolvedConfig{
			Options:     Options{ShutdownTimeout: 2 * time.Second},
			WebhookAddr: fmt.Sprintf("127.0.0.1:%d", freePort(t)),
			Webhook:     config.WebhookRoute{Path: "/smoke"},
		},
		Log:  slog.Default(),
		Chat: &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: "ok"},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- srv.Start(ctx) }()

	// The start-up report and the channel's own "starting" coalesce by design --
	// signals coalesce, because a reader only ever wants the latest state of every
	// channel -- so the first published state is either "configured" or "starting"
	// depending on which side wins. What must hold is that the transitions reach
	// the store, and that the last word is the truth.
	waitForPublishedState(t, writer, "webhook", "running")

	srv.Stop()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("srv.Start returned %v, want a clean stop", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("srv.Start did not return after Stop")
	}
	// The final report is what leaves the store honest about a stopped service.
	waitForPublishedState(t, writer, "webhook", "stopped")
}
