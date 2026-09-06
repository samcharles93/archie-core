package archied

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/store"
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

func TestOpenStateStoreAdapterLocalUsesOpenedStore(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "tasks.sqlite"))
	if err != nil {
		t.Fatalf("open temp store: %v", err)
	}
	defer st.Close()

	b := newBootstrap()
	b.st = st
	b.secrets = &secret.Registry{}
	// Empty [services.state].target → the base path: the local *store.Store.
	if err := b.openStateStoreAdapter(); err != nil {
		t.Fatalf("openStateStoreAdapter(local): %v", err)
	}
	if b.stateStore != st {
		t.Fatal("local adapter should be the opened *store.Store")
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
