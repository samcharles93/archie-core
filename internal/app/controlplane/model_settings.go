package controlplane

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
)

const (
	ProviderSettingsKind     = "provider-settings"
	ModelRoleAssignmentsKind = "model-role-assignments"
)

type providerDocument struct {
	Class         string           `json:"class"`
	APIKeyEnv     string           `json:"api_key_env,omitempty"`
	APIKey        config.SecretRef `json:"api_key_ref"`
	BaseURL       string           `json:"base_url,omitempty"`
	HasCredential bool             `json:"has_credential"`
}

func modelDefinitions() []Definition {
	return []Definition{
		{Kind: ProviderSettingsKind, Title: "Providers", ApplyMode: "restart-required", Document: map[string]providerDocument{}, Seed: seedProviders, Validate: validateProviders},
		{Kind: ModelRoleAssignmentsKind, Title: "Model role assignments", ApplyMode: "restart-required", Document: map[string]string{}, Seed: func(cfg config.Config) any { return cfg.Models }, Validate: validateModelRoles},
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

func validateModelRoles(input []byte) error {
	return validateAs(input, func(models map[string]string) error {
		for role, model := range models {
			if strings.TrimSpace(role) == "" || !strings.Contains(model, "/") {
				return fmt.Errorf("model role %q must reference provider/model", role)
			}
		}
		return nil
	})
}
