package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools"
)

// fakeSender is a Transport double that scripts responses by JSON-RPC
// method name, so client tests never spawn a real subprocess. Safe for
// concurrent use  --  RegisterTools-produced handlers are meant to be
// called concurrently by real callers, and tests exercise that.
type fakeSender struct {
	// responses maps a request method to the raw response body to return.
	responses map[string][]byte
	// errors maps a request method to an error to return instead.
	errors map[string]error

	mu sync.Mutex
	// calls records every request body sent, in order.
	calls []Message
}

func (f *fakeSender) Send(_ context.Context, body []byte) ([]byte, error) {
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, msg)
	f.mu.Unlock()
	if err, ok := f.errors[msg.Method]; ok {
		return nil, err
	}
	if resp, ok := f.responses[msg.Method]; ok {
		return resp, nil
	}
	return nil, errors.New("fakeSender: no scripted response for method " + msg.Method)
}

func (f *fakeSender) Notify(_ context.Context, body []byte) error {
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return err
	}
	f.mu.Lock()
	f.calls = append(f.calls, msg)
	f.mu.Unlock()
	return f.errors[msg.Method]
}

func newTestClient(f *fakeSender) *Client {
	return NewClient(f, "test-server")
}

func TestInitializeSendsInitializeThenInitializedNotification(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"initialize": []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"widget-server","version":"1.0.0"},"capabilities":{}}}`),
		},
	}
	c := newTestClient(f)

	result, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerInfo.Name != "widget-server" {
		t.Errorf("ServerInfo.Name = %q, want widget-server", result.ServerInfo.Name)
	}
	if len(f.calls) != 2 {
		t.Fatalf("Send called %d times, want 2 (initialize + notifications/initialized)", len(f.calls))
	}
	if f.calls[0].Method != "initialize" {
		t.Errorf("call[0].Method = %q, want initialize", f.calls[0].Method)
	}
	if f.calls[1].Method != "notifications/initialized" {
		t.Errorf("call[1].Method = %q, want notifications/initialized", f.calls[1].Method)
	}
	if len(f.calls[1].ID) != 0 {
		t.Error("notifications/initialized must be a notification (no id)")
	}
}

func TestInitializeReturnsErrorOnTransportFailure(t *testing.T) {
	f := &fakeSender{errors: map[string]error{"initialize": errors.New("boom")}}
	c := newTestClient(f)

	_, err := c.Initialize(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInitializeReturnsErrorOnRPCError(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"initialize": []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"init failed"}}`),
		},
	}
	c := newTestClient(f)

	_, err := c.Initialize(context.Background())
	if err == nil {
		t.Fatal("expected error from RPC error response")
	}
}

func TestListToolsParsesToolSchemas(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[
				{"name":"search_repos","description":"Search repositories","inputSchema":{"type":"object","properties":{"query":{"type":"string"}}}},
				{"name":"get_issue","description":"Get an issue","inputSchema":{"type":"object"}}
			]}}`),
		},
	}
	c := newTestClient(f)

	toolList, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(toolList) != 2 {
		t.Fatalf("ListTools returned %d tools, want 2", len(toolList))
	}
	if toolList[0].Name != "search_repos" {
		t.Errorf("tools[0].Name = %q, want search_repos", toolList[0].Name)
	}
	if toolList[0].InputSchema["type"] != "object" {
		t.Errorf("tools[0].InputSchema[type] = %v, want object", toolList[0].InputSchema["type"])
	}
}

func TestListToolsFollowsPaginationCursor(t *testing.T) {
	calls := 0
	// Two-page response: first call returns nextCursor, second returns none.
	page1 := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"a","inputSchema":{}}],"nextCursor":"page2"}}`)
	page2 := []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"b","inputSchema":{}}]}}`)

	c := NewClient(sendFunc(func(_ context.Context, body []byte) ([]byte, error) {
		calls++
		var msg Message
		_ = json.Unmarshal(body, &msg)
		var params struct {
			Cursor string `json:"cursor,omitempty"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		if params.Cursor == "" {
			return page1, nil
		}
		return page2, nil
	}), "test-server")

	toolList, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("Send called %d times, want 2 (paginated)", calls)
	}
	if len(toolList) != 2 || toolList[0].Name != "a" || toolList[1].Name != "b" {
		t.Fatalf("ListTools = %+v, want [a b] across both pages", toolList)
	}
}

func TestCallToolSendsNameAndArguments(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/call": []byte(`{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"result text"}]}}`),
		},
	}
	c := newTestClient(f)

	result, err := c.CallTool(context.Background(), "search_repos", map[string]any{"query": "widgets"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "result text" {
		t.Fatalf("CallTool result = %+v", result)
	}

	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(f.calls[0].Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Name != "search_repos" {
		t.Errorf("params.Name = %q, want search_repos", params.Name)
	}
	if params.Arguments["query"] != "widgets" {
		t.Errorf("params.Arguments[query] = %v, want widgets", params.Arguments["query"])
	}
}

func TestCallToolReturnsIsErrorWithoutGoError(t *testing.T) {
	// Per MCP spec, a tool-level failure is reported via isError:true in a
	// successful JSON-RPC response, not a JSON-RPC error  --  the LLM needs
	// to see the failure content, not just a Go error.
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/call": []byte(`{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"file not found"}],"isError":true}}`),
		},
	}
	c := newTestClient(f)

	result, err := c.CallTool(context.Background(), "read_file", map[string]any{"path": "/nope"})
	if err != nil {
		t.Fatalf("CallTool returned Go error %v for a tool-level isError result", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
}

func TestRegisterToolsAddsPrefixedEntriesToRegistry(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[
				{"name":"search_repos","description":"Search repos","inputSchema":{"type":"object"}}
			]}}`),
		},
	}
	c := newTestClient(f)
	reg := tools.NewRegistry()

	n, err := c.RegisterTools(context.Background(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RegisterTools registered %d tools, want 1", n)
	}

	entries := reg.All()
	if len(entries) != 1 {
		t.Fatalf("registry has %d entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Name != "mcp.test-server.search_repos" {
		t.Errorf("registered name = %q, want mcp.test-server.search_repos", entry.Name)
	}
	if entry.Toolset != "mcp" {
		t.Errorf("Toolset = %q, want mcp", entry.Toolset)
	}
	if entry.Handler == nil {
		t.Fatal("registered entry has nil Handler")
	}
}

func TestRegisterToolsHandlerInvokesCallTool(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"echoed"}]}}`),
		},
	}
	c := newTestClient(f)
	reg := tools.NewRegistry()

	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}

	entry, ok := reg.Get("mcp.test-server.echo")
	if !ok {
		t.Fatal("mcp.test-server.echo not found in registry")
	}
	out, err := entry.Handler(context.Background(), map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "echoed" {
		t.Errorf("handler output = %v, want \"echoed\"", out)
	}
}

func TestRegisterToolsHandlerSurfacesToolLevelError(t *testing.T) {
	f := &fakeSender{
		responses: map[string][]byte{
			"tools/list": []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"broken","inputSchema":{}}]}}`),
			"tools/call": []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"denied"}],"isError":true}}`),
		},
	}
	c := newTestClient(f)
	reg := tools.NewRegistry()

	if _, err := c.RegisterTools(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	entry, _ := reg.Get("mcp.test-server.broken")
	_, err := entry.Handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected handler to surface isError as a Go error")
	}
}

func TestListToolsReturnsErrorOnMalformedResponse(t *testing.T) {
	f := &fakeSender{responses: map[string][]byte{"tools/list": []byte(`not json`)}}
	c := newTestClient(f)

	if _, err := c.ListTools(context.Background()); err == nil {
		t.Fatal("expected error on malformed response")
	}
}

// sendFunc adapts a function to the Transport interface.
type sendFunc func(ctx context.Context, body []byte) ([]byte, error)

func (f sendFunc) Send(ctx context.Context, body []byte) ([]byte, error) { return f(ctx, body) }
func (f sendFunc) Notify(context.Context, []byte) error                  { return nil }
