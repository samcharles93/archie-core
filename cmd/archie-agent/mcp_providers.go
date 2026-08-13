package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	mcptoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/mcp"
)

var errUnknownMCPTransport = errors.New("unknown MCP transport")

// mcpProviderSet holds the resources needed to clean up MCP providers.
type mcpProviderSet struct {
	providers []toolprovider.Engine
	registry  *tools.Registry
}

type mcpProviderBuilder func(config.MCPServer) (toolprovider.Engine, string, error)

// startMCPProviders creates MCP transports from config, starts them,
// discovers tools, and registers them into a local registry. Returns
// nil when there are no servers configured.
func startMCPProviders(ctx context.Context, servers []config.MCPServer, log *slog.Logger) (*mcpProviderSet, error) {
	return startMCPProvidersWith(ctx, servers, log, buildMCPProvider)
}

func startMCPProvidersWith(
	ctx context.Context,
	servers []config.MCPServer,
	log *slog.Logger,
	build mcpProviderBuilder,
) (*mcpProviderSet, error) {
	if len(servers) == 0 {
		return nil, nil
	}

	set := &mcpProviderSet{registry: tools.NewRegistry()}
	var firstErr error
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			log.Warn("mcp server skipped: empty name")
			continue
		}

		provider, transportType, err := build(server)
		if errors.Is(err, errUnknownMCPTransport) {
			log.Warn("mcp server skipped: unknown transport", "name", name, "transport", transportType)
			continue
		}
		if err != nil {
			log.Warn("mcp server skipped", "name", name, "err", err)
			firstErr = retainFirstError(firstErr, err)
			continue
		}

		toolCount, err := startMCPProvider(ctx, name, provider, set.registry, log)
		if err != nil {
			firstErr = retainFirstError(firstErr, err)
			continue
		}

		set.providers = append(set.providers, provider)
		log.Info("mcp provider started", "name", name, "transport", transportType, "tools", toolCount)
	}

	if len(set.providers) == 0 {
		return nil, firstErr
	}
	return set, nil
}

func buildMCPProvider(server config.MCPServer) (toolprovider.Engine, string, error) {
	name := strings.TrimSpace(server.Name)
	transportType := strings.ToLower(strings.TrimSpace(server.Transport))
	if transportType == "" {
		transportType = "stdio"
	}

	transport, err := mcpTransportForServer(name, transportType, server)
	if err != nil {
		return nil, transportType, err
	}
	return mcptoolprovider.New(name, transport), transportType, nil
}

func mcpTransportForServer(
	name string,
	transportType string,
	server config.MCPServer,
) (mcptoolprovider.LifecycleTransport, error) {
	switch transportType {
	case "stdio":
		command := strings.TrimSpace(server.Command)
		if command == "" {
			return nil, fmt.Errorf("MCP stdio server %q requires a command", name)
		}
		return mcp.NewStdioTransport(mcp.StdioTransportConfig{
			Command: command,
			Args:    append([]string(nil), server.Args...),
		}), nil
	case "http", "streamablehttp":
		url := strings.TrimSpace(server.URL)
		if url == "" {
			return nil, fmt.Errorf("MCP http server %q requires a url", name)
		}
		return mcp.NewHTTPTransport(mcp.HTTPTransportConfig{
			Endpoint: url,
			Headers:  server.Headers,
		}), nil
	case "sse":
		sseEndpoint := strings.TrimSpace(server.SSEEndpoint)
		if sseEndpoint == "" {
			return nil, fmt.Errorf("MCP sse server %q requires an sse_endpoint", name)
		}
		return mcp.NewSSETransport(mcp.SSETransportConfig{
			SSEEndpoint:     sseEndpoint,
			MessageEndpoint: strings.TrimSpace(server.MessageEndpoint),
			Headers:         server.Headers,
		}), nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnknownMCPTransport, transportType)
	}
}

func startMCPProvider(
	ctx context.Context,
	name string,
	provider toolprovider.Engine,
	registry *tools.Registry,
	log *slog.Logger,
) (int, error) {
	if err := provider.Start(ctx); err != nil {
		log.Warn("mcp provider start failed", "name", name, "err", err)
		return 0, err
	}

	entries, err := provider.Discover(ctx)
	if err != nil {
		log.Warn("mcp tool discovery failed", "name", name, "err", err)
		_ = provider.Stop(ctx)
		return 0, err
	}

	for _, entry := range entries {
		if err := registry.Register(entry); err != nil {
			log.Warn("mcp tool registration failed", "tool", entry.Name, "err", err)
		}
	}
	return len(entries), nil
}

func retainFirstError(firstErr, err error) error {
	if firstErr != nil {
		return firstErr
	}
	return err
}

func (s *mcpProviderSet) cleanup(ctx context.Context, log *slog.Logger) {
	for _, provider := range s.providers {
		if err := provider.Stop(ctx); err != nil {
			log.Warn("mcp provider stop failed", "err", err)
		}
	}
}
