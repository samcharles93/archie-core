package archied

import (
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
)

func TestApplyModelCatalogNormalizesEveryExecutionScope(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.Provider{
			"openai": {Class: "custom", APIKeyEnv: "CUSTOM_KEY"},
		},
		Identities: []config.IdentityConfig{
			{Name: "one", Providers: map[string]config.Provider{
				"identity-only": {Class: "openai-compatible"},
			}},
		},
	}
	snapshot := modelcatalog.Snapshot{Providers: []modelcatalog.Provider{
		{
			ID: "openai", Class: "openai", APIKeyEnv: "OPENAI_API_KEY",
			BaseURL: "https://api.openai.test/v1",
			Models:  []modelcatalog.Model{{ID: "gpt-tool", ContextWindow: 128_000, MaxOutputTokens: 16_000}},
		},
		{
			ID: "anthropic", Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY",
			Models: []modelcatalog.Model{{ID: "claude-tool"}},
		},
	}}

	models := applyModelCatalog(&cfg, snapshot)

	if got := cfg.Providers["openai"]; got.Class != "custom" || got.APIKeyEnv != "CUSTOM_KEY" {
		t.Fatalf("explicit global provider was overwritten: %#v", got)
	}
	if _, ok := cfg.Providers["anthropic"]; !ok {
		t.Fatal("discovered provider missing from global execution config")
	}
	if _, ok := cfg.Identities[0].Providers["anthropic"]; !ok {
		t.Fatal("discovered provider missing from identity execution config")
	}
	if _, ok := cfg.Identities[0].Providers["identity-only"]; !ok {
		t.Fatal("explicit identity provider was removed")
	}
	wantModels := []string{"anthropic/claude-tool", "openai/gpt-tool"}
	if !reflect.DeepEqual(models, wantModels) {
		t.Fatalf("models = %v, want %v", models, wantModels)
	}
	if got := cfg.ModelLimits["openai/gpt-tool"]; got.ContextWindow != 128_000 || got.MaxOutputTokens != 16_000 {
		t.Fatalf("model limits = %+v, want catalog capacity", got)
	}
}
