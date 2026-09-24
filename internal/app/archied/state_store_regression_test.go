package archied

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

// startupGrace is how long RunStateStore is given to either refuse the config
// or settle into serving before the test cancels it. It bounds "did startup
// reject us" (fast, deterministic) rather than "is the machine fast enough"
// (I/O-bound, which is what made the previous 200ms budget a coin flip).
const startupGrace = 2 * time.Second

// shutdownGrace bounds how long a cancelled RunStateStore may take to return.
const shutdownGrace = 5 * time.Second

func TestExternalServicesDoNotRequireBridgeNetwork(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.55")
			_, _ = w.Write([]byte("OK"))
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			writeDockerResponse(t, w, []any{})
		default:
			http.Error(w, "bridge discovery must not be required", http.StatusBadRequest)
		}
	}))
	defer api.Close()
	t.Setenv("DOCKER_HOST", api.URL)
	t.Setenv("DOCKER_API_VERSION", "1.55")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	cfg := config.Config{}
	cfg.NATS.Mode = config.NATSModeExternal
	cfg.Containers.Network = "host"
	cfg.Services = config.Services{config.ServiceNameState: {Target: "state.example.test:9090"}}
	pool, _, cleanup := startContainers(t.Context(), cfg, slog.New(slog.DiscardHandler))
	defer cleanup()
	if pool == nil {
		t.Fatal("external services on host networking must construct a worker pool")
	}
}

func TestRunStateStoreAcceptsEnvironmentToken(t *testing.T) {
	t.Setenv("STATE_STORE_TOKEN", "environment-token")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := fmt.Sprintf("bot_user = 'archie'\ndatabase_url = %q\n[forge]\ntype = 'github'\nhost = 'https://github.example.com'\n[[repos]]\nowner = 'acme'\nname = 'widget'\n", pgtest.URL(t))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	// Cancellation is the assertion: RunStateStore must start (the env token
	// satisfies the non-loopback listener) and then return promptly when the
	// context ends. It must NOT be raced against a wall-clock budget -- SQLite
	// schema init is I/O, so a test that gives startup a few hundred
	// milliseconds is a coin flip, not a check. Cancel first, then assert.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- RunStateStore(ctx, StateStoreOptions{Config: path, Listen: "0.0.0.0:0"}) }()

	select {
	case err := <-errCh:
		// Returning before cancellation means startup refused the config, which
		// is the failure this test exists to catch.
		t.Fatalf("RunStateStore returned before cancellation, want it to serve: %v", err)
	case <-time.After(startupGrace):
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("RunStateStore returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(shutdownGrace):
		t.Fatal("RunStateStore did not return within the shutdown grace after cancellation")
	}
}
