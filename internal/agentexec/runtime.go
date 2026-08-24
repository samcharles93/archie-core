package agentexec

import "github.com/samcharles93/ai-sdk/runtime"

// NewRuntime builds a provider runtime for interactive chat or a worker-local
// workflow stage. Nil means no providers were configured.
func NewRuntime(providers map[string]Provider) *runtime.Runtime {
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
	return runtime.NewRuntime(runtime.Config{Providers: catalog})
}
