package archied

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
	"github.com/samcharles93/archie-core/internal/secret"
)

// The State Store seeds provider-settings from the config it booted with, so
// building its secret registry must leave every provider's source reference
// in place rather than rewriting it to a process-local env name.
func TestConfiguredSecretRegistryKeepsProviderReferences(t *testing.T) {
	ref := secret.SecretRef{Engine: "env", Key: "OPENAI_KEY"}
	t.Setenv("OPENAI_KEY", "sk-test")
	cfg := config.Config{Providers: map[string]config.Provider{"openai": {Class: "openai", APIKey: ref}}}

	if _, err := configuredSecretRegistry(&cfg, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	got := cfg.Providers["openai"]
	if got.APIKey != ref || got.APIKeyEnv != "" {
		t.Fatalf("provider rewritten: api_key=%+v api_key_env=%q", got.APIKey, got.APIKeyEnv)
	}
}

func TestStateStoreBootSeedsProviderSourceReference(t *testing.T) {
	url := pgtest.URL(t)
	store := pgstore.On(pgstore.PoolAt(t, url))
	path := filepath.Join(t.TempDir(), "config.toml")
	ref := secret.SecretRef{Engine: "env", Key: "ARCHIE_TEST_PROVIDER_SOURCE"}
	t.Setenv(ref.Key, "provider-secret")
	body := fmt.Sprintf("bot_user = 'archie'\ndatabase_url = %q\n[providers.openai]\nclass = 'openai'\napi_key = {engine = 'env', key = %q}\n", url, ref.Key)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- RunStateStore(ctx, StateStoreOptions{Config: path, Listen: "127.0.0.1:0"}) }()
	deadline := time.After(10 * time.Second)
	for {
		resource, err := store.Resource(t.Context(), controlplane.ProviderSettingsKind)
		if err == nil {
			if strings.Contains(string(resource.Value), "ARCHIE_PROVIDER_") || !strings.Contains(string(resource.Value), ref.Key) {
				t.Fatalf("stored provider settings = %s, want source reference and no derived env name", resource.Value)
			}
			break
		}
		select {
		case bootErr := <-errCh:
			t.Fatalf("RunStateStore failed before provider import: %v", bootErr)
		case <-deadline:
			t.Fatalf("provider settings were not imported: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	cancel()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("RunStateStore shutdown: %v", err)
	}
}
