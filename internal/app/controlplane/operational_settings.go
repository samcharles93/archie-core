package controlplane

import (
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

const (
	RepositoryPoliciesKind       = "repository-policies"
	ChannelSettingsKind          = "channel-settings"
	SchedulingPolicyKind         = "scheduling-policy"
	ToolSettingsKind             = "tool-settings"
	PluginSettingsKind           = "plugin-settings"
	ContainerRuntimePoliciesKind = "container-runtime-policies"
)

type schedulingPolicy struct {
	PollInterval string          `json:"poll_interval"`
	MaxRetries   int             `json:"max_retries"`
	Dispatch     config.Dispatch `json:"dispatch"`
}

type pluginSettings struct {
	PluginDir       string `json:"plugin_dir"`
	ModuleDir       string `json:"module_dir"`
	SecretEngineDir string `json:"secret_engine_dir"`
	SkillsDir       string `json:"skills_dir"`
}

func operationalDefinitions() []Definition {
	return []Definition{
		{Kind: RepositoryPoliciesKind, Title: "Repository policies", ApplyMode: "restart-required", Schema: arraySchema, Seed: func(cfg config.Config) any { return cfg.Repos }, Validate: validateRepositories},
		{Kind: ChannelSettingsKind, Title: "Channel settings", ApplyMode: "restart-required", Schema: objectSchema, Seed: seedChannels, Validate: validateChannels},
		{Kind: SchedulingPolicyKind, Title: "Scheduling policy", ApplyMode: "restart-required", Schema: objectSchema, Seed: func(cfg config.Config) any {
			return schedulingPolicy{cfg.PollInterval.Std().String(), cfg.MaxRetries, cfg.Dispatch}
		}, Validate: validateScheduling},
		{Kind: ToolSettingsKind, Title: "Tool and MCP settings", ApplyMode: "restart-required", Schema: objectSchema, Seed: seedTools, Validate: validateTools},
		{Kind: PluginSettingsKind, Title: "Plugin settings", ApplyMode: "restart-required", Schema: objectSchema, Seed: func(cfg config.Config) any {
			return pluginSettings{cfg.PluginDir, cfg.ModuleDir, cfg.SecretEngineDir, cfg.SkillsDir}
		}, Validate: func(input []byte) error { return validateAs(input, func(pluginSettings) error { return nil }) }},
		{Kind: ContainerRuntimePoliciesKind, Title: "Container runtime policies", ApplyMode: "restart-required", Schema: objectSchema, Seed: func(cfg config.Config) any { return cfg.Containers }, Validate: validateContainers},
	}
}

func validateRepositories(input []byte) error {
	return validateAs(input, func(repos []config.Repo) error {
		seen := make(map[string]struct{}, len(repos))
		for _, repo := range repos {
			if strings.TrimSpace(repo.Owner) == "" || strings.TrimSpace(repo.Name) == "" {
				return fmt.Errorf("repository owner and name are required")
			}
			if _, exists := seen[repo.FullName()]; exists {
				return fmt.Errorf("duplicate repository %q", repo.FullName())
			}
			seen[repo.FullName()] = struct{}{}
		}
		return nil
	})
}

func validateScheduling(input []byte) error {
	return validateAs(input, func(policy schedulingPolicy) error {
		if policy.MaxRetries < 0 {
			return fmt.Errorf("max_retries must not be negative")
		}
		if policy.PollInterval != "0s" {
			interval, err := time.ParseDuration(policy.PollInterval)
			if err != nil || interval < 0 {
				return fmt.Errorf("poll_interval must be a non-negative duration")
			}
		}
		return nil
	})
}

func validateContainers(input []byte) error {
	return validateAs(input, func(settings config.ContainerConfig) error {
		if settings.MaxConcurrency < 0 || time.Duration(settings.MaxUptime) < 0 || time.Duration(settings.VolumeTTL) < 0 {
			return fmt.Errorf("container limits must not be negative")
		}
		if settings.PullPolicy != "" && settings.PullPolicy != "missing" && settings.PullPolicy != "always" {
			return fmt.Errorf("pull_policy must be missing or always")
		}
		return nil
	})
}
