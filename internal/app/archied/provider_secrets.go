package archied

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/traefik/yaegi/stdlib/unrestricted"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/secret/enginehost"
	"github.com/samcharles93/archie-core/internal/secret/secretextract"
)

func configuredSecretRegistry(cfg *config.Config, log *slog.Logger) (*secret.Registry, error) {
	registry := secret.NewRegistry()
	if cfg.SecretEngineDir != "" {
		// Secret engines are operator-authored code in the same trust
		// domain as the daemon config, and the shipped engines run CLIs
		// (sops, vault, age). They shell out through enginehost.Run (the
		// host-side helper) because yaegi's interpreted os/exec cannot
		// pass an environment to child processes; unrestricted symbols
		// are included for parity with skillscript interpretation.
		loaded, err := registry.LoadDir(cfg.SecretEngineDir, secretextract.Symbols, enginehost.Symbols, unrestricted.Symbols)
		if err != nil {
			return nil, fmt.Errorf("load secret engines from %q: %w", cfg.SecretEngineDir, err)
		}
		log.Info("secret engines loaded", "count", loaded)
	}
	if err := resolveProviderSecrets(cfg, registry); err != nil {
		return nil, fmt.Errorf("resolve provider secrets: %w", err)
	}
	return registry, nil
}

func resolveProviderSecrets(cfg *config.Config, registry *secret.Registry) error {
	if err := resolveProviderMap("root", cfg.Providers, registry); err != nil {
		return err
	}
	for i := range cfg.Identities {
		if err := resolveProviderMap(cfg.Identities[i].Name, cfg.Identities[i].Providers, registry); err != nil {
			return fmt.Errorf("identity %q: %w", cfg.Identities[i].Name, err)
		}
	}
	return nil
}

func resolveProviderMap(scope string, providers map[string]config.Provider, registry *secret.Registry) error {
	for id, provider := range providers {
		if provider.APIKey == (secret.SecretRef{}) {
			continue
		}
		value, err := provider.APIKey.Resolve(registry)
		if err != nil {
			return fmt.Errorf("provider %q: %w", id, err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("provider %q: resolved api_key is empty", id)
		}
		envName := providerSecretEnvName(scope, id)
		if err := os.Setenv(envName, value); err != nil {
			return fmt.Errorf("provider %q: export resolved api_key: %w", id, err)
		}
		provider.APIKey = secret.SecretRef{}
		provider.APIKeyEnv = envName
		providers[id] = provider
	}
	return nil
}

func providerSecretEnvName(scope, providerID string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + providerID))
	return fmt.Sprintf("ARCHIE_PROVIDER_%X_API_KEY", sum[:8])
}
