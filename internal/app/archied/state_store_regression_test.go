package archied

import (
	"context"
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
)

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
	cfg.Services.State.Target = "state.example.test:9090"
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
	data := fmt.Sprintf("bot_user = 'archie'\ndb_path = %q\n[forge]\ntype = 'github'\nhost = 'https://github.example.com'\n[[repos]]\nowner = 'acme'\nname = 'widget'\n", filepath.Join(dir, "state.db"))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := RunStateStore(ctx, StateStoreOptions{Config: path, Listen: "0.0.0.0:0"}); err != nil {
		t.Fatalf("environment token should permit startup: %v", err)
	}
}
