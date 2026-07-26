package mcp

import (
	"context"
	"os"
	"testing"
	"time"

	protocol "github.com/samcharles93/archie-core/internal/tools/mcp"
)

func TestDesktopCommanderClientCompatibility(t *testing.T) {
	if os.Getenv("ARCHIE_TEST_DESKTOP_COMMANDER") != "1" {
		t.Skip("set ARCHIE_TEST_DESKTOP_COMMANDER=1 to run the external MCP integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	transport := protocol.NewStdioTransport(protocol.StdioTransportConfig{
		Command: "npx",
		Args: []string{
			"-y",
			"@wonderwhy-er/desktop-commander@0.2.46",
			"--no-onboarding",
		},
		Dir: "../../..",
	})
	provider := New("desktop-commander", transport)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start Desktop Commander through MCP client: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := provider.Stop(stopCtx); err != nil {
			t.Errorf("stop Desktop Commander: %v", err)
		}
	}()

	entries, err := provider.Discover(ctx)
	if err != nil {
		t.Fatalf("discover Desktop Commander tools: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("Desktop Commander advertised no tools")
	}
	required := map[string]bool{
		"mcp.desktop_commander.start_process":         false,
		"mcp.desktop_commander.interact_with_process": false,
		"mcp.desktop_commander.read_process_output":   false,
	}
	for _, entry := range entries {
		if _, ok := required[entry.Name]; ok {
			required[entry.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Errorf("Desktop Commander did not advertise required shell tool %q", name)
		}
	}
}
