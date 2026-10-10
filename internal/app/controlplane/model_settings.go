package controlplane

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
)

const (
	ProviderSettingsKind = "provider-settings"
	ModelAliasesKind     = "model-aliases"
)

var bootDerivedProviderEnv = regexp.MustCompile(`^ARCHIE_PROVIDER_[0-9A-F]{16}_API_KEY$`)

type providerDocument struct {
	Class         string           `json:"class"`
	APIKeyEnv     string           `json:"api_key_env,omitempty"`
	APIKey        config.SecretRef `json:"api_key_ref"`
	BaseURL       string           `json:"base_url,omitempty"`
	HasCredential bool             `json:"has_credential"`
}

func modelDefinitions() []Definition {
	return []Definition{
		{Kind: ProviderSettingsKind, Title: "Providers", ApplyMode: "live", Document: map[string]providerDocument{}, Seed: seedProviders, Validate: validateProviders},
		{Kind: ModelAliasesKind, Title: "Model aliases", ApplyMode: "live", Document: map[string]string{}, Seed: func(cfg config.Config) any { return cfg.Models }, Validate: validateModelAliases},
	}
}

func seedProviders(cfg config.Config) any {
	out := make(map[string]providerDocument, len(cfg.Providers))
	for name, provider := range cfg.Providers {
		out[name] = providerDocument{Class: provider.Class, APIKeyEnv: provider.APIKeyEnv, APIKey: provider.APIKey, BaseURL: provider.BaseURL, HasCredential: provider.APIKeyEnv != "" || provider.APIKey != (config.SecretRef{})}
	}
	return out
}

func validateProviders(input []byte) error {
	return validateAs(input, func(providers map[string]providerDocument) error {
		for name, provider := range providers {
			if err := rejectBootDerivedProviderEnv(name, provider); err != nil {
				return err
			}
			if strings.TrimSpace(name) == "" || strings.TrimSpace(provider.Class) == "" {
				return fmt.Errorf("provider name and class are required")
			}
			if provider.BaseURL != "" {
				parsed, err := url.Parse(provider.BaseURL)
				if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
					return fmt.Errorf("provider %q has invalid base_url", name)
				}
			}
		}
		return nil
	})
}

func rejectBootDerivedProviderEnv(name string, provider providerDocument) error {
	if bootDerivedProviderEnv.MatchString(provider.APIKeyEnv) && provider.APIKey == (config.SecretRef{}) {
		return fmt.Errorf("provider %q api_key_env %q is a boot-derived name; replace provider-settings with the operator's api_key_ref", name, provider.APIKeyEnv)
	}
	return nil
}

var modelAliasName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func validateModelAliases(input []byte) error {
	return validateAs(input, func(models map[string]string) error {
		for alias, model := range models {
			if !modelAliasName.MatchString(alias) {
				return fmt.Errorf("model alias %q must be lowercase letters, digits, '.', '_' or '-'", alias)
			}
			if provider, name, ok := strings.Cut(model, "/"); !ok || provider == "" || name == "" {
				return fmt.Errorf("model alias %q must reference provider/model", alias)
			}
		}
		if _, ok := models[config.DefaultModelAlias]; !ok && len(models) > 0 {
			return fmt.Errorf("model alias %q is required", config.DefaultModelAlias)
		}
		return nil
	})
}
