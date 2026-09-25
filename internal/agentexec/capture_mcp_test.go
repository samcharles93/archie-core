package agentexec

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var findingTool = CaptureTool{
	Name:           "record_finding",
	Description:    "Record one finding.",
	Parameters:     json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"severity":{"type":"string"}},"required":["title"]}`),
	RequiredFields: []string{"title"},
	MaxCalls:       2,
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func connectCaptureServer(t *testing.T, sink *lockedBuffer) *mcp.ClientSession {
	t.Helper()
	serverT, clientT := mcp.NewInMemoryTransports()
	go func() { _ = ServeCaptureMCP(t.Context(), []CaptureTool{findingTool}, sink, serverT) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callText(t *testing.T, s *mcp.ClientSession, args any) string {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: "record_finding", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	return out.String()
}

func TestCaptureMCPServesTheStageCaptureTools(t *testing.T) {
	sink := &lockedBuffer{}
	s := connectCaptureServer(t, sink)
	tools, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "record_finding" || tools.Tools[0].Description != "Record one finding." {
		t.Fatalf("tools %+v, want the stage's one capture tool", tools.Tools)
	}

	if got := callText(t, s, map[string]any{"title": "nil deref", "severity": "important"}); !strings.Contains(got, "recorded") {
		t.Fatalf("valid call replied %q", got)
	}
	if got := callText(t, s, map[string]any{"severity": "nitpick"}); !strings.Contains(got, "rejected") {
		t.Fatalf("call missing a required field replied %q", got)
	}
	if got := callText(t, s, map[string]any{"title": "second"}); !strings.Contains(got, "recorded") {
		t.Fatalf("second valid call replied %q", got)
	}
	if got := callText(t, s, map[string]any{"title": "third"}); !strings.Contains(got, "maximum") {
		t.Fatalf("call past MaxCalls replied %q", got)
	}
	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("sink has %d records, want only the 2 accepted calls:\n%s", len(lines), sink.String())
	}
}

func TestReadCapturesRevalidatesEverything(t *testing.T) {
	// The captures file is writable by the harness user, so anything in it
	// may be forged. Only records that pass the same validation survive.
	path := filepath.Join(t.TempDir(), "captures.jsonl")
	content := strings.Join([]string{
		`{"tool":"record_finding","args":{"title":"real"}}`,
		`{"tool":"record_finding","args":{"severity":"no title"}}`,
		`{"tool":"grant_admin","args":{"title":"x"}}`,
		`not json at all`,
		`{"tool":"record_finding","args":{"title":"second"}}`,
		`{"tool":"record_finding","args":{"title":"over the cap"}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	captures, err := readCaptures(path, []CaptureTool{findingTool})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, raw := range captures["record_finding"] {
		var v struct{ Title string }
		_ = json.Unmarshal(raw, &v)
		titles = append(titles, v.Title)
	}
	if !slices.Equal(titles, []string{"real", "second"}) || len(captures) != 1 {
		t.Fatalf("captures %v (titles %v), want only the two valid record_finding calls", captures, titles)
	}
}

func TestHarnessCollectsCapturesThroughTheMCPServer(t *testing.T) {
	f := newFixture(t, "capture")
	f.req.CaptureTools = []CaptureTool{findingTool}
	f.req.Harness.MCPConfig = []string{"--mcp-config", "{{.MCPConfig}}"}
	res, _ := f.run(t, t.Context())
	if res.Status != StatusPassed {
		t.Fatalf("status %q (%s)", res.Status, res.Detail)
	}
	if got := len(res.Captures["record_finding"]); got != 1 {
		t.Fatalf("captures %v, want the one valid record the harness made", res.Captures)
	}
	inv := f.invocations(t)
	if len(inv) != 1 || !strings.Contains(inv[0], "--mcp-config ") {
		t.Fatalf("invocations %q, want the MCP config registered", inv)
	}
}

func TestHarnessRefusesCaptureToolsWithoutMCP(t *testing.T) {
	f := newFixture(t, "edit")
	f.req.CaptureTools = []CaptureTool{findingTool}
	if _, err := NewHarnessRunner(nil).Run(t.Context(), f.workspace, f.req, nil); err == nil {
		t.Fatal("a stage with capture tools ran on a harness with no MCP registration; its results would be lost")
	}
}
