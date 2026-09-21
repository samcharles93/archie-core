package controlplane

import (
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
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
		}, Validate: validatePluginSettings},
		{Kind: ContainerRuntimePoliciesKind, Title: "Container runtime policies", ApplyMode: "restart-required", Schema: objectSchema, Seed: func(cfg config.Config) any { return cfg.Containers }, Validate: validateContainers},
	}
}

// validatePluginSettings accepts every value: the four directories are free-form
// operator paths with no cross-field rule and no rule the configuration package
// applies either (configuration.Validate has no say over them), so there is
// nothing yet for this validator to enforce. It stays a real function rather than
// an inline no-op so the place to add a rule is obvious when one exists -- e.g.
// "a path that is absolute" or "a skills dir under the plugin dir".
func validatePluginSettings(input []byte) error {
	return validateAs(input, func(pluginSettings) error { return nil })
}

func validateRepositories(input []byte) error {
	// The rules live in the configuration package and are shared with the file
	// document's own validation (configuration.ValidateRepositories): which
	// repository lists are valid cannot differ between the file that seeds this
	// resource and the resource that replaces it.
	return validateAs(input, configuration.ValidateRepositories)
}

func validateScheduling(input []byte) error {
	return validateAs(input, func(policy schedulingPolicy) error {
		if policy.MaxRetries < 0 {
			return fmt.Errorf("max_retries must not be negative")
		}
		// A positive interval, not a non-negative one: the file layer's rule is
		// the same, and there is no defaulting behind a stored value -- a stored
		// "0s" reaches cfg.PollInterval as zero and stops archied starting.
		interval, err := time.ParseDuration(policy.PollInterval)
		if err != nil || interval <= 0 {
			return fmt.Errorf("poll_interval must be a positive duration")
		}
		// Same reason as the repository rules above: RuntimeConfig replaces
		// cfg.Dispatch from this resource, and the daemon refuses to start with a
		// trigger it does not know.
		if !configuration.DispatchTriggerValid(policy.Dispatch.Trigger) {
			return fmt.Errorf("dispatch.trigger %q is not a discovery rule the daemon can poll with", policy.Dispatch.Trigger)
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
		// The image is required for autonomous workflow workers; the same rule
		// holds for the stored policies that replace the file's [containers].
		if strings.TrimSpace(settings.Image) == "" {
			return fmt.Errorf("container image is required for autonomous workflow workers")
		}
		return nil
	})
}
