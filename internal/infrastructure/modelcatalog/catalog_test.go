package modelcatalog

import (
	"testing"

	sdkcatalog "github.com/samcharles93/ai-sdk/catalog"

	"github.com/samcharles93/archie-core/internal/config"
)

func envFrom(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// modelProvider is one catalog entry shaped so usableProvider keeps it: it
// drops any provider with no tool-calling model.
func modelProvider(env []string) catalogProvider {
	return catalogProvider{
		ID:     "test",
		NPM:    "@ai-sdk/openai",
		Env:    env,
		Models: map[string]sdkcatalog.Model{"m": {ID: "m", ToolCall: true}},
	}
}

// TestUsableProviderCredentials pins which provider a credential makes usable.
// A configured provider whose key failed to resolve is disabled, and must not
// borrow an ambient environment variable: that would send a turn with a key
// the operator did not choose and surface as a provider auth error.
func TestUsableProviderCredentials(t *testing.T) {
	tests := []struct {
		name       string
		source     catalogProvider
		configured map[string]config.Provider
		env        map[string]string
		want       bool
		wantEnv    string
	}{
		{
			name:    "unconfigured provider with an ambient key is usable",
			source:  modelProvider([]string{"OPENAI_API_KEY"}),
			env:     map[string]string{"OPENAI_API_KEY": "ambient"},
			want:    true,
			wantEnv: "OPENAI_API_KEY",
		},
		{
			name:   "unconfigured provider with no key is not usable",
			source: modelProvider([]string{"OPENAI_API_KEY"}),
			want:   false,
		},
		{
			name:       "configured provider with a resolved key uses its own env var",
			source:     modelProvider([]string{"OPENAI_API_KEY"}),
			configured: map[string]config.Provider{"test": {APIKeyEnv: "ARCHIE_PROVIDER_1_API_KEY"}},
			env:        map[string]string{"ARCHIE_PROVIDER_1_API_KEY": "resolved", "OPENAI_API_KEY": "ambient"},
			want:       true,
			wantEnv:    "ARCHIE_PROVIDER_1_API_KEY",
		},
		{
			name:       "configured provider whose resolved env var is empty is not usable",
			source:     modelProvider([]string{"OPENAI_API_KEY"}),
			configured: map[string]config.Provider{"test": {APIKeyEnv: "ARCHIE_PROVIDER_1_API_KEY"}},
			env:        map[string]string{"OPENAI_API_KEY": "ambient"},
			want:       false,
		},
		{
			name:       "configured provider disabled by a failed resolution never borrows the ambient key",
			source:     modelProvider([]string{"OPENAI_API_KEY"}),
			configured: map[string]config.Provider{"test": {Disabled: true}},
			env:        map[string]string{"OPENAI_API_KEY": "ambient"},
			want:       false,
		},
		{
			name:       "configured keyless provider stays usable",
			source:     modelProvider(nil),
			configured: map[string]config.Provider{"test": {Class: "ollama"}},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, ok := usableProvider("test", tt.source, Options{
				Configured: tt.configured,
				Getenv:     envFrom(tt.env),
			})
			if ok != tt.want {
				t.Fatalf("usableProvider ok = %v, want %v", ok, tt.want)
			}
			if ok && provider.APIKeyEnv != tt.wantEnv {
				t.Errorf("APIKeyEnv = %q, want %q", provider.APIKeyEnv, tt.wantEnv)
			}
		})
	}
}

// TestMergeProvidersKeepsDisabled covers the other half of a disabled
// provider's lifetime: Apply rewrites cfg.Providers through mergeProviders,
// and a dropped flag would let the next catalog refresh serve the ambient key
// again.
func TestMergeProvidersKeepsDisabled(t *testing.T) {
	merged := mergeProviders(
		map[string]config.Provider{"openai": {Class: "openai", APIKeyEnv: "OPENAI_API_KEY"}},
		map[string]config.Provider{"openai": {Disabled: true}},
	)
	if !merged["openai"].Disabled {
		t.Fatal("mergeProviders dropped Disabled; a refresh would serve the ambient key")
	}
}
