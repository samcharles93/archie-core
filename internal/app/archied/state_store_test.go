package archied

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestStateStoreListenIsLoopback(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		want   bool
	}{
		{"loopback ipv4", "127.0.0.1:9090", true},
		{"loopback ipv6", "[::1]:9090", true},
		{"localhost hostname", "localhost:9090", true},
		{"localhost uppercase", "LOCALHOST:9090", true},
		{"wildcard", "0.0.0.0:9090", false},
		{"private ip", "192.168.1.10:9090", false},
		{"dns name", "store.example.com:9090", false},
		{"empty host", ":9090", false},
		{"malformed", "127.0.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stateStoreListenIsLoopback(tt.listen)
			if tt.name == "malformed" {
				if err == nil {
					t.Fatalf("stateStoreListenIsLoopback(%q) = (%v, nil), want error", tt.listen, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("stateStoreListenIsLoopback(%q): %v", tt.listen, err)
			}
			if got != tt.want {
				t.Fatalf("stateStoreListenIsLoopback(%q) = %v, want %v", tt.listen, got, tt.want)
			}
		})
	}
}

func TestStateStoreServerOptsLoopbackIsInsecure(t *testing.T) {
	opts, loopback, err := stateStoreServerOpts("127.0.0.1:9090", "", &staterpc.TaskGrants{})
	if err != nil {
		t.Fatalf("stateStoreServerOpts(loopback, no token) error: %v", err)
	}
	if !loopback {
		t.Fatal("loopback listener should report loopback")
	}
	if len(opts) != 0 {
		t.Fatalf("loopback listener should have no server opts, got %d", len(opts))
	}
}

func TestStateStoreServerOptsNonLoopbackRequiresToken(t *testing.T) {
	if _, _, err := stateStoreServerOpts("0.0.0.0:9090", "", &staterpc.TaskGrants{}); err == nil {
		t.Fatal("non-loopback listener without a token should fail closed")
	}
	opts, loopback, err := stateStoreServerOpts("0.0.0.0:9090", "secret", &staterpc.TaskGrants{})
	if err != nil {
		t.Fatalf("non-loopback listener with a token error: %v", err)
	}
	if loopback {
		t.Fatal("non-loopback listener should not report loopback")
	}
	if len(opts) != 2 {
		t.Fatalf("non-loopback listener should install two server options (the unary and stream task-grant interceptors), got %d", len(opts))
	}
}

func TestStateStoreServerOptsMalformedListen(t *testing.T) {
	if _, _, err := stateStoreServerOpts("not-an-address", "", &staterpc.TaskGrants{}); err == nil {
		t.Fatal("malformed listen address should error")
	}
}

func TestConstantTimeTokenValidator(t *testing.T) {
	validate := constantTimeTokenValidator("correct-token")
	if !validate("correct-token") {
		t.Fatal("validator should accept the configured token")
	}
	if validate("wrong-token") {
		t.Fatal("validator should reject a different token")
	}
	if validate("") {
		t.Fatal("validator should reject an empty token")
	}
}

// TestServeStateStoreServesContract verifies the standalone composition's
// serveStateStore + stateStoreDeps wiring actually serves the StateStore gRPC
// contract over a real loopback listener: the same service .4.2 serves
// in-process, now extracted into its own process. A read-only StatusCounts
// call proves the service is registered and wired to the opened store.
func TestServeStateStoreServesContract(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "tasks.sqlite"))
	if err != nil {
		t.Fatalf("open temp store: %v", err)
	}
	defer st.Close()

	b := newBootstrap()
	b.st = st
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serveCh := make(chan error, 1)
	go func() { serveCh <- serveStateStore(ctx, listener, b.stateStoreDeps(&staterpc.TaskGrants{}), nil) }()

	conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewStateStoreServiceClient(conn)
	if _, err := client.StatusCounts(t.Context(), &pb.StatusCountsRequest{}); err != nil {
		t.Fatalf("StatusCounts over gRPC: %v", err)
	}

	cancel()
	select {
	case err := <-serveCh:
		if err != nil {
			t.Fatalf("serveStateStore returned: %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("serveStateStore did not return after cancel")
	}
}

// TestStateStoreDataSurvivesRestart is the .4.7 restart/recovery check: a
// task written by one archie-state-store process must still be there for a
// fresh process opening the same archie.db path, proving b.openStateStore
// (RunStateStore's own opener, not a test-only shortcut) round-trips through
// the file rather than an in-memory or per-process store.
func TestStateStoreDataSurvivesRestart(t *testing.T) {
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "archie")}

	first := newBootstrap()
	first.cfg = cfg
	if err := first.openStateStore(t.Context()); err != nil {
		t.Fatalf("first openStateStore: %v", err)
	}
	task, err := first.st.EnqueueChatTask(t.Context(), "acme", "widget", "restart check", "body", "implement", "")
	if err != nil {
		t.Fatalf("EnqueueChatTask: %v", err)
	}
	first.cleanup() // closes the SQLite handle, simulating process exit

	second := newBootstrap()
	second.cfg = cfg
	if err := second.openStateStore(t.Context()); err != nil {
		t.Fatalf("second openStateStore: %v", err)
	}
	defer second.cleanup()

	got, err := second.st.TaskByID(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("TaskByID after restart: %v", err)
	}
	if got == nil {
		t.Fatal("task did not survive a restart against the same db path")
	}
	if got.Title != task.Title {
		t.Fatalf("restarted task title = %q, want %q", got.Title, task.Title)
	}
}

// TestOpenStoresNeverOwnsTaskDB is the .4.7 single-owner-SQLite assertion:
// the daemon/gateway composition path (openStores, run by Run/RunGateway)
// must never populate b.st -- that field is reserved for the standalone
// archie-state-store binary's openStateStore (state_store.go). Regressing
// this would mean archie.db is opened by two processes at once.
func TestOpenStoresNeverOwnsTaskDB(t *testing.T) {
	b := newBootstrap()
	b.cfg = config.Config{DBPath: filepath.Join(t.TempDir(), "archie")}
	if err := b.openStores(t.Context()); err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer b.cleanup()

	if b.st != nil {
		t.Fatal("openStores populated b.st -- the daemon/gateway path must not own archie.db directly (docs/prds/state-store-contract.md §12 step 7)")
	}
}
