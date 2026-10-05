package modelloop

import (
	"strings"

	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// NewRuntime builds a provider runtime for interactive chat or a worker-local
// workflow stage. Nil means no providers were configured. limits is keyed
// "provider/model"; its Reasoning flag tells the runtime which models need
// max_completion_tokens instead of max_tokens.
func NewRuntime(providers map[string]agentrun.Provider, limits map[string]config.ModelLimits) *runtime.Runtime {
	if len(providers) == 0 {
		return nil
	}
	runtime.RegisterBuiltinClasses()
	catalog := make(map[string]runtime.ProviderConfig, len(providers))
	for name, provider := range providers {
		cfg := runtime.ProviderConfig{ID: name, Class: provider.Class, BaseURL: provider.BaseURL, Models: reasoningModels(name, limits)}
		if provider.APIKeyEnv == "" {
			cfg.Auth = runtime.AuthConfig{Type: runtime.AuthTypeNone}
		} else {
			cfg.Auth = runtime.AuthConfig{APIKeyEnv: provider.APIKeyEnv}
		}
		catalog[name] = cfg
	}
	return runtime.NewRuntime(runtime.Config{Providers: catalog})
}

func reasoningModels(provider string, limits map[string]config.ModelLimits) []runtime.ModelConfig {
	var models []runtime.ModelConfig
	for ref, l := range limits {
		if id, ok := strings.CutPrefix(ref, provider+"/"); ok && l.Reasoning {
			models = append(models, runtime.ModelConfig{ID: id, Reasoning: true})
		}
	}
	return models
}
