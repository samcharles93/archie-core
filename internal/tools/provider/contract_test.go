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
	for _, required := range []string{
		"toolprovider.NewRegistry(toolReg)",
		"memorytoolprovider.New(memManager)",
		"configuredMCPProvider(srv)",
		"capabilityHost.Register(providerRegistry)",
		"chatGenerateOptions(messages, toolReg",
		"agentexec.NewInProcessRunner(llm, log, toolReg)",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("cmd/archied/main.go does not contain tool-provider wiring %q", required)
		}
	}
	if strings.Contains(text, ".RegisterTools(ctx, toolReg)") {
		t.Error("composition bypasses the typed provider registry with direct MCP registration")
	}
	if strings.Contains(text, "memManager.GetToolSchemas()") {
		t.Error("composition bypasses the typed memory tool provider")
	}
}
