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
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestStateStoreServerOptsLoopbackIsInsecure(t *testing.T) {
	opts, loopback, err := stateStoreServerOpts("127.0.0.1:9090", "", &staterpc.TaskGrants{})
	if err != nil {
		t.Fatalf("stateStoreServerOpts(loopback, no token) error: %v", err)
	}
	if !loopback {
		t.Fatal("loopback listener should report loopback")
	}
	if len(opts) != 1 {
		t.Fatalf("loopback listener should install only the keepalive enforcement policy (no token interceptors), got %d server opts", len(opts))
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
	if len(opts) != 3 {
		t.Fatalf("non-loopback listener should install three server options (the keepalive enforcement policy and the unary and stream task-grant interceptors), got %d", len(opts))
	}
}

func TestStateStoreServerOptsMalformedListen(t *testing.T) {
	if _, _, err := stateStoreServerOpts("not-an-address", "", &staterpc.TaskGrants{}); err == nil {
		t.Fatal("malformed listen address should error")
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

// TestStateStoreDepsServeTaskLogs proves the standalone State Store process is
// where a task-log read is served from: the daemon process holds no dashboard
// listener any more, so the service the UI process dials is the only place this
// contract can have a server-side reader, and it must be the daemon's own
// registry over the state directory.
//
// Without this wiring the RPC exists and answers Unavailable for every task,
// which the dashboard reports as "this process cannot read logs" -- honest, but
// still not a working download (archie-core-iaqx).
func TestStateStoreDepsServeTaskLogs(t *testing.T) {
	b := newBootstrap()
	logs := logging.NewTaskRegistry(filepath.Join(t.TempDir(), "logs", "tasks"), logging.NewFeed(10), logging.TaskSinkOptions{})
	b.taskLogs = logs

	deps := b.stateStoreDeps(&staterpc.TaskGrants{})
	if deps.TaskLogs == nil {
		t.Fatal("stateStoreDeps leaves TaskLogs nil; the State Store process is the only server-side reader the dashboard can reach")
	}
	if deps.TaskLogs != storecontract.TaskLogStore(logs) {
		t.Fatalf("TaskLogs = %T, want the daemon's own registry over the state directory", deps.TaskLogs)
	}
	// A boot without a registry must not fabricate one: an empty reader would
	// answer found=false for every attempt, which reads as "the attempt has no
	// log" rather than "this process cannot read logs".
	if plain := (&boot{}).stateStoreDeps(&staterpc.TaskGrants{}); plain.TaskLogs != nil {
		t.Errorf("TaskLogs = %T with no registry on the boot, want nil so the RPC reports unavailability", plain.TaskLogs)
	}
}

// TestStateStoreDepsServePlaybookDispatcher verifies stateStoreDeps lifts the
// opened *store.Store's PlaybookDispatcher surface onto Deps, so the
// standalone State Store has a server for the two playbook RPCs. It does not
// dial those RPCs here -- it inspects the assembled Deps only. The nil check
// is the load-bearing part: a boot without a store must leave
// PlaybookDispatcher nil (the server then answers codes.Unavailable) rather
// than fabricating a dispatcher.
func TestStateStoreDepsServePlaybookDispatcher(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatalf("open temp store: %v", err)
	}
	defer st.Close()

	b := newBootstrap()
	b.st = st
	// The playbook ledger moved to the event-capture store with the rest of
	// the dispatch tables; the task store no longer serves it.
	eda := edastore.OpenTest(t)
	b.eda = eda
	deps := b.stateStoreDeps(&staterpc.TaskGrants{})
	if deps.PlaybookDispatcher == nil {
		t.Fatal("stateStoreDeps leaves PlaybookDispatcher nil; the standalone State Store is the only production server for the playbook dispatch ledger")
	}
	if deps.PlaybookDispatcher != storecontract.PlaybookDispatcher(eda) {
		t.Fatalf("PlaybookDispatcher = %T, want the opened *edastore.Store", deps.PlaybookDispatcher)
	}
	// A boot without a store must not fabricate one: nil keeps the RPCs honest
	// as unavailable rather than depending on a nil receiver.
	if plain := (&boot{}).stateStoreDeps(&staterpc.TaskGrants{}); plain.PlaybookDispatcher != nil {
		t.Errorf("PlaybookDispatcher = %T with no store on the boot, want nil so the RPCs report unavailability", plain.PlaybookDispatcher)
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
