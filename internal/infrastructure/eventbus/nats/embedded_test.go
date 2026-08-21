package nats

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go"
)

// TestStartEmbeddedLifecycle proves the in-process server starts with
// JetStream enabled, accepts a client that provisions a stream, and stops on
// Shutdown. It is the production path behind docs/prds/embedded-nats.md,
// distinct from the test-only natstest helper used elsewhere.
func TestStartEmbeddedLifecycle(t *testing.T) {
	srv, err := StartEmbedded(t.Context(), EmbeddedOptions{StoreDir: t.TempDir()}, discardLogger())
	if err != nil {
		t.Fatalf("StartEmbedded = %v", err)
	}

	url := srv.ClientURL()
	if url == "" {
		t.Fatal("ClientURL() = empty, want the bound address")
	}

	// Connecting and provisioning a stream proves JetStream is live, not just
	// the core NATS socket.
	c, err := Connect(t.Context(), Config{
		URL:           url,
		Subjects:      []string{"embedded.test.>"},
		FilterSubject: "embedded.test.>",
	}, discardLogger())
	if err != nil {
		t.Fatalf("Connect to embedded server = %v", err)
	}
	c.Close()

	srv.Shutdown()

	// After Shutdown the port must refuse new connections, proving the server
	// owns its own stop rather than leaking the goroutine.
	if _, err := nats.Connect(url); err == nil {
		t.Fatalf("nats.Connect(%q) after Shutdown = nil, want connection refused", url)
	}
}

// TestStartEmbeddedShutdownOnCancelledContext pins that a cancelled ctx aborts
// startup rather than blocking the full readiness window during shutdown
// racing boot.
func TestStartEmbeddedShutdownOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := StartEmbedded(ctx, EmbeddedOptions{StoreDir: t.TempDir()}, discardLogger()); err == nil {
		t.Fatal("StartEmbedded(cancelled ctx) = nil, want an error")
	}
}
