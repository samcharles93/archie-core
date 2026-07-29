package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
	protocol "github.com/samcharles93/archie-core/internal/tools/mcp"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
)

func TestProviderInitializesDiscoversAndCallsOriginalMCPTool(t *testing.T) {
	transport := newFakeTransport()
	transport.listed = []protocol.ToolSchema{{
		Name:        "Search Repos!",
		Description: "Search repositories",
		InputSchema: tools.JSONSchema{"type": "object"},
	}}
	provider := New("Git Hub/Prod", transport)

	manifest := provider.Manifest()
	if manifest.ID != "mcp.git-hub-prod" {
		t.Fatalf("Manifest().ID = %q, want mcp.git-hub-prod", manifest.ID)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Manifest().Validate() = %v", err)
	}
	if err := provider.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := transport.notifications(); len(got) != 1 || got[0] != "notifications/initialized" {
		t.Fatalf("notifications = %v, want initialized", got)
	}

	entries, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("Discover() count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Name != "mcp.git_hub_prod.search_repos" {
		t.Fatalf("tool name = %q, want sanitized mapped name", entry.Name)
	}
	if entry.Toolset != "mcp" || entry.Description != transport.listed[0].Description {
		t.Fatalf("tool metadata = %+v", entry)
	}

	got, err := entry.Handler(context.Background(), map[string]any{"query": "archie"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "firstsecond" {
		t.Fatalf("handler result = %v, want concatenated MCP text", got)
	}
	name, args := transport.lastCall()
	if name != "Search Repos!" || args["query"] != "archie" {
		t.Fatalf("MCP call = %q %#v, want original name and args", name, args)
	}
	if err := provider.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if transport.State() != protocol.StateStopped {
		t.Fatalf("transport state = %s, want stopped", transport.State())
	}
}

func TestProviderSurfacesMCPToolLevelErrors(t *testing.T) {
	transport := newFakeTransport()
	transport.listed = []protocol.ToolSchema{{Name: "fails"}}
	transport.callResult = protocol.CallToolResult{
		Content: []protocol.ContentBlock{{Type: "text", Text: "denied"}},
		IsError: true,
	}
	provider := New("errors", transport)
	if err := provider.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("Discover() count = %d, want 1", len(entries))
	}

	if _, err := entries[0].Handler(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "denied") {
		t.Fatalf("handler error = %v, want MCP tool failure content", err)
	}
}

func TestProviderPreservesResourceTextAndMultimodalResults(t *testing.T) {
	transport := newFakeTransport()
	transport.listed = []protocol.ToolSchema{{Name: "capture"}}
	transport.callResult = protocol.CallToolResult{Content: []protocol.ContentBlock{
		{Type: "text", Text: "captured"},
		{Type: "resource", Resource: &protocol.ResourceContent{Text: " resource context"}},
		{Type: "image", Data: "aW1hZ2U=", MimeType: "image/png"},
	}}
	provider := New("media", transport)
	if err := provider.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	output, err := entries[0].Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := output.(tools.MultimodalResult)
	if !ok {
		t.Fatalf("handler result type = %T, want tools.MultimodalResult", output)
	}
	if !result.IsMultimodal || result.Summary != "captured resource context" {
		t.Fatalf("multimodal result = %+v", result)
	}
}

func TestProviderStartFailureStopsTransport(t *testing.T) {
	tests := []struct {
		name      string
		startErr  error
		sendErr   error
		wantStops int
	}{
		{name: "transport start", startErr: errors.New("start failed")},
		{name: "initialize", sendErr: errors.New("initialize failed"), wantStops: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newFakeTransport()
			transport.startErr = tt.startErr
			transport.sendErr = tt.sendErr
			provider := New("failure", transport)
			if err := provider.Start(context.Background()); err == nil {
				t.Fatal("Start() succeeded")
			}
			if transport.stopCount != tt.wantStops {
				t.Fatalf("transport Stop() calls = %d, want %d", transport.stopCount, tt.wantStops)
			}
		})
	}
}

func TestProviderRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		server    string
		transport LifecycleTransport
	}{
		{name: "empty name", transport: newFakeTransport()},
		{name: "nil transport", server: "server"},
		{name: "typed nil transport", server: "server", transport: (*fakeTransport)(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := New(tt.server, tt.transport)
			if err := provider.Start(context.Background()); err == nil {
				t.Fatal("Start() succeeded")
			}
			if provider.Health(context.Background()).Status != plugin.HealthUnhealthy {
				t.Fatal("invalid provider health is not unhealthy")
			}
		})
	}
}

func TestProviderHealthMapsTransportState(t *testing.T) {
	tests := []struct {
		state protocol.TransportState
		want  plugin.HealthStatus
	}{
		{state: protocol.StateRunning, want: plugin.HealthHealthy},
		{state: protocol.StateStarting, want: plugin.HealthDegraded},
		{state: protocol.StateStopping, want: plugin.HealthDegraded},
		{state: protocol.StateStopped, want: plugin.HealthUnhealthy},
		{state: protocol.StateError, want: plugin.HealthUnhealthy},
	}
	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			transport := newFakeTransport()
			transport.state = tt.state
			if got := New("health", transport).Health(context.Background()).Status; got != tt.want {
				t.Fatalf("Health().Status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderSatisfiesEngine(t *testing.T) {
	var _ toolprovider.Engine = New("compile", newFakeTransport())
}

// Compile-time checks: HTTP and SSE transports satisfy LifecycleTransport.
func TestTransportsSatisfyLifecycleTransport(t *testing.T) {
	var _ LifecycleTransport = (*protocol.HTTPTransport)(nil)
	var _ LifecycleTransport = (*protocol.SSETransport)(nil)
	// Also check StdioTransport, which already satisfied it.
	var _ LifecycleTransport = (*protocol.StdioTransport)(nil)
}

func TestSanitizeSegmentsAreDeterministicASCII(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		manifest string
		tool     string
	}{
		{name: "spaces symbols and unicode", input: " 42 Répo/搜索 ", manifest: "x-42-r-po", tool: "x_42_r_po"},
		{name: "separator runs", input: "A...B", manifest: "a-b", tool: "a_b"},
		{name: "no identifier content", input: "---", manifest: "", tool: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeManifestSegment(tt.input); got != tt.manifest {
				t.Fatalf("sanitizeManifestSegment(%q) = %q, want %q", tt.input, got, tt.manifest)
			}
			if got := sanitizeToolSegment(tt.input); got != tt.tool {
				t.Fatalf("sanitizeToolSegment(%q) = %q, want %q", tt.input, got, tt.tool)
			}
		})
	}
}

type fakeTransport struct {
	mu sync.Mutex

	state         protocol.TransportState
	startErr      error
	sendErr       error
	stopErr       error
	stopCount     int
	listed        []protocol.ToolSchema
	callResult    protocol.CallToolResult
	notifyMethods []string
	calledName    string
	calledArgs    map[string]any
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		state: protocol.StateStopped,
		callResult: protocol.CallToolResult{
			Content: []protocol.ContentBlock{
				{Type: "text", Text: "first"},
				{Type: "text", Text: "second"},
			},
		},
	}
}

func (t *fakeTransport) Start(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startErr != nil {
		return t.startErr
	}
	t.state = protocol.StateRunning
	return nil
}

func (t *fakeTransport) Stop(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopCount++
	t.state = protocol.StateStopped
	return t.stopErr
}

func (t *fakeTransport) State() protocol.TransportState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *fakeTransport) Send(_ context.Context, body []byte) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sendErr != nil {
		return nil, t.sendErr
	}
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	var result any
	switch request.Method {
	case "initialize":
		result = protocol.InitializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo:      protocol.ServerInfo{Name: "fake", Version: "1.0.0"},
			Capabilities:    protocol.Capabilities{Tools: json.RawMessage(`{}`)},
		}
	case "tools/list":
		result = map[string]any{"tools": t.listed}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		t.calledName = params.Name
		t.calledArgs = params.Arguments
		result = t.callResult
	default:
		return nil, errors.New("unexpected method: " + request.Method)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(protocol.Message{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  resultJSON,
	})
}

func (t *fakeTransport) Notify(_ context.Context, body []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var notification struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &notification); err != nil {
		return err
	}
	t.notifyMethods = append(t.notifyMethods, notification.Method)
	return nil
}

func (t *fakeTransport) notifications() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.notifyMethods...)
}

func (t *fakeTransport) lastCall() (string, map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calledName, t.calledArgs
}
