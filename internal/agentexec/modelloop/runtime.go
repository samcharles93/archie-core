package modelloop

import (
	"os"

	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// NewRuntime builds a provider runtime for interactive chat or a worker-local
// workflow stage. Nil means no providers were configured. catalogPath is the
// models.dev snapshot the service already cached; it supplies each model's
// reasoning flag so requests use the parameters the model accepts. Empty or
// unreadable leaves the runtime without model metadata.
func NewRuntime(providers map[string]agentrun.Provider, catalogPath string) *runtime.Runtime {
	if len(providers) == 0 {
		return nil
	}
	runtime.RegisterBuiltinClasses()
	catalog := make(map[string]runtime.ProviderConfig, len(providers))
	for name, provider := range providers {
		cfg := runtime.ProviderConfig{ID: name, Class: provider.Class, BaseURL: provider.BaseURL}
		if provider.APIKeyEnv == "" {
			cfg.Auth = runtime.AuthConfig{Type: runtime.AuthTypeNone}
		} else {
			cfg.Auth = runtime.AuthConfig{APIKeyEnv: provider.APIKeyEnv}
		}
		catalog[name] = cfg
	}
	cfg := runtime.Config{Providers: catalog}
	models := runtime.NewCatalog(runtime.CatalogOptions{})
	if raw, err := os.ReadFile(catalogPath); err == nil && models.LoadFromJSON(raw) == nil {
		return runtime.NewRuntimeWithCatalog(cfg, models)
	}
	return runtime.NewRuntime(cfg)
}
