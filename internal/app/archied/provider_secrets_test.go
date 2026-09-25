package archied

import (
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
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
