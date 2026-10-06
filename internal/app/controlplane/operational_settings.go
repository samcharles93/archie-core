package controlplane

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

const (
	RepositoryPoliciesKind       = "repository-policies"
	SchedulingPolicyKind         = "scheduling-policy"
	ToolSettingsKind             = "tool-settings"
	PluginSettingsKind           = "plugin-settings"
	ContainerRuntimePoliciesKind = "container-runtime-policies"
	ExtensionSettingsKind        = controlplanerpc.ExtensionSettingsKind
	UpdateSettingsKind           = controlplanerpc.UpdateSettingsKind
)

type schedulingPolicy struct {
	PollInterval string `json:"poll_interval"`
	MaxRetries   int    `json:"max_retries"`
	// Label is the dispatch trigger label. Nil keeps the file's label.
	Label    *string         `json:"label,omitempty"`
	Dispatch config.Dispatch `json:"dispatch"`
}

type pluginSettings struct {
	SkillsDir string `json:"skills_dir"`
}

func operationalDefinitions() []Definition {
	return []Definition{
		{Kind: RepositoryPoliciesKind, Title: "Repository policies", ApplyMode: "live", Document: []config.Repo{}, Seed: func(cfg config.Config) any { return cfg.Repos }, Validate: validateRepositories},
		// Restart-required: the Messaging Service, which owns the channel
		// transports, reconciles them live and archied re-layers each update, but
		// the Gateway's chat surfaces (workspace tools, rate limit, model
		// catalogs) are boot-built, so a change cannot promise live apply
		// everywhere the record reports it.
		{Kind: ChannelSettingsKind, Title: "Channel settings", ApplyMode: "restart-required", Document: channelSettings{}, Seed: seedChannels, Validate: validateChannels, Normalize: normalizeChannels},
		{Kind: SchedulingPolicyKind, Title: "Scheduling policy", ApplyMode: "live", Document: schedulingPolicy{}, Seed: func(cfg config.Config) any {
			return schedulingPolicy{PollInterval: cfg.PollInterval.Std().String(), MaxRetries: cfg.MaxRetries, Label: &cfg.Label, Dispatch: cfg.Dispatch}
		}, Validate: validateScheduling},
		// Live: changed MCP servers are reconnected, rolling back to the old engine
		// if the new one fails to start.
		{Kind: ToolSettingsKind, Title: "Tool and MCP settings", ApplyMode: "live", Document: toolSettings{}, Seed: seedTools, Validate: validateTools},
		// Restart-required: the one field, skills_dir, is read at boot, and the
		// watch refuses a change rather than layering it into the running config
		// while the boot-built consumers hold the old value
		// (refuseSkillsDirChange).
		{Kind: PluginSettingsKind, Title: "Plugin settings", ApplyMode: "restart-required", Document: pluginSettings{}, Seed: func(cfg config.Config) any {
			return pluginSettings{cfg.SkillsDir}
		}, Validate: validatePluginSettings},
		// Live: each process that supervises extensions re-reads this on its sync
		// tick, then starts, restarts or stops what changed.
		{Kind: ExtensionSettingsKind, Title: "Extension settings", ApplyMode: "live", Document: controlplanerpc.ExtensionSettings{}, Seed: func(config.Config) any { return controlplanerpc.ExtensionSettings{} }, Validate: validateExtensions},
		// Live: the container pool reads these on every acquire and the dispatcher
		// is resized on publish.
		// Live: every check and install reads the commands afresh.
		{Kind: UpdateSettingsKind, Title: "Update settings", ApplyMode: "live", Document: releaseupdate.Commands{}, Seed: seedUpdateCommands, Validate: validateUpdateCommands},
		{Kind: ContainerRuntimePoliciesKind, Title: "Container runtime policies", ApplyMode: "live", Document: containerRuntimePolicies{}, Seed: seedContainerPolicies, Validate: validateContainers, Normalize: normalizeContainerPolicies},
	}
}

// validateExtensions refuses a blank or repeated extension name: the name is
// the installed package the setting enables.
func validateExtensions(input []byte) error {
	return validateAs(input, func(doc controlplanerpc.ExtensionSettings) error {
		seen := make(map[string]struct{}, len(doc.Extensions))
		for _, setting := range doc.Extensions {
			if strings.TrimSpace(setting.Name) == "" {
				return fmt.Errorf("extension name is required")
			}
			if _, dup := seen[setting.Name]; dup {
				return fmt.Errorf("extension %q is listed twice", setting.Name)
			}
			seen[setting.Name] = struct{}{}
		}
		return nil
	})
}

// validatePluginSettings accepts every value.
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
		// A label-requiring trigger needs a label.
		if workintake.RequiresLabel(policy.Dispatch.Trigger) && (policy.Label == nil || *policy.Label == "") {
			return fmt.Errorf("label is required when dispatch.trigger is %q (an empty label matches every open issue)", policy.Dispatch.Trigger)
		}
		return nil
	})
}

// seedUpdateCommands points at the update scripts a release installs beside
// its binaries, so a zip install can update itself with nothing configured.
func seedUpdateCommands(config.Config) any {
	var commands releaseupdate.Commands
	exe, err := os.Executable()
	if err != nil {
		return commands
	}
	script := func(name string) []string {
		path := filepath.Join(filepath.Dir(exe), name)
		if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
			return nil
		}
		return []string{path}
	}
	commands.Check = script("archie-update-check")
	commands.Install = script("archie-update-install")
	return commands
}

// validateUpdateCommands refuses a command with a blank program, and an
// install command without a check to approve its release.
func validateUpdateCommands(input []byte) error {
	return validateAs(input, func(doc releaseupdate.Commands) error {
		for name, command := range map[string][]string{"check_command": doc.Check, "install_command": doc.Install} {
			if len(command) != 0 && strings.TrimSpace(command[0]) == "" {
				return fmt.Errorf("%s needs a program", name)
			}
		}
		if len(doc.Install) != 0 && len(doc.Check) == 0 {
			return fmt.Errorf("install_command needs a check_command")
		}
		return nil
	})
}
