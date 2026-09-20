package controlplane

import (
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
)

type toolSettings struct {
	MCPServers []mcpServerSettings   `json:"mcp_servers"`
	Policy     config.ToolPolicy     `json:"policy"`
	WebFetch   config.WebFetchConfig `json:"web_fetch"`
	Minimax    minimaxSettings       `json:"minimax"`
}

type mcpServerSettings struct {
	Name              string   `json:"name"`
	Transport         string   `json:"transport"`
	Command           string   `json:"command,omitempty"`
	Args              []string `json:"args,omitempty"`
	WorkDir           string   `json:"work_dir,omitempty"`
	URL               string   `json:"url,omitempty"`
	SSEEndpoint       string   `json:"sse_endpoint,omitempty"`
	MessageEndpoint   string   `json:"message_endpoint,omitempty"`
	HeadersConfigured bool     `json:"headers_configured"`
}

type minimaxSettings struct {
	Enabled              bool             `json:"enabled"`
	APIKey               config.SecretRef `json:"api_key_ref"`
	CredentialConfigured bool             `json:"credential_configured"`
	BaseURL              string           `json:"base_url,omitempty"`
}

func seedTools(cfg config.Config) any {
	servers := make([]mcpServerSettings, 0, len(cfg.Tools.MCPServers))
	for _, server := range cfg.Tools.MCPServers {
		servers = append(servers, mcpServerSettings{Name: server.Name, Transport: server.Transport, Command: server.Command, Args: server.Args, WorkDir: server.WorkDir, URL: server.URL, SSEEndpoint: server.SSEEndpoint, MessageEndpoint: server.MessageEndpoint, HeadersConfigured: len(server.Headers) > 0})
	}
	return toolSettings{MCPServers: servers, Policy: cfg.Tools.Policy, WebFetch: cfg.Tools.WebFetch, Minimax: minimaxSettings{Enabled: cfg.Tools.Minimax.Enabled, APIKey: cfg.Tools.Minimax.APIKey, CredentialConfigured: cfg.Tools.Minimax.APIKey != (config.SecretRef{}), BaseURL: cfg.Tools.Minimax.BaseURL}}
}

func validateTools(input []byte) error {
	return validateAs(input, func(settings toolSettings) error {
		for _, server := range settings.MCPServers {
			transport := strings.ToLower(strings.TrimSpace(server.Transport))
			if server.Name == "" {
				return fmt.Errorf("MCP server name is required")
			}
			switch transport {
			case "", "stdio":
				if server.Command == "" {
					return fmt.Errorf("MCP stdio server %q requires command", server.Name)
				}
			case "http", "streamablehttp":
				if server.URL == "" {
					return fmt.Errorf("MCP http server %q requires url", server.Name)
				}
			case "sse":
				if server.SSEEndpoint == "" {
					return fmt.Errorf("MCP sse server %q requires sse_endpoint", server.Name)
				}
			default:
				return fmt.Errorf("unknown MCP transport %q", server.Transport)
			}
		}
		return nil
	})
}
