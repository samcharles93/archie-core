package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// resourceReader returns a resource kind's value and version; found is false
// when the store holds none.

// RuntimeConfig layers stored resources over base and returns each kind's
// version.
func (c *Client) RuntimeConfig(ctx context.Context, base config.Config) (config.Config, map[string]int64, error) {
	return runtimeConfigFrom(ctx, c, base)
}

// RuntimeChatConfig layers the stored channel settings over the file document's
// chat section.

// StoredRuntimeConfig is RuntimeConfig over this server's own store. Kinds
// not stored are read as their seed.
func (s *Server) StoredRuntimeConfig(ctx context.Context, base config.Config) (config.Config, map[string]int64, error) {
	seeds, err := s.seededValues(base)
	if err != nil {
		return config.Config{}, nil, err
	}
	return runtimeConfigFrom(ctx, storeReader{resources: s.store, seeds: seeds}, base)
}

// storeReader reads this server's store, returning the seed for kinds it
// does not hold.
type storeReader struct {
	resources ResourceStore
	seeds     map[string][]byte
}

func (r storeReader) Query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error) {
	resource, err := r.resources.Resource(ctx, storecontract.DefaultOrgID, kind)
	if errors.Is(err, storecontract.ErrResourceNotFound) {
		seed, ok := r.seeds[kind]
		if !ok {
			return 0, false, fmt.Errorf("read %s: %w", kind, storecontract.ErrResourceNotFound)
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

func runtimeConfigFrom(ctx context.Context, reader controlplanerpc.ResourceReader, base config.Config) (config.Config, map[string]int64, error) {
	out := base.Clone()
	versions := map[string]int64{}
	if err := layerResource(ctx, reader, versions, ProviderSettingsKind, func(value []byte) error {
		var providers map[string]providerDocument
		if err := json.Unmarshal(value, &providers); err != nil {
			return err
		}
		out.Providers = make(map[string]config.Provider, len(providers))
		for name, provider := range providers {
			if err := rejectBootDerivedProviderEnv(name, provider); err != nil {
				return err
			}
			out.Providers[name] = config.Provider{Class: provider.Class, APIKeyEnv: provider.APIKeyEnv, APIKey: provider.APIKey, BaseURL: provider.BaseURL}
		}
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	// Stored role assignments replace cfg.Models outright.
	if err := layerResource(ctx, reader, versions, ModelRoleAssignmentsKind, func(value []byte) error {
		var roles map[string]string
		if err := json.Unmarshal(value, &roles); err != nil {
			return err
		}
		out.Models = roles
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	if err := layerResourceJSON(ctx, reader, versions, RepositoryPoliciesKind, &out.Repos); err != nil {
		return config.Config{}, nil, err
	}
	chat, chatVersion, err := controlplanerpc.RuntimeChatConfigFrom(ctx, reader, out.Chat)
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
		// A stored label replaces the file's.
		if policy.Label != nil {
			out.Label = *policy.Label
		}
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	// review-settings owns the pr-review dials once the store carries a value.
	// The two fields are assigned individually, not `out.Review = ...`: a field
	// added to config.Review later keeps the file's value until the stored
	// document carries it.
	if err := layerResource(ctx, reader, versions, ReviewSettingsKind, func(value []byte) error {
		var settings reviewSettings
		if err := json.Unmarshal(value, &settings); err != nil {
			return err
		}
		out.Review.PrecisionGate = settings.PrecisionGate
		out.Review.ApproveBeforePost = settings.ApproveBeforePost
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	return runtimeToolConfigFrom(ctx, reader, versions, out)
}

func runtimeToolConfigFrom(ctx context.Context, reader controlplanerpc.ResourceReader, versions map[string]int64, out config.Config) (config.Config, map[string]int64, error) {
	if err := layerResource(ctx, reader, versions, ToolSettingsKind, func(value []byte) error {
		var settings toolSettings
		if err := json.Unmarshal(value, &settings); err != nil {
			return err
		}
		headers := make(map[string]map[string]string, len(out.Tools.MCPServers))
		fileParallelToolCalls := make(map[string]bool, len(out.Tools.MCPServers))
		for _, server := range out.Tools.MCPServers {
			headers[server.Name] = server.Headers
			fileParallelToolCalls[server.Name] = server.ParallelToolCalls
		}
		servers := make([]config.MCPServer, 0, len(settings.MCPServers))
		for _, server := range settings.MCPServers {
			// cfg.Tools is replaced wholesale, so keep file-owned fields: headers, and
			// ParallelToolCalls when not stored.
			parallelToolCalls := fileParallelToolCalls[server.Name]
			if server.ParallelToolCalls != nil {
				parallelToolCalls = *server.ParallelToolCalls
			}
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
				ParallelToolCalls: parallelToolCalls,
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
	if err := layerResource(ctx, reader, versions, ContainerRuntimePoliciesKind, func(value []byte) error {
		var policies containerRuntimePolicies
		if err := json.Unmarshal(value, &policies); err != nil {
			return err
		}
		// Keep the file-owned Profiles and RegistryAuth across the replacement.
		profiles := out.Containers.Profiles
		registryAuth := out.Containers.RegistryAuth
		out.Containers = policies.settings()
		out.Containers.Profiles = profiles
		out.Containers.RegistryAuth = registryAuth
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	// Stored profiles replace the file's outright.
	if err := layerResource(ctx, reader, versions, AgentProfileKind, func(value []byte) error {
		var profiles map[string]agentProfile
		if err := json.Unmarshal(value, &profiles); err != nil {
			return err
		}
		out.Containers.Profiles = agentProfilesSettings(profiles)
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	// CredentialBindingsKind is likewise its own resource: a binding applies
	// without a restart, and a stored value replaces the file's outright.
	if err := layerResource(ctx, reader, versions, CredentialBindingsKind, func(value []byte) error {
		var bindings []credentialBinding
		if err := json.Unmarshal(value, &bindings); err != nil {
			return err
		}
		out.Containers.Credentials = credentialBindingsSettings(bindings)
		return nil
	}); err != nil {
		return config.Config{}, nil, err
	}
	return out, versions, nil
}

// layerResource decodes a stored resource and records its version. A kind
// with no stored value is skipped.
func layerResource(ctx context.Context, reader controlplanerpc.ResourceReader, versions map[string]int64, kind string, decode func([]byte) error) error {
	version, found, err := reader.Query(ctx, kind, decode)
	if err != nil {
		return err
	}
	if found {
		versions[kind] = version
	}
	return nil
}

func layerResourceJSON(ctx context.Context, reader controlplanerpc.ResourceReader, versions map[string]int64, kind string, target any) error {
	return layerResource(ctx, reader, versions, kind, func(value []byte) error { return json.Unmarshal(value, target) })
}

// query returns the version of the resource it decoded, so callers that layer
// a resource in can report the version they applied. A kind the store holds no
// value for is reported as not found rather than as an error: the caller leaves
// the file document's value in effect (see resourceReader).
