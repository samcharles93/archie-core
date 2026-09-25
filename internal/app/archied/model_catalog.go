package archied

import (
	"maps"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/webui"
)

func applyModelCatalog(cfg *config.Config, snapshot modelcatalog.Snapshot) []string {
	discovered := make(map[string]config.Provider, len(snapshot.Providers))
	limits := make(map[string]config.ModelLimits)
	var models []string
	for _, provider := range snapshot.Providers {
		discovered[provider.ID] = config.Provider{
			Class: provider.Class, APIKeyEnv: provider.APIKeyEnv, BaseURL: provider.BaseURL,
		}
		for _, model := range provider.Models {
			ref := provider.ID + "/" + model.ID
			models = append(models, ref)
			limits[ref] = config.ModelLimits{ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens}
		}
	}
	cfg.ModelLimits = limits
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
		if override.APIKey != (secret.SecretRef{}) {
			resolved.APIKey = override.APIKey
		}
		if override.BaseURL != "" {
			resolved.BaseURL = override.BaseURL
		}
		merged[id] = resolved
	}
	return merged
}

// catalogView renders the usable providers for the published config view,
// sorted by id with each provider's model ids sorted, so the document is
// stable across publishes.
func catalogView(snapshot modelcatalog.Snapshot) []webui.CatalogProviderView {
	view := make([]webui.CatalogProviderView, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		models := make([]string, 0, len(provider.Models))
		for _, model := range provider.Models {
			models = append(models, model.ID)
		}
		slices.Sort(models)
		view = append(view, webui.CatalogProviderView{
			ID: provider.ID, Name: provider.Name, Class: provider.Class,
			APIKeyEnv: provider.APIKeyEnv, BaseURL: provider.BaseURL, Models: models,
		})
	}
	slices.SortFunc(view, func(a, b webui.CatalogProviderView) int { return strings.Compare(a.ID, b.ID) })
	return view
}
