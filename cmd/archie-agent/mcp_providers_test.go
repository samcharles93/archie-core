package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
	protocolmcp "github.com/samcharles93/archie-core/internal/tools/mcp"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
)

type fakeMCPEngine struct {
	id          string
	entries     []tools.ToolEntry
	startErr    error
	discoverErr error
	stopErr     error
	starts      int
	discovers   int
	stops       int
}

func (f *fakeMCPEngine) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "mcp." + f.id,
		Name:         f.id,
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"tools"},
	}
}

func (f *fakeMCPEngine) Start(context.Context) error {
	f.starts++
	return f.startErr
}

func (f *fakeMCPEngine) Discover(context.Context) ([]tools.ToolEntry, error) {
	f.discovers++
	return f.entries, f.discoverErr
}

func (f *fakeMCPEngine) Health(context.Context) plugin.Health {
	return plugin.Health{Status: plugin.HealthHealthy}
}

func (f *fakeMCPEngine) Stop(context.Context) error {
	f.stops++
	return f.stopErr
}

func testTool(name string) tools.ToolEntry {
	return tools.ToolEntry{
		Name: name,
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, nil
		},
	}
}

func TestBuildMCPProviderSelectsTransport(t *testing.T) {
	tests := []struct {
		name                string
		server              config.MCPServer
		wantTransport       string
		wantType            any
		wantCommand         string
		wantArgs            []string
		wantEndpoint        string
		wantMessageEndpoint string
		wantHeaders         map[string]string
		wantErr             string
		wantUnknown         bool
	}{
		{
			name:          "default stdio trims command",
			server:        config.MCPServer{Name: " local ", Command: " server ", Args: []string{"--flag"}},
			wantTransport: "stdio",
			wantType:      (*protocolmcp.StdioTransport)(nil),
			wantCommand:   "server",
			wantArgs:      []string{"--flag"},
		},
		{
			name:          "explicit stdio",
			server:        config.MCPServer{Name: "local", Transport: " STDIO ", Command: "server"},
			wantTransport: "stdio",
			wantType:      (*protocolmcp.StdioTransport)(nil),
			wantCommand:   "server",
		},
		{
			name:          "http",
			server:        config.MCPServer{Name: "remote", Transport: "http", URL: " https://example.test/mcp ", Headers: map[string]string{"Authorization": "Bearer test"}},
			wantTransport: "http",
			wantType:      (*protocolmcp.HTTPTransport)(nil),
			wantEndpoint:  "https://example.test/mcp",
			wantHeaders:   map[string]string{"Authorization": "Bearer test"},
		},
		{
			name:          "streamable http alias",
			server:        config.MCPServer{Name: "remote", Transport: "StreamableHTTP", URL: "https://example.test/mcp"},
			wantTransport: "streamablehttp",
			wantType:      (*protocolmcp.HTTPTransport)(nil),
			wantEndpoint:  "https://example.test/mcp",
		},
		{
			name:                "sse",
			server:              config.MCPServer{Name: "events", Transport: "sse", SSEEndpoint: " https://example.test/events ", MessageEndpoint: " https://example.test/messages ", Headers: map[string]string{"X-Test": "value"}},
			wantTransport:       "sse",
			wantType:            (*protocolmcp.SSETransport)(nil),
			wantEndpoint:        "https://example.test/events",
			wantMessageEndpoint: "https://example.test/messages",
			wantHeaders:         map[string]string{"X-Test": "value"},
		},
		{
			name:          "stdio requires command",
			server:        config.MCPServer{Name: "local", Transport: "stdio"},
			wantTransport: "stdio",
			wantErr:       `MCP stdio server "local" requires a command`,
		},
		{
			name:          "http requires url",
			server:        config.MCPServer{Name: "remote", Transport: "http"},
			wantTransport: "http",
			wantErr:       `MCP http server "remote" requires a url`,
		},
		{
			name:          "sse requires endpoint",
			server:        config.MCPServer{Name: "events", Transport: "sse"},
			wantTransport: "sse",
			wantErr:       `MCP sse server "events" requires an sse_endpoint`,
		},
		{
			name:          "unknown transport is a nonfatal skip",
			server:        config.MCPServer{Name: "other", Transport: "websocket"},
			wantTransport: "websocket",
			wantErr:       "unknown MCP transport: websocket",
			wantUnknown:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, transportType, err := buildMCPProvider(tc.server)
			if transportType != tc.wantTransport {
				t.Fatalf("transport = %q, want %q", transportType, tc.wantTransport)
			}
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				if errors.Is(err, errUnknownMCPTransport) != tc.wantUnknown {
					t.Fatalf("errors.Is(err, errUnknownMCPTransport) = %v, want %v", errors.Is(err, errUnknownMCPTransport), tc.wantUnknown)
				}
				if provider != nil {
					t.Fatalf("provider = %T, want nil on invalid configuration", provider)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildMCPProvider() error = %v", err)
			}
			if provider == nil {
				t.Fatal("buildMCPProvider() returned nil provider")
			}
			transport := reflect.ValueOf(provider).Elem().FieldByName("transport")
			if transport.IsNil() {
				t.Fatal("provider transport is nil")
			}
			if got, want := transport.Elem().Type(), reflect.TypeOf(tc.wantType); got != want {
				t.Fatalf("transport type = %v, want %v", got, want)
			}
			assertMCPTransportConfig(t, transport.Elem(), tc.wantCommand, tc.wantArgs, tc.wantEndpoint, tc.wantMessageEndpoint, tc.wantHeaders)
		})
	}
}

func assertMCPTransportConfig(
	t *testing.T,
	transport reflect.Value,
	wantCommand string,
	wantArgs []string,
	wantEndpoint string,
	wantMessageEndpoint string,
	wantHeaders map[string]string,
) {
	t.Helper()
	config := transport.Elem().FieldByName("config")
	assertStringField := func(name, want string) {
		t.Helper()
		if want == "" {
			return
		}
		if got := config.FieldByName(name).String(); got != want {
			t.Errorf("transport config %s = %q, want %q", name, got, want)
		}
	}
	assertStringField("Command", wantCommand)
	if wantEndpoint != "" {
		field := "Endpoint"
		if config.FieldByName(field).Kind() == reflect.Invalid {
			field = "SSEEndpoint"
		}
		assertStringField(field, wantEndpoint)
	}
	assertStringField("MessageEndpoint", wantMessageEndpoint)

	if wantArgs != nil {
		args := config.FieldByName("Args")
		got := make([]string, args.Len())
		for i := range args.Len() {
			got[i] = args.Index(i).String()
		}
		if !reflect.DeepEqual(got, wantArgs) {
			t.Errorf("transport config Args = %v, want %v", got, wantArgs)
		}
	}
	if wantHeaders != nil {
		headers := config.FieldByName("Headers")
		for key, want := range wantHeaders {
			if got := headers.MapIndex(reflect.ValueOf(key)).String(); got != want {
				t.Errorf("transport config Headers[%q] = %q, want %q", key, got, want)
			}
		}
	}
}

func TestStartMCPProviderLifecycle(t *testing.T) {
	startErr := errors.New("start failed")
	discoverErr := errors.New("discover failed")
	log := slog.New(slog.DiscardHandler)

	tests := []struct {
		name           string
		provider       *fakeMCPEngine
		wantErr        error
		wantTools      int
		wantStarts     int
		wantDiscovers  int
		wantStops      int
		wantRegistered []string
	}{
		{
			name:       "start failure does not attempt discovery or stop",
			provider:   &fakeMCPEngine{id: "start", startErr: startErr},
			wantErr:    startErr,
			wantStarts: 1,
		},
		{
			name:          "discovery failure stops started provider",
			provider:      &fakeMCPEngine{id: "discover", discoverErr: discoverErr, stopErr: errors.New("ignored cleanup failure")},
			wantErr:       discoverErr,
			wantStarts:    1,
			wantDiscovers: 1,
			wantStops:     1,
		},
		{
			name: "registration failure is nonfatal",
			provider: &fakeMCPEngine{id: "registration", entries: []tools.ToolEntry{
				testTool("available"),
				{Name: "invalid"},
				testTool("available"),
			}},
			wantTools:      3,
			wantStarts:     1,
			wantDiscovers:  1,
			wantRegistered: []string{"available"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := tools.NewRegistry()
			gotTools, err := startMCPProvider(t.Context(), tc.name, tc.provider, registry, log)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if gotTools != tc.wantTools {
				t.Fatalf("tool count = %d, want %d", gotTools, tc.wantTools)
			}
			if tc.provider.starts != tc.wantStarts || tc.provider.discovers != tc.wantDiscovers || tc.provider.stops != tc.wantStops {
				t.Fatalf("lifecycle counts = start:%d discover:%d stop:%d, want start:%d discover:%d stop:%d", tc.provider.starts, tc.provider.discovers, tc.provider.stops, tc.wantStarts, tc.wantDiscovers, tc.wantStops)
			}
			for _, name := range tc.wantRegistered {
				if _, ok := registry.Get(name); !ok {
					t.Errorf("tool %q was not registered", name)
				}
			}
		})
	}
}

func TestStartMCPProvidersPreservesOptionalProviderBehavior(t *testing.T) {
	configErr := errors.New("invalid config")
	startErr := errors.New("start failed")
	discoverErr := errors.New("discover failed")
	stopErr := errors.New("stop failed")
	providers := map[string]*fakeMCPEngine{
		"start-failure":    {id: "start-failure", startErr: startErr},
		"discover-failure": {id: "discover-failure", discoverErr: discoverErr},
		"valid-one":        {id: "valid-one", entries: []tools.ToolEntry{testTool("shared")}, stopErr: stopErr},
		"valid-two":        {id: "valid-two", entries: []tools.ToolEntry{testTool("shared")}},
	}
	buildCalls := make(map[string]int)
	build := func(server config.MCPServer) (toolprovider.Engine, string, error) {
		buildCalls[server.Name]++
		switch server.Name {
		case "unknown":
			return nil, "websocket", fmt.Errorf("%w: websocket", errUnknownMCPTransport)
		case "invalid":
			return nil, "stdio", configErr
		default:
			return providers[server.Name], "stdio", nil
		}
	}

	servers := []config.MCPServer{
		{Name: "  "},
		{Name: "unknown"},
		{Name: "invalid"},
		{Name: "start-failure"},
		{Name: "discover-failure"},
		{Name: "valid-one"},
		{Name: "valid-two"},
	}
	set, err := startMCPProvidersWith(t.Context(), servers, slog.New(slog.DiscardHandler), build)
	if err != nil {
		t.Fatalf("error = %v, want nil when valid providers remain available", err)
	}
	if set == nil {
		t.Fatal("provider set = nil, want valid providers to remain available")
	}
	if got := len(set.providers); got != 2 {
		t.Fatalf("available providers = %d, want 2", got)
	}
	if _, ok := set.registry.Get("shared"); !ok {
		t.Fatal("tool from valid provider was not available")
	}
	if buildCalls["  "] != 0 {
		t.Fatal("empty-name config reached provider builder")
	}
	if providers["start-failure"].stops != 0 {
		t.Fatal("start failure unexpectedly stopped provider")
	}
	if providers["discover-failure"].stops != 1 {
		t.Fatalf("discovery failure stop count = %d, want 1", providers["discover-failure"].stops)
	}

	set.cleanup(t.Context(), slog.New(slog.DiscardHandler))
	if providers["valid-one"].stops != 1 || providers["valid-two"].stops != 1 {
		t.Fatalf("cleanup stop counts = (%d, %d), want (1, 1)", providers["valid-one"].stops, providers["valid-two"].stops)
	}
}

func TestStartMCPProvidersReturnsNilWithoutAvailableProviders(t *testing.T) {
	firstErr := errors.New("first provider failed")
	secondErr := errors.New("second provider failed")
	tests := []struct {
		name    string
		servers []config.MCPServer
		build   mcpProviderBuilder
		wantErr error
	}{
		{
			name: "no configured servers",
		},
		{
			name:    "unknown transport is skipped without error",
			servers: []config.MCPServer{{Name: "unknown"}},
			build: func(config.MCPServer) (toolprovider.Engine, string, error) {
				return nil, "unknown", errUnknownMCPTransport
			},
		},
		{
			name:    "first provider error is returned",
			servers: []config.MCPServer{{Name: "first"}, {Name: "second"}},
			build: func(server config.MCPServer) (toolprovider.Engine, string, error) {
				if server.Name == "first" {
					return nil, "stdio", firstErr
				}
				return nil, "stdio", secondErr
			},
			wantErr: firstErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			build := tc.build
			if build == nil {
				build = func(config.MCPServer) (toolprovider.Engine, string, error) {
					t.Fatal("builder called without configured servers")
					return nil, "", nil
				}
			}
			set, err := startMCPProvidersWith(t.Context(), tc.servers, slog.New(slog.DiscardHandler), build)
			if set != nil {
				t.Fatalf("provider set = %#v, want nil", set)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
			} else if reflect.ValueOf(err).Pointer() != reflect.ValueOf(tc.wantErr).Pointer() {
				t.Fatalf("error = %v, want exact first error %v", err, tc.wantErr)
			}
		})
	}
}
