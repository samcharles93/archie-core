package servicekit

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/secret"
)

// CatalogRefreshInterval is how often a running service re-reads the catalog.
const CatalogRefreshInterval = time.Hour

// Catalog holds the last model catalog a service loaded.
type Catalog struct {
	cachePath string

	mu       sync.RWMutex
	snapshot modelcatalog.Snapshot
	models   []string
}

// NewCatalog caches the catalog beside the config file at cfgPath.
func NewCatalog(cfgPath string) *Catalog {
	return &Catalog{cachePath: filepath.Join(filepath.Dir(cfgPath), "models.json")}
}

// State returns the loaded snapshot and its model references.
func (c *Catalog) State() (modelcatalog.Snapshot, []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot, c.models
}

// Set records a loaded snapshot and its model references.
func (c *Catalog) Set(snapshot modelcatalog.Snapshot, models []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot, c.models = snapshot, models
}

// Fetch reads the catalog, resolving provider keys through getenv.
func (c *Catalog) Fetch(ctx context.Context, getenv func(string) string, configured map[string]config.Provider) (modelcatalog.Snapshot, error) {
	return modelcatalog.Load(ctx, modelcatalog.Options{CachePath: c.cachePath, Getenv: getenv, Configured: configured})
}

// Every runs fn every interval until ctx ends.
func Every(ctx context.Context, interval time.Duration, fn func()) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fn()
			}
		}
	}()
}

// ApplyModelCatalog layers the catalog's providers and model limits under cfg's
// own and returns the catalog's model references, sorted.
func ApplyModelCatalog(cfg *config.Config, snapshot modelcatalog.Snapshot) []string {
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
			limits[ref] = config.ModelLimits{ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens, Reasoning: model.Reasoning}
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
