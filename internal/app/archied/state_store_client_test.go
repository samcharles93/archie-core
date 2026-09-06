package archied

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
)

func TestComposeStateStoreClientRequiresTarget(t *testing.T) {
	_, _, err := composeStateStoreClient(config.ServiceConnection{}, &secret.Registry{})
	if err == nil {
		t.Fatal("empty target should error")
	}
	if !strings.Contains(err.Error(), "services.state.target is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestComposeStateStoreClientNonLoopbackFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target string
	}{
		{name: "wildcard without token", target: "0.0.0.0:9090"},
		{name: "hostname without token", target: "store.example.com:9090"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := composeStateStoreClient(config.ServiceConnection{Target: tt.target}, &secret.Registry{})
			if err == nil {
				t.Fatalf("non-loopback %q without a token should fail closed (docs/prds/state-store-contract.md §9)", tt.target)
			}
		})
	}
}

func TestComposeStateStoreClientLoopbackSucceeds(t *testing.T) {
	st, cleanup, err := composeStateStoreClient(config.ServiceConnection{Target: "127.0.0.1:9090"}, &secret.Registry{})
	if err != nil {
		t.Fatalf("loopback without token should succeed: %v", err)
	}
	if st == nil {
		t.Fatal("expected a non-nil store adapter")
	}
	if cleanup == nil {
		t.Fatal("expected a cleanup func")
	}
	cleanup() // must not panic or fail; Close() on the adapter is a no-op (§11)
}

func TestComposeStateStoreClientTokenFromTargetToken(t *testing.T) {
	// Non-loopback with a configured token must not fail closed; the interceptor
	// is what carries it, so the client is dialable.
	st, _, err := composeStateStoreClient(config.ServiceConnection{Target: "0.0.0.0:9090", TargetToken: "secret"}, &secret.Registry{})
	if err != nil {
		t.Fatalf("non-loopback with a token should succeed: %v", err)
	}
	if st == nil {
		t.Fatal("expected a non-nil store adapter")
	}
}

func TestOpenStateStoreAdapterRequiresTarget(t *testing.T) {
	b := newBootstrap()
	b.secrets = &secret.Registry{}
	// After .4.6 the daemon and gateway no longer own archie.db in-process, so
	// an empty [services.state].target is a composition error, not the old
	// local *store.Store default (docs/prds/state-store-contract.md §12 step 7).
	if err := b.openStateStoreAdapter(); err == nil {
		t.Fatal("empty services.state.target should error: the in-process store path is deleted")
	}
}

func TestOpenStateStoreAdapterRemoteFailsClosed(t *testing.T) {
	b := newBootstrap()
	b.secrets = &secret.Registry{}
	b.cfg.Services.State = config.ServiceConnection{Target: "0.0.0.0:9090"}
	if err := b.openStateStoreAdapter(); err == nil {
		t.Fatal("non-loopback without a token should fail closed")
	}
}

// TestStateStoreSingleOwnerComposition proves the single-owner SQLite
// invariant at the composition root: both production consumers (the daemon
// Run path and the gateway RunGateway) resolve their task-store surface from
// openStateStoreAdapter, which REQUIRES [services.state].target and never
// opens archie.db directly. After .4.6 the in-process store path is deleted
// (docs/prds/state-store-contract.md §12 step 7), so an empty target is a
// startup error rather than the old local default -- if it were not, the
// daemon or gateway would open the single SQLite file behind the store
// service's back, violating no-dual-store-ownership.
func TestStateStoreSingleOwnerComposition(t *testing.T) {
	b := newBootstrap()
	b.secrets = &secret.Registry{}

	// The daemon openStores path must not construct a local *store.Store.
	// openStores only resolves secrets and the forge client, then opens chat
	// sessions; the task store surface comes from the remote adapter, so no
	// archie.db task file is touched. This asserts openStateStoreAdapter (the
	// only task-store constructor) rejects an empty target.
	if err := b.openStateStoreAdapter(); err == nil {
		t.Fatal("empty services.state.target must error: the in-process store path is deleted (single owner = archie-state-store)")
	}
	if b.stateStore != nil {
		t.Fatalf("stateStore = %p, want nil (no local store opened)", b.stateStore)
	}
}
