package archied

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	taskactionstore "github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// openSecondStore opens a second, independent *store.Store on a fresh temp
// path so a boot consumer test can prove the composition routes through
// b.stateStore rather than b.st: the two differ by identity, so an assertion
// that a consumer field holds storeB (not storeA) can only pass if the wiring
// reads b.stateStore.
func openSecondStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "state-store.db"))
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestBuildDaemonRoutesTaskStoreThroughStateStore proves the daemon's task
// lifecycle consumer resolves its store surfaces from b.stateStore (the State
// Store contract adapter -- always the remote *staterpc.Client after .4.6)
// and not from b.st directly. This is the .4.5 TaskStore-composite swap
// (docs/prds/state-store-contract.md §12 step 6): the daemon's Store field and
// its mapping/binding dispatch surfaces all come from the adapter.
func TestBuildDaemonRoutesTaskStoreThroughStateStore(t *testing.T) {
	storeA, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "store-a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB := openSecondStore(t)

	b := &boot{
		cfg:        config.Config{},
		log:        slog.New(slog.DiscardHandler),
		st:         storeA,
		stateStore: storeB,
		web:        &webui.Server{},
	}
	b.buildDaemon()

	if b.d.Store != storeB {
		t.Fatalf("daemon Store = %p, want b.stateStore (%p); task lifecycle did not route through the State Store adapter", b.d.Store, storeB)
	}
	if b.d.Mappings != storeB {
		t.Fatalf("daemon Mappings = %p, want b.stateStore (%p)", b.d.Mappings, storeB)
	}
	if b.d.Bindings != storeB {
		t.Fatalf("daemon Bindings = %p, want b.stateStore (%p)", b.d.Bindings, storeB)
	}
	if b.d.BindingDispatcher != storeB {
		t.Fatalf("daemon BindingDispatcher = %p, want b.stateStore (%p)", b.d.BindingDispatcher, storeB)
	}
	if b.d.BindingTaskCreator != storeB {
		t.Fatalf("daemon BindingTaskCreator = %p, want b.stateStore (%p)", b.d.BindingTaskCreator, storeB)
	}
}

// TestSetupObservabilityRoutesTaskStoreThroughStateStore proves the dashboard's
// task store (web.Store) is wired from b.stateStore (the State Store contract
// adapter), not b.st, so the webui reaches the same contract adapter the
// daemon uses.
func TestSetupObservabilityRoutesTaskStoreThroughStateStore(t *testing.T) {
	storeA, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "store-a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB := openSecondStore(t)

	logFeed := logging.NewFeed(16)
	b := &boot{
		cfg:         config.Config{},
		log:         slog.New(slog.DiscardHandler),
		st:          storeA,
		stateStore:  storeB,
		doc:         &configuration.Document{},
		agentStatus: &daemon.AgentStatus{},
		logFeed:     logFeed,
		taskLogs:    logging.NewTaskRegistry(filepath.Join(t.TempDir(), "logs"), logFeed, logging.TaskSinkOptions{}),
	}
	t.Cleanup(b.cleanup)
	b.setupObservability(t.Context())

	if b.web.Store != storeB {
		t.Fatalf("webui Store = %p, want b.stateStore (%p); dashboard task store did not route through the State Store adapter", b.web.Store, storeB)
	}
	// Capture/mapping/binding surfaces resolve from the same adapter.
	if b.web.Captures != storeB {
		t.Fatalf("webui Captures = %p, want b.stateStore (%p)", b.web.Captures, storeB)
	}
	if b.web.Mappings != storeB {
		t.Fatalf("webui Mappings = %p, want b.stateStore (%p)", b.web.Mappings, storeB)
	}
}

// TestTaskActionsRoutesThroughStateStore proves the daemon's operator task
// action service wraps b.stateStore (the contract adapter), so an operator
// approve/cancel/archive action mutates the same store the daemon lifecycle
// uses.
func TestTaskActionsRoutesThroughStateStore(t *testing.T) {
	storeB := openSecondStore(t)

	b := &boot{
		cfg:        config.Config{},
		log:        slog.New(slog.DiscardHandler),
		stateStore: storeB,
	}
	svc := b.taskActions()
	shadow, ok := svc.Store.(taskactionstore.Store)
	if !ok {
		t.Fatalf("taskactions Service.Store = %T, want taskactionstore.Store", svc.Store)
	}
	if shadow.TaskStore != storeB {
		t.Fatalf("taskactions TaskStore = %p, want b.stateStore (%p)", shadow.TaskStore, storeB)
	}
}
