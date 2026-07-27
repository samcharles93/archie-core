package main

import (
	"context"
	"testing"
)

func TestChatModelManagerBuildsUniqueCatalogAndSelectsChatDefault(t *testing.T) {
	manager := newChatModelManager(map[string]string{
		"builder": "provider/zeta",
		"chat":    "provider/alpha",
		"review":  "provider/zeta",
		"empty":   "",
	})

	models := manager.Models()
	if len(models) != 2 || models[0] != "provider/alpha" || models[1] != "provider/zeta" {
		t.Fatalf("Models() = %v, want sorted unique model references", models)
	}
	models[0] = "mutated"
	if got := manager.Models()[0]; got != "provider/alpha" {
		t.Fatalf("Models() exposed internal state: first model = %q", got)
	}
	if got := manager.ActiveModel(); got != "provider/alpha" {
		t.Fatalf("ActiveModel() = %q, want chat model", got)
	}
}

func TestChatModelManagerFallsBackToBuilder(t *testing.T) {
	manager := newChatModelManager(map[string]string{
		"builder": "provider/builder",
		"review":  "provider/reviewer",
	})

	if got := manager.ActiveModel(); got != "provider/builder" {
		t.Fatalf("ActiveModel() = %q, want builder fallback", got)
	}
}

func TestChatModelManagerSwitchesOnlyToConfiguredModels(t *testing.T) {
	manager := newChatModelManager(map[string]string{
		"chat":   "provider/one",
		"review": "provider/two",
	})

	if err := manager.SetActiveModel(context.Background(), "provider/two"); err != nil {
		t.Fatalf("SetActiveModel() error = %v", err)
	}
	if got := manager.ActiveModel(); got != "provider/two" {
		t.Fatalf("ActiveModel() = %q, want switched model", got)
	}

	if err := manager.SetActiveModel(context.Background(), "provider/unknown"); err == nil {
		t.Fatal("SetActiveModel() error = nil, want unknown-model error")
	}
	if got := manager.ActiveModel(); got != "provider/two" {
		t.Fatalf("failed switch changed ActiveModel() to %q", got)
	}
}

func TestChatModelManagerHonorsCancelledContext(t *testing.T) {
	manager := newChatModelManager(map[string]string{
		"chat":   "provider/one",
		"review": "provider/two",
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := manager.SetActiveModel(ctx, "provider/two"); err == nil {
		t.Fatal("SetActiveModel() error = nil, want context cancellation")
	}
	if got := manager.ActiveModel(); got != "provider/one" {
		t.Fatalf("cancelled switch changed ActiveModel() to %q", got)
	}
}

func TestChatModelManagerSeparatesProvidersAndModels(t *testing.T) {
	manager := newChatModelManager(map[string]string{
		"chat":    "openrouter/openai/gpt-5.6",
		"builder": "openai/gpt-5.6",
		"review":  "anthropic/claude-opus",
	})

	providers := manager.Providers()
	if len(providers) != 3 ||
		providers[0] != "anthropic" ||
		providers[1] != "openai" ||
		providers[2] != "openrouter" {
		t.Fatalf("Providers() = %v", providers)
	}
	if got := manager.ActiveProvider(); got != "openrouter" {
		t.Fatalf("ActiveProvider() = %q, want openrouter", got)
	}
	if got := manager.ModelsForProvider("openai"); len(got) != 1 || got[0] != "openai/gpt-5.6" {
		t.Fatalf("openai models = %v", got)
	}
	if got := manager.ModelsForProvider("openrouter"); len(got) != 1 || got[0] != "openrouter/openai/gpt-5.6" {
		t.Fatalf("openrouter models = %v", got)
	}
}

func TestChatModelManagerProviderSwitchSelectsValidModel(t *testing.T) {
	manager := newChatModelManager(map[string]string{
		"chat":    "openrouter/openai/gpt-5.6",
		"builder": "openai/gpt-5.6",
		"review":  "openai/o3",
	})

	if err := manager.SetActiveProvider(context.Background(), "openai"); err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}
	if got := manager.ActiveProvider(); got != "openai" {
		t.Fatalf("ActiveProvider() = %q, want openai", got)
	}
	if got := manager.ActiveModel(); got != "openai/gpt-5.6" {
		t.Fatalf("ActiveModel() = %q, want first configured openai model", got)
	}

	if err := manager.SetActiveProvider(context.Background(), "missing"); err == nil {
		t.Fatal("SetActiveProvider() error = nil, want unknown-provider error")
	}
	if got := manager.ActiveProvider(); got != "openai" {
		t.Fatalf("failed switch changed provider to %q", got)
	}
}

func TestChatModelManagerIncludesExplicitChatCatalog(t *testing.T) {
	manager := newChatModelManager(
		map[string]string{"chat": "openai/gpt-5.6-sol"},
		[]string{
			"openrouter/openai/gpt-5.6-sol",
			"anthropic/claude-opus",
			"openai/gpt-5.6-sol",
		},
	)

	if got := manager.Models(); len(got) != 3 {
		t.Fatalf("Models() = %v, want workflow model plus explicit catalog", got)
	}
	if got := manager.ModelsForProvider("openrouter"); len(got) != 1 ||
		got[0] != "openrouter/openai/gpt-5.6-sol" {
		t.Fatalf("OpenRouter catalog = %v", got)
	}
}
