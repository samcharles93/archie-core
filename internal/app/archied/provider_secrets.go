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
	return registry, nil
}

func resolveProviderSecrets(cfg *config.Config, registry *secret.Registry, log *slog.Logger) error {
	if err := resolveProviderMap("root", cfg.Providers, registry, log); err != nil {
		return err
	}
	for i := range cfg.Identities {
		if err := resolveProviderMap(cfg.Identities[i].Name, cfg.Identities[i].Providers, registry, log); err != nil {
			return fmt.Errorf("identity %q: %w", cfg.Identities[i].Name, err)
		}
	}
	return nil
}

// resolveProviderMap exports each provider's resolved api_key into a private
// environment variable and rewrites the provider to reference that variable, so
// a key never travels as config data.
//
// A credential that cannot be resolved disables that provider and is reported,
// rather than failing the boot. configuration.md's startup policy is that a
// missing credential disables a capability and only an invalid config stops the
// daemon, and it names LLM keys as the case that must behave that way; the forge
// path was fixed to degrade for the same reason (resolveForge).
//
// A reference naming only one of engine and key is a config error rather than a
// missing credential, so that stays fatal. applyForgeDefaults only ever builds a
// ref with both halves, so a half-named one can only come from a hand-edited
// config.
func resolveProviderMap(scope string, providers map[string]config.Provider, registry *secret.Registry, log *slog.Logger) error {
	for id, provider := range providers {
		switch {
		case provider.APIKey == (secret.SecretRef{}):
			continue
		case provider.APIKey.Engine == "" || provider.APIKey.Key == "":
			return fmt.Errorf("provider %q: api_key must name both an engine and a key, got {engine: %q, key: %q}",
				id, provider.APIKey.Engine, provider.APIKey.Key)
		}

		value, err := registry.Resolve(provider.APIKey)
		if err != nil {
			disableProvider(providers, id, provider, log, "api_key unavailable", err)
			continue
		}
		if value = strings.TrimSpace(value); value == "" {
			disableProvider(providers, id, provider, log, "api_key resolved empty", nil)
			continue
		}
		envName := providerSecretEnvName(scope, id)
		if err := os.Setenv(envName, value); err != nil {
			disableProvider(providers, id, provider, log, "api_key could not be exported", err)
			continue
		}
		provider.APIKey = secret.SecretRef{}
		provider.APIKeyEnv = envName
		providers[id] = provider
	}
	return nil
}

// disableProvider clears a provider's credential so every consumer sees it as
// unconfigured, and says why. The reference is cleared rather than left in
// place: webui's ProviderView derives Configured from it, so keeping an
// unresolvable reference would advertise a provider that cannot be used.
func disableProvider(providers map[string]config.Provider, id string, provider config.Provider, log *slog.Logger, reason string, cause error) {
	provider.APIKey = secret.SecretRef{}
	provider.APIKeyEnv = ""
	providers[id] = provider
	if cause != nil {
		log.Warn("provider disabled: "+reason, "provider", id, "err", cause)
		return
	}
	log.Warn("provider disabled: "+reason, "provider", id)
}

func providerSecretEnvName(scope, providerID string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + providerID))
	return fmt.Sprintf("ARCHIE_PROVIDER_%X_API_KEY", sum[:8])
}

// ResolveProviders resolves cfg's provider credentials the way the daemon does
// at boot, for a tool that calls models outside the daemon.
func ResolveProviders(cfg *config.Config, log *slog.Logger) error {
	secrets, err := configuredSecretRegistry(cfg, log)
	if err != nil {
		return err
	}
	return resolveProviderSecrets(cfg, secrets, log)
}
