package archied

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	taskactionstore "github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
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

// TestSetupObservabilityKeepsStoreSurfacesOutOfTheDaemonWebui proves the
// cutover's composition rule: the daemon's webui is the configuration
// snapshot's renderer, so it carries the config Holder and provenance and
// no store surfaces -- the dashboard's store-backed reads resolve in the
// UI process from its own State Store client (pinned by archieui's
// compose tests), and a store handle here would be an unused second
// authority.
func TestSetupObservabilityKeepsStoreSurfacesOutOfTheDaemonWebui(t *testing.T) {
	storeA, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "store-a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB := openSecondStore(t)

	b := &boot{
		cfg:         config.Config{},
		log:         slog.New(slog.DiscardHandler),
		st:          storeA,
		stateStore:  storeB,
		doc:         &configuration.Document{},
		agentStatus: &daemon.AgentStatus{},
	}
	t.Cleanup(b.cleanup)
	b.setupObservability(t.Context())

	if b.cfgHolder == nil {
		t.Fatal("cfgHolder is nil; the daemon renders the configuration snapshot it publishes (archie-core-ymut)")
	}
	for name, surface := range map[string]any{
		"Store":         b.web.Store,
		"Captures":      b.web.Captures,
		"Mappings":      b.web.Mappings,
		"Bindings":      b.web.Bindings,
		"CaptureIntake": b.web.CaptureIntake,
	} {
		if surface != nil {
			t.Errorf("webui %s is wired; the daemon serves no dashboard after the cutover", name)
		}
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
