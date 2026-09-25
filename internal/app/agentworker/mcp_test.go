package agentworker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// TestArchieAgentMCPOverStdio drives the real archie-agent binary's mcp
// subcommand with the MCP SDK's client, the way a harness CLI does.
func TestArchieAgentMCPOverStdio(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "archie-agent")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "github.com/samcharles93/archie-core/cmd/archie-agent")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build archie-agent: %v\n%s", err, out)
	}
	spec, _ := json.Marshal([]agentexec.CaptureTool{{
		Name: "record_finding", Description: "Record one finding.",
		Parameters:     json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
		RequiredFields: []string{"title"},
	}})
	specPath, captures := filepath.Join(dir, "tools.json"), filepath.Join(dir, "captures.jsonl")
	if err := os.WriteFile(specPath, spec, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(captures, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	transport := &mcp.CommandTransport{Command: exec.CommandContext(t.Context(), bin, "mcp", "-spec", specPath, "-captures", captures)}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "harness", Version: "0"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "record_finding", Arguments: map[string]any{"title": "nil deref"}})
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := res.Content[0].(*mcp.TextContent); !ok || !strings.Contains(text.Text, "recorded") {
		t.Fatalf("reply %+v", res.Content)
	}
	_ = session.Close()
	raw, err := os.ReadFile(captures)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"title":"nil deref"`) {
		t.Fatalf("captures file %q, want the recorded call", raw)
	}
}
