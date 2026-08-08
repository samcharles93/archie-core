package provider_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolProviderEngineContractExists(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join("provider.go"))
	if err != nil {
		t.Fatalf("read provider.go: %v", err)
	}
	text := string(source)
	for _, required := range []string{
		"type Engine interface",
		"plugin.Module",
		"Discover(context.Context) ([]tools.ToolEntry, error)",
		"type Registry struct",
		"func (r *Registry) Register(engine Engine) error",
		"func (r *Registry) Get(id string) (Engine, bool)",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("provider.go does not contain required typed-family contract %q", required)
		}
	}
}

func TestArchiedWiresTypedProvidersAndExecutableConsumers(t *testing.T) {
	t.Parallel()

	mainPath := filepath.Join("..", "..", "..", "cmd", "archied", "main.go")
	source, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read archied main: %v", err)
	}
	text := string(source)

	// Also check telegram_setup.go, which holds the chat-turn wiring
	// extracted from main.go during the Phase 4 structural refactor.
	tgPath := filepath.Join("..", "..", "..", "cmd", "archied", "telegram_setup.go")
	tgSource, tgErr := os.ReadFile(tgPath)
	if tgErr == nil {
		text += string(tgSource)
	}

	for _, required := range []string{
		"toolprovider.NewRegistry(toolReg)",
		"memorytoolprovider.New(memManager)",
		"configuredMCPProvider(srv)",
		"capabilityHost.Register(providerRegistry)",
		// The chat turn builds its toolset from the registry before the
		// system prompt is rendered, so the prompt can advertise exactly
		// the tools the model is handed. After the Phase 4 structural
		// refactor these calls live in telegram_setup.go.
		"chatGenerateOptions(ctx,",
		"toolSummaries(options.Tools)",
		"agentexec.NewInProcessRunner(llm, log, toolReg)",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("archied wiring not found: %q", required)
		}
	}
	if strings.Contains(text, ".RegisterTools(ctx, toolReg)") {
		t.Error("composition bypasses the typed provider registry with direct MCP registration")
	}
	if strings.Contains(text, "memManager.GetToolSchemas()") {
		t.Error("composition bypasses the typed memory tool provider")
	}
}
