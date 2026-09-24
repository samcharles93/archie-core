package archied

import (
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	taskactionstore "github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
)

// openSecondStore opens a second, independent *pgstore.TaskDB on a fresh temp
// path so a boot consumer test can prove the composition routes through
// b.stateStore rather than b.st: the two differ by identity, so an assertion
// that a consumer field holds storeB (not storeA) can only pass if the wiring
// reads b.stateStore.
// stateStoreAdapter stands in for the real adapter (*staterpc.Client), which
// fronts both halves of the contract over one connection: the task store and
// the event-capture store. A plain *pgstore.TaskDB no longer satisfies the
// event-capture surfaces, so a test that asserts "everything routes through
// the adapter" needs something that implements them, exactly as the client
// does in production.
type stateStoreAdapter struct {
	*pgstore.TaskDB
	*postgres.EDA
}

func openSecondStore(t *testing.T) *stateStoreAdapter {
	t.Helper()
	st := pgstore.Open(t)
	t.Cleanup(func() { _ = st.Close() })
	return &stateStoreAdapter{TaskDB: st, EDA: pgstore.EDA(t, nil)}
}

// TestBuildDaemonRoutesTaskStoreThroughStateStore proves the daemon's task
// lifecycle consumer resolves its store surfaces from b.stateStore (the State
// Store contract adapter -- always the remote *staterpc.Client after .4.6)
// and not from b.st directly. This is the .4.5 TaskStore-composite swap
// (docs/prds/state-store-contract.md §12 step 6): the daemon's Store field and
// its mapping/binding dispatch surfaces all come from the adapter.
func TestBuildDaemonRoutesTaskStoreThroughStateStore(t *testing.T) {
	storeA := pgstore.Open(t)
	t.Cleanup(func() { _ = storeA.Close() })
	storeB := openSecondStore(t)

	b := &boot{
		cfg:        config.Config{},
		log:        slog.New(slog.DiscardHandler),
		st:         storeA,
		stateStore: storeB,
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
