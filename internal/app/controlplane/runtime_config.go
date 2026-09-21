package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

// resourceReader is the one read the layering performs: a resource kind's value
// and the version it came from. The gRPC client and the in-process server both
// provide it, so "what the database covers in the running config" has exactly
// one implementation. The offline validate runs the same code as boot, which is
// the only way its verdict can be boot's verdict.
type resourceReader interface {
	query(ctx context.Context, kind string, decode func([]byte) error) (int64, error)
}

// RuntimeConfig applies restart-scoped database resources over bootstrap
// configuration. Values intentionally absent from a control-plane projection,
// such as secret-bearing MCP headers and channel update commands, remain
// bootstrap-owned.
//
// It also returns the version of each kind it read, so the process that
// layers them in can report which version it is running
// (docs/prds/control-plane-apply-status.md).
func (c *Client) RuntimeConfig(ctx context.Context, base config.Config) (config.Config, map[string]int64, error) {
	return runtimeConfigFrom(ctx, c, base)
}

// RuntimeChatConfig layers the stored channel settings over the file document's
// chat section.
func (c *Client) RuntimeChatConfig(ctx context.Context, base config.ChatConfig) (config.ChatConfig, int64, error) {
	return runtimeChatConfigFrom(ctx, c, base)
}

// StoredRuntimeConfig is RuntimeConfig's layering over the store this server
// owns, for a caller holding the database file rather than a connection to it.
// Boot layers the stored settings onto the file config and then runs
// configuration.Validate, so an offline check of that database has to make the
// same first move with the same implementation; called with the config the
// daemon would load, the result is the document boot decides on.
func (s *Server) StoredRuntimeConfig(ctx context.Context, base config.Config) (config.Config, map[string]int64, error) {
	return runtimeConfigFrom(ctx, storeReader{resources: s.store}, base)
}

// storeReader reads the server's own store. It mirrors what the gRPC read
// answers -- a missing kind is an error, not an empty value -- so a store the
// daemon cannot layer settings from is one this reports rather than skips.
type storeReader struct {
	resources ResourceStore
}

func (r storeReader) query(ctx context.Context, kind string, decode func([]byte) error) (int64, error) {
	resource, err := r.resources.Resource(ctx, kind)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", kind, err)
	}
	if err := decode(resource.Value); err != nil {
		return 0, fmt.Errorf("decode %s: %w", kind, err)
	}
	return resource.Version, nil
}

func runtimeConfigFrom(ctx context.Context, reader resourceReader, base config.Config) (config.Config, map[string]int64, error) {
	out := base.Clone()
	versions := map[string]int64{}
	if err := layerResource(ctx, reader, versions, ProviderSettingsKind, func(value []byte) error {
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
		return config.Config{}, nil, err
	}
	if err := layerResourceJSON(ctx, reader, versions, ModelRoleAssignmentsKind, &out.Models); err != nil {
		return config.Config{}, nil, err
	}
	if err := layerResourceJSON(ctx, reader, versions, RepositoryPoliciesKind, &out.Repos); err != nil {
		return config.Config{}, nil, err
	}
	chat, chatVersion, err := runtimeChatConfigFrom(ctx, reader, out.Chat)
	if err != nil {
		return config.Config{}, nil, err
	}
	out.Chat, versions[ChannelSettingsKind] = chat, chatVersion
	if err := layerResource(ctx, reader, versions, SchedulingPolicyKind, func(value []byte) error {
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
		return config.Config{}, nil, err
	}
	return runtimeToolConfigFrom(ctx, reader, versions, out)
}

func runtimeToolConfigFrom(ctx context.Context, reader resourceReader, versions map[string]int64, out config.Config) (config.Config, map[string]int64, error) {
	if err := layerResource(ctx, reader, versions, ToolSettingsKind, func(value []byte) error {
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
		return config.Config{}, nil, err
	}
	if err := layerResource(ctx, reader, versions, PluginSettingsKind, func(value []byte) error {
		var settings pluginSettings
		if err := json.Unmarshal(value, &settings); err != nil {
			return err
		}
		out.PluginDir, out.ModuleDir, out.SecretEngineDir, out.SkillsDir = settings.PluginDir, settings.ModuleDir, settings.SecretEngineDir, settings.SkillsDir
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	if err := layerResourceJSON(ctx, reader, versions, ContainerRuntimePoliciesKind, &out.Containers); err != nil {
		return config.Config{}, nil, err
	}
	return out, versions, nil
}

func runtimeChatConfigFrom(ctx context.Context, reader resourceReader, base config.ChatConfig) (config.ChatConfig, int64, error) {
	out := base
	version, err := reader.query(ctx, ChannelSettingsKind, func(value []byte) error {
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
	return out, version, err
}

// layerResource decodes a resource and records the version it came from, so the
// layering ends up holding the version of every kind it applied.
func layerResource(ctx context.Context, reader resourceReader, versions map[string]int64, kind string, decode func([]byte) error) error {
	version, err := reader.query(ctx, kind, decode)
	if err != nil {
		return err
	}
	versions[kind] = version
	return nil
}

func layerResourceJSON(ctx context.Context, reader resourceReader, versions map[string]int64, kind string, target any) error {
	return layerResource(ctx, reader, versions, kind, func(value []byte) error { return json.Unmarshal(value, target) })
}

// query returns the version of the resource it decoded, so callers that layer
// a resource in can report the version they applied.
func (c *Client) query(ctx context.Context, kind string, decode func([]byte) error) (int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: kind})
	if err != nil {
		return 0, clientError(err)
	}
	if response.Resource == nil {
		return 0, fmt.Errorf("%s resource missing", kind)
	}
	if err := decode(response.Resource.ValueJson); err != nil {
		return 0, fmt.Errorf("decode %s: %w", kind, err)
	}
	return response.Resource.Version, nil
}
