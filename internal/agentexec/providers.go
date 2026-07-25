package agentexec

import "github.com/samcharles93/archie-core/internal/config"

// ProvidersFromConfig converts the daemon's configured LLM providers into
// the wire-safe Provider map sent to an agent runner. It carries only
// class/env-var-name/base-URL  --  never the resolved API key.
func ProvidersFromConfig(providers map[string]config.Provider) map[string]Provider {
	out := make(map[string]Provider, len(providers))
	for name, p := range providers {
		out[name] = Provider{Class: p.Class, APIKeyEnv: p.APIKeyEnv, BaseURL: p.BaseURL}
	}
	return out
}
