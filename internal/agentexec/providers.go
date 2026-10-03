package agentexec

import (
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// ProvidersFromConfig converts the daemon's configured LLM providers into
// the wire-safe Provider map sent to an agent runner. It carries only
// class/env-var-name/base-URL -- never the resolved API key.
func ProvidersFromConfig(providers map[string]config.Provider) map[string]agentrun.Provider {
	out := make(map[string]agentrun.Provider, len(providers))
	for name, p := range providers {
		out[name] = agentrun.Provider{Class: p.Class, APIKeyEnv: p.APIKeyEnv, BaseURL: p.BaseURL}
	}
	return out
}
