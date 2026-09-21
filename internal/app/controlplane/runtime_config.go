package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/store"
)

// resourceReader is the one read the layering performs: a resource kind's value
// and the version it came from, or nothing at all when the store holds no value
// for that kind. The gRPC client and the in-process server both provide it, so
// "what the database covers in the running config" has exactly one
// implementation. The offline validate runs the same code as boot, which is the
// only way its verdict can be boot's verdict.
//
// found is false only for the reader that dials a live State Store: a kind with
// no stored value leaves the file document's value in effect instead of failing
// the process, which is the state a seed ImportConfig refused leaves behind
// (controlplane.Server.ImportConfig skips it) and the state of a database the
// migration has not reached yet. storeReader never reports absent -- it answers
// with the seed the State Store would write -- so the offline verdict stays
// boot's.
type resourceReader interface {
	query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error)
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
//
// A kind the store does not hold is read as the seed the State Store would
// write for it from base (see storeReader.query), because that store seeds
// every kind before it serves.
func (s *Server) StoredRuntimeConfig(ctx context.Context, base config.Config) (config.Config, map[string]int64, error) {
	seeds, err := s.seededValues(base)
	if err != nil {
		return config.Config{}, nil, err
	}
	return runtimeConfigFrom(ctx, storeReader{resources: s.store, seeds: seeds}, base)
}

// storeReader reads the server's own store. It mirrors what the gRPC read
// answers -- a kind that is stored is the value and version it holds -- with
// one difference: a kind the store does not hold yet is the value the State
// Store seeds for it, since the daemon dials a store that has already run
// ImportConfig over the same config.
type storeReader struct {
	resources ResourceStore
	seeds     map[string][]byte
}

func (r storeReader) query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error) {
	resource, err := r.resources.Resource(ctx, kind)
	if errors.Is(err, store.ErrResourceNotFound) {
		seed, ok := r.seeds[kind]
		if !ok {
			return 0, false, fmt.Errorf("read %s: %w", kind, store.ErrResourceNotFound)
		}
		if err := decode(seed); err != nil {
			return 0, false, fmt.Errorf("decode %s seed: %w", kind, err)
		}
		// No version: nothing has been stored for this kind, so nothing has
		// been applied either. It is still a value to layer in -- the seed --
		// which is why this reader never reports the kind absent.
		return 0, true, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", kind, err)
	}
	if err := decode(resource.Value); err != nil {
		return 0, false, fmt.Errorf("decode %s: %w", kind, err)
	}
	return resource.Version, true, nil
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
		// The label pairs with the trigger and layers with it: once the store
		// carries one, it owns it, and the file can no longer drop the label a
		// stored label-requiring trigger depends on. A policy stored before
		// the field existed -- the nil the seed never produces -- leaves the
		// file document's label in force, and boot's gate judges the pairing
		// either way.
		if policy.Label != nil {
			out.Label = *policy.Label
		}
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
			servers = append(servers, config.MCPServer{
				Name:              server.Name,
				Transport:         server.Transport,
				Command:           server.Command,
				Args:              server.Args,
				WorkDir:           server.WorkDir,
				URL:               server.URL,
				Headers:           headers[server.Name],
				SSEEndpoint:       server.SSEEndpoint,
				MessageEndpoint:   server.MessageEndpoint,
				ParallelToolCalls: server.ParallelToolCalls,
			})
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
	version, found, err := reader.query(ctx, ChannelSettingsKind, func(value []byte) error {
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
	if err != nil {
		return out, 0, err
	}
	if !found {
		return out, 0, nil
	}
	return out, version, nil
}

// layerResource decodes a resource and records the version it came from, so the
// layering ends up holding the version of every kind it applied.
//
// A kind with no stored value is not an error and records no version: the file
// document's value stays in effect. That is the state a seed the resource
// validator refused leaves behind (controlplane.Server.ImportConfig skips it),
// and failing here instead would stop the process with a database-named error
// that editing config.toml cannot clear.
func layerResource(ctx context.Context, reader resourceReader, versions map[string]int64, kind string, decode func([]byte) error) error {
	version, found, err := reader.query(ctx, kind, decode)
	if err != nil {
		return err
	}
	if found {
		versions[kind] = version
	}
	return nil
}

func layerResourceJSON(ctx context.Context, reader resourceReader, versions map[string]int64, kind string, target any) error {
	return layerResource(ctx, reader, versions, kind, func(value []byte) error { return json.Unmarshal(value, target) })
}

// query returns the version of the resource it decoded, so callers that layer
// a resource in can report the version they applied. A kind the store holds no
// value for is reported as not found rather than as an error: the caller leaves
// the file document's value in effect (see resourceReader).
func (c *Client) query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: kind})
	if err != nil {
		mapped := clientError(err)
		if errors.Is(mapped, ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, mapped
	}
	if response.Resource == nil {
		return 0, false, fmt.Errorf("%s resource missing", kind)
	}
	if err := decode(response.Resource.ValueJson); err != nil {
		return 0, false, fmt.Errorf("decode %s: %w", kind, err)
	}
	return response.Resource.Version, true, nil
}
