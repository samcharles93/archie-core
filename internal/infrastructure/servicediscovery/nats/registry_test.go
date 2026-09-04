package nats

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"

	"github.com/samcharles93/archie-core/internal/servicediscovery"
)

// discardLogger silences the registry logger in tests, matching the sibling
// eventbus/nats test convention.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// testConfig returns a discovery Config with a short TTL/heartbeat so tests
// exercise expiry quickly without relying on a fixture elsewhere.
func testConfig() Config {
	return Config{TTL: time.Second, Heartbeat: 200 * time.Millisecond}
}

// newServer starts a real in-process NATS server with JetStream enabled. This
// is the established test technique for the NATS infrastructure (see the
// sibling eventbus/nats retention test): a real server, not a mocked client.
func newServer(t *testing.T) *server.Server {
	t.Helper()
	srv := natstest.RunServer(&server.Options{
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
	})
	t.Cleanup(srv.Shutdown)
	return srv
}

// newClient connects a registry client to a fresh server with the given config.
// Cleanup closes the connection after the caller's own cleanups (registered
// later) have run, so a caller can cancel registration contexts first.
func newClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	cfg.URL = newServer(t).ClientURL()
	c, err := Connect(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("Connect = %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// expectEvent reads from a Watch channel until it observes the wanted event,
// discarding unrelated early events (e.g. the rest of an initial snapshot).
func expectEvent(t *testing.T, ch <-chan servicediscovery.Event, want servicediscovery.Event) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("watch channel closed before %s for %s", want.Kind, want.Endpoint.ID)
			}
			if ev == want {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s for %s", want.Kind, want.Endpoint.ID)
		}
	}
}

// TestRegistryResolveNotInstalled pins that a service that was never registered
// resolves to ErrNotInstalled, not an empty roster.
func TestRegistryResolveNotInstalled(t *testing.T) {
	client := newClient(t, testConfig())
	_, err := client.Resolve(context.Background(), "curator")
	if !errors.Is(err, servicediscovery.ErrNotInstalled) {
		t.Fatalf("Resolve(unregistered) = %v, want ErrNotInstalled", err)
	}
}

// TestRegistryWatchNotInstalled pins that watching a never-registered service
// returns ErrNotInstalled at call time.
func TestRegistryWatchNotInstalled(t *testing.T) {
	client := newClient(t, testConfig())
	_, err := client.Watch(context.Background(), "curator")
	if !errors.Is(err, servicediscovery.ErrNotInstalled) {
		t.Fatalf("Watch(unregistered) = %v, want ErrNotInstalled", err)
	}
}

// TestRegistryRegisterResolve covers registration and a single-instance resolve.
func TestRegistryRegisterResolve(t *testing.T) {
	client := newClient(t, testConfig())
	regCtx, regCancel := context.WithCancel(context.Background())
	t.Cleanup(regCancel)

	reg, err := client.Register(regCtx, "curator", servicediscovery.Endpoint{
		ID:      "inst-1",
		Address: "127.0.0.1:5001",
	})
	if err != nil {
		t.Fatalf("Register = %v", err)
	}
	t.Cleanup(func() { _ = reg.Unregister(context.Background()) })

	eps, err := client.Resolve(context.Background(), "curator")
	if err != nil {
		t.Fatalf("Resolve after Register = %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("Resolve length = %d, want 1: %+v", len(eps), eps)
	}
	got := eps[0]
	want := servicediscovery.Endpoint{Service: "curator", ID: "inst-1", Address: "127.0.0.1:5001"}
	if got != want {
		t.Errorf("Resolve endpoint = %+v, want %+v", got, want)
	}
}

// TestRegistryRegisterResolveMultiple proves multiple instances of one service
// resolve together, ordered deterministically by ID.
func TestRegistryRegisterResolveMultiple(t *testing.T) {
	client := newClient(t, testConfig())
	regCtx, regCancel := context.WithCancel(context.Background())
	t.Cleanup(regCancel)

	instances := []servicediscovery.Endpoint{
		{ID: "b", Address: "127.0.0.1:5002"},
		{ID: "a", Address: "127.0.0.1:5001"},
	}
	var regs []*Registration
	for _, ep := range instances {
		reg, err := client.Register(regCtx, "curator", ep)
		if err != nil {
			t.Fatalf("Register %s = %v", ep.ID, err)
		}
		regs = append(regs, reg)
	}
	t.Cleanup(func() {
		for _, r := range regs {
			_ = r.Unregister(context.Background())
		}
	})

	eps, err := client.Resolve(context.Background(), "curator")
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("Resolve length = %d, want 2: %+v", len(eps), eps)
	}
	if eps[0].ID != "a" || eps[1].ID != "b" {
		t.Errorf("Resolve order = %s, %s; want a, b", eps[0].ID, eps[1].ID)
	}
}

// TestRegistryResolveInstalledButDown is the contract's governing semantic: a
// service that registered but has no live endpoint resolves to an empty,
// non-error roster, distinct from ErrNotInstalled.
func TestRegistryResolveInstalledButDown(t *testing.T) {
	client := newClient(t, testConfig())
	regCtx, regCancel := context.WithCancel(context.Background())
	t.Cleanup(regCancel)

	reg, err := client.Register(regCtx, "curator", servicediscovery.Endpoint{
		ID:      "inst-1",
		Address: "127.0.0.1:5001",
	})
	if err != nil {
		t.Fatalf("Register = %v", err)
	}
	if err := reg.Unregister(context.Background()); err != nil {
		t.Fatalf("Unregister = %v", err)
	}

	// The installed marker persists, so the service is still installed; it just
	// has no live endpoints.
	eps, err := client.Resolve(context.Background(), "curator")
	if err != nil {
		t.Fatalf("Resolve after Unregister = %v, want nil (installed but down)", err)
	}
	if len(eps) != 0 {
		t.Fatalf("Resolve length = %d, want 0 for installed-but-down service", len(eps))
	}
	if eps == nil {
		t.Fatal("Resolve returned nil slice, want a non-nil empty slice")
	}

	_, err = client.Resolve(context.Background(), "never-installed")
	if !errors.Is(err, servicediscovery.ErrNotInstalled) {
		t.Fatalf("Resolve(never-installed) = %v, want ErrNotInstalled", err)
	}
}

// TestRegistryWatchJoinLeave covers Join and Leave emission, including the
// initial snapshot and a heartbeat refresh not being re-emitted as a Join.
func TestRegistryWatchJoinLeave(t *testing.T) {
	client := newClient(t, testConfig())

	// Register A first so its entry is part of the initial watch snapshot and
	// the installed marker is present before Watch.
	regCtx, regCancel := context.WithCancel(context.Background())
	t.Cleanup(regCancel)
	regA, err := client.Register(regCtx, "curator", servicediscovery.Endpoint{ID: "a", Address: "127.0.0.1:5001"})
	if err != nil {
		t.Fatalf("Register a = %v", err)
	}
	t.Cleanup(func() { _ = regA.Unregister(context.Background()) })

	watchCtx, watchCancel := context.WithCancel(context.Background())
	t.Cleanup(watchCancel)
	events, err := client.Watch(watchCtx, "curator")
	if err != nil {
		t.Fatalf("Watch = %v", err)
	}

	// Initial snapshot delivers the pre-existing instance as a Join.
	expectEvent(t, events, servicediscovery.Event{
		Kind:     servicediscovery.Join,
		Endpoint: servicediscovery.Endpoint{Service: "curator", ID: "a", Address: "127.0.0.1:5001"},
	})

	// A new instance joining is a Join.
	regB, err := client.Register(regCtx, "curator", servicediscovery.Endpoint{ID: "b", Address: "127.0.0.1:5002"})
	if err != nil {
		t.Fatalf("Register b = %v", err)
	}
	expectEvent(t, events, servicediscovery.Event{
		Kind:     servicediscovery.Join,
		Endpoint: servicediscovery.Endpoint{Service: "curator", ID: "b", Address: "127.0.0.1:5002"},
	})

	// An explicit unregister is a Leave.
	if err := regB.Unregister(context.Background()); err != nil {
		t.Fatalf("Unregister b = %v", err)
	}
	expectEvent(t, events, servicediscovery.Event{
		Kind:     servicediscovery.Leave,
		Endpoint: servicediscovery.Endpoint{Service: "curator", ID: "b"},
	})
}

// TestRegistryWatchHeartbeatExpiry proves that a stopped heartbeat leads to a
// Leave via the bucket TTL (installed-but-down), not to a flapping re-Join of
// the same ID.
func TestRegistryWatchHeartbeatExpiry(t *testing.T) {
	cfg := testConfig()
	cfg.TTL = 600 * time.Millisecond
	cfg.Heartbeat = 100 * time.Millisecond
	client := newClient(t, cfg)

	regCtx, regCancel := context.WithCancel(context.Background())
	t.Cleanup(regCancel)
	reg, err := client.Register(regCtx, "curator", servicediscovery.Endpoint{ID: "exp", Address: "127.0.0.1:5001"})
	if err != nil {
		t.Fatalf("Register = %v", err)
	}
	t.Cleanup(func() { _ = reg.Unregister(context.Background()) })

	watchCtx, watchCancel := context.WithCancel(context.Background())
	t.Cleanup(watchCancel)
	events, err := client.Watch(watchCtx, "curator")
	if err != nil {
		t.Fatalf("Watch = %v", err)
	}
	expectEvent(t, events, servicediscovery.Event{
		Kind:     servicediscovery.Join,
		Endpoint: servicediscovery.Endpoint{Service: "curator", ID: "exp", Address: "127.0.0.1:5001"},
	})

	// Stop the heartbeat; the entry is no longer refreshed and expires via TTL.
	regCancel()

	expectEvent(t, events, servicediscovery.Event{
		Kind:     servicediscovery.Leave,
		Endpoint: servicediscovery.Endpoint{Service: "curator", ID: "exp"},
	})

	// The service is still installed (its marker persists), so Resolve is an
	// empty, non-error roster -- not ErrNotInstalled.
	deadline := time.Now().Add(3 * time.Second)
	for {
		eps, err := client.Resolve(context.Background(), "curator")
		if err != nil {
			t.Fatalf("Resolve after expiry = %v, want nil (installed but down)", err)
		}
		if len(eps) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Resolve still reports %d live endpoints after heartbeat expiry", len(eps))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
