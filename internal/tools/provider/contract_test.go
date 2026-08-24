package provider_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolProviderEngineContractExists(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("provider.go")
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
	var text strings.Builder
	text.WriteString(string(source))

	// Also check the chat composition files, which hold the chat-turn wiring
	// extracted from main.go during the structural refactor.
	for _, name := range []string{"telegram_setup.go", "chat_turn_model.go", "bootstrap.go"} {
		path := filepath.Join("..", "..", "..", "cmd", "archied", name)
		source, readErr := os.ReadFile(path)
		if readErr == nil {
			text.WriteString(string(source))
		}
	}

	for _, required := range []string{
		// The provider registry, its engines and the consumer wiring live
		// in bootstrap.go (the boot-phase carrier) since the composition
		// root was decomposed; the receiver makes the arg expressions
		// carry the b. prefix there.
		"toolprovider.NewRegistry(b.toolReg)",
		"memorytoolprovider.New(b.memManager)",
		"configuredMCPProvider(srv, cfg.WorkDir)",
		"capabilityHost.Register(b.providerRegistry)",
		// The chat turn builds its toolset from the registry before the
		// system prompt is rendered, so the prompt can advertise exactly
		// the tools the model is handed. After the structural refactor these
		// calls live in the chat composition files.
		"chatGenerateOptions(ctx,",
		"toolSummaries(options.Tools)",
	} {
		if !strings.Contains(text.String(), required) {
			t.Errorf("archied wiring not found: %q", required)
		}
	}
	if strings.Contains(text.String(), ".RegisterTools(ctx, toolReg)") {
		t.Error("composition bypasses the typed provider registry with direct MCP registration")
	}
	if strings.Contains(text.String(), "memManager.GetToolSchemas()") {
		t.Error("composition bypasses the typed memory tool provider")
	}
	if strings.Contains(text.String(), "NewLoopRunner") {
		t.Error("archied constructs a worker-local autonomous runner")
	}
}
