package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

// RuntimeConfig applies restart-scoped database resources over bootstrap
// configuration. Values intentionally absent from a control-plane projection,
// such as secret-bearing MCP headers and channel update commands, remain
// bootstrap-owned.
func (c *Client) RuntimeConfig(ctx context.Context, base config.Config) (config.Config, error) {
	out := base.Clone()
	if err := c.query(ctx, ProviderSettingsKind, func(value []byte) error {
		var providers map[string]providerDocument
		if err := json.Unmarshal(value, &providers); err != nil {
			return err
		}
		out.Providers = make(map[string]config.Provider, len(providers))
		for name, provider := range providers {
			out.Providers[name] = config.Provider{Class: provider.Class, APIKeyEnv: provider.APIKeyEnv, APIKey: provider.APIKey, BaseURL: provider.BaseURL}
		}
		return nil
	}); err != nil {
		return config.Config{}, err
	}
	if err := c.queryJSON(ctx, ModelRoleAssignmentsKind, &out.Models); err != nil {
		return config.Config{}, err
	}
	if err := c.queryJSON(ctx, RepositoryPoliciesKind, &out.Repos); err != nil {
		return config.Config{}, err
	}
	chat, err := c.RuntimeChatConfig(ctx, out.Chat)
	if err != nil {
		return config.Config{}, err
	}
	out.Chat = chat
	if err := c.query(ctx, SchedulingPolicyKind, func(value []byte) error {
		var policy schedulingPolicy
		if err := json.Unmarshal(value, &policy); err != nil {
			return err
		}
		interval, err := time.ParseDuration(policy.PollInterval)
		if err != nil {
			return err
		}
		out.PollInterval, out.MaxRetries, out.Dispatch = config.Duration(interval), policy.MaxRetries, policy.Dispatch
		return nil
	}); err != nil {
		return config.Config{}, err
	}
	return c.runtimeToolConfig(ctx, out)
}

func (c *Client) runtimeToolConfig(ctx context.Context, out config.Config) (config.Config, error) {
	if err := c.query(ctx, ToolSettingsKind, func(value []byte) error {
		var settings toolSettings
		if err := json.Unmarshal(value, &settings); err != nil {
			return err
		}
		headers := make(map[string]map[string]string, len(out.Tools.MCPServers))
		for _, server := range out.Tools.MCPServers {
			headers[server.Name] = server.Headers
		}
		servers := make([]config.MCPServer, 0, len(settings.MCPServers))
		for _, server := range settings.MCPServers {
			servers = append(servers, config.MCPServer{Name: server.Name, Transport: server.Transport, Command: server.Command, Args: server.Args, WorkDir: server.WorkDir, URL: server.URL, Headers: headers[server.Name], SSEEndpoint: server.SSEEndpoint, MessageEndpoint: server.MessageEndpoint})
		}
		out.Tools = config.ToolsConfig{MCPServers: servers, Policy: settings.Policy, WebFetch: settings.WebFetch, Minimax: config.MinimaxConfig{Enabled: settings.Minimax.Enabled, APIKey: settings.Minimax.APIKey, BaseURL: settings.Minimax.BaseURL}}
		return nil
	}); err != nil {
		return config.Config{}, err
	}
	if err := c.query(ctx, PluginSettingsKind, func(value []byte) error {
		var settings pluginSettings
		if err := json.Unmarshal(value, &settings); err != nil {
			return err
		}
		out.PluginDir, out.ModuleDir, out.SecretEngineDir, out.SkillsDir = settings.PluginDir, settings.ModuleDir, settings.SecretEngineDir, settings.SkillsDir
		return nil
	}); err != nil {
		return config.Config{}, err
	}
	if err := c.queryJSON(ctx, ContainerRuntimePoliciesKind, &out.Containers); err != nil {
		return config.Config{}, err
	}
	return out, nil
}

func (c *Client) RuntimeChatConfig(ctx context.Context, base config.ChatConfig) (config.ChatConfig, error) {
	out := base
	err := c.query(ctx, ChannelSettingsKind, func(value []byte) error {
		var settings channelSettings
		if err := json.Unmarshal(value, &settings); err != nil {
			return err
		}
		check, install := base.Telegram.UpdateCheckCommand, base.Telegram.UpdateInstallCommand
		out = config.ChatConfig{
			Operator: settings.Operator, ShowToolCalls: settings.ShowToolCalls, MaxSteps: settings.MaxSteps,
			Models: settings.Models, Email: settings.Email, WebhookAddr: settings.WebhookAddr,
			Webhook:   config.WebhookRoute{Path: settings.Webhook.Path, Secret: settings.Webhook.Secret, Template: settings.Webhook.Template, DeliverTo: settings.Webhook.DeliverTo},
			Telegram:  config.TelegramConfig{AllowedUserIDs: settings.Telegram.AllowedUserIDs, Token: settings.Telegram.Token, TokenEnv: settings.Telegram.TokenEnv, UpdateCheckCommand: check, UpdateInstallCommand: install},
			RateLimit: settings.RateLimit, UnrestrictedFilesystem: settings.UnrestrictedFilesystem, Workspace: settings.Workspace,
		}
		return nil
	})
	return out, err
}

func (c *Client) queryJSON(ctx context.Context, kind string, target any) error {
	return c.query(ctx, kind, func(value []byte) error { return json.Unmarshal(value, target) })
}

func (c *Client) query(ctx context.Context, kind string, decode func([]byte) error) error {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: kind})
	if err != nil {
		return clientError(err)
	}
	if response.Resource == nil {
		return fmt.Errorf("%s resource missing", kind)
	}
	if err := decode(response.Resource.ValueJson); err != nil {
		return fmt.Errorf("decode %s: %w", kind, err)
	}
	return nil
}
