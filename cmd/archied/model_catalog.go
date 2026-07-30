package main

import (
	"maps"
	"slices"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
)

func applyModelCatalog(cfg *config.Config, snapshot modelcatalog.Snapshot) []string {
	discovered := make(map[string]config.Provider, len(snapshot.Providers))
	var models []string
	for _, provider := range snapshot.Providers {
		discovered[provider.ID] = config.Provider{
			Class: provider.Class, APIKeyEnv: provider.APIKeyEnv, BaseURL: provider.BaseURL,
		}
		for _, model := range provider.Models {
			models = append(models, provider.ID+"/"+model.ID)
		}
	}
	cfg.Providers = mergeProviders(discovered, cfg.Providers)
	for i := range cfg.Identities {
		cfg.Identities[i].Providers = mergeProviders(discovered, cfg.Identities[i].Providers)
	}
	slices.Sort(models)
	return models
}

func mergeProviders(base, overrides map[string]config.Provider) map[string]config.Provider {
	merged := make(map[string]config.Provider, len(base)+len(overrides))
	maps.Copy(merged, base)
	for id, override := range overrides {
		resolved := merged[id]
		if override.Class != "" {
			resolved.Class = override.Class
		}
		if override.APIKeyEnv != "" {
			resolved.APIKeyEnv = override.APIKeyEnv
		}
		if override.BaseURL != "" {
			resolved.BaseURL = override.BaseURL
		}
		merged[id] = resolved
	}
	return merged
}
