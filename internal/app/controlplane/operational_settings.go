package controlplane

import (
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

const (
	RepositoryPoliciesKind       = "repository-policies"
	SchedulingPolicyKind         = "scheduling-policy"
	ToolSettingsKind             = "tool-settings"
	PluginSettingsKind           = "plugin-settings"
	ContainerRuntimePoliciesKind = "container-runtime-policies"
)

type schedulingPolicy struct {
	PollInterval string `json:"poll_interval"`
	MaxRetries   int    `json:"max_retries"`
	// Label is the trigger-matching label the stored dispatch pairs with.
	// It is a pointer, not a string, so a policy stored before the field
	// existed decodes nil and leaves the file document's label in force when
	// it is layered (runtime_config.go) -- the same absence-means-inherited
	// shape a kind with no stored value has. Writes are stricter: the pairing
	// rule below refuses a label-requiring trigger with no label, so the
	// store can never bless the half of a pairing boot then refuses.
	Label    *string         `json:"label,omitempty"`
	Dispatch config.Dispatch `json:"dispatch"`
}

type pluginSettings struct {
	PluginDir       string `json:"plugin_dir"`
	ModuleDir       string `json:"module_dir"`
	SecretEngineDir string `json:"secret_engine_dir"`
	SkillsDir       string `json:"skills_dir"`
}

func operationalDefinitions() []Definition {
	return []Definition{
		{Kind: RepositoryPoliciesKind, Title: "Repository policies", ApplyMode: "live", Document: []config.Repo{}, Seed: func(cfg config.Config) any { return cfg.Repos }, Validate: validateRepositories},
		{Kind: ChannelSettingsKind, Title: "Channel settings", ApplyMode: "live", Document: channelSettings{}, Seed: seedChannels, Validate: validateChannels, Normalize: normalizeChannels},
		{Kind: SchedulingPolicyKind, Title: "Scheduling policy", ApplyMode: "live", Document: schedulingPolicy{}, Seed: func(cfg config.Config) any {
			return schedulingPolicy{PollInterval: cfg.PollInterval.Std().String(), MaxRetries: cfg.MaxRetries, Label: &cfg.Label, Dispatch: cfg.Dispatch}
		}, Validate: validateScheduling},
		{Kind: ToolSettingsKind, Title: "Tool and MCP settings", ApplyMode: "restart-required", Document: toolSettings{}, Seed: seedTools, Validate: validateTools},
		// ApplyMode is live for additions: a stored directory change re-layers live
		// and the daemon's reconciliation loads new and changed files without a
		// restart. A removal cannot unload Yaegi's interpreter, so it stays an
		// outstanding apply-status problem rather than a restart of the whole kind
		// (docs/prds/plugin-settings-live.md).
		{Kind: PluginSettingsKind, Title: "Plugin settings", ApplyMode: "live", Document: pluginSettings{}, Seed: func(cfg config.Config) any {
			return pluginSettings{cfg.PluginDir, cfg.ModuleDir, cfg.SecretEngineDir, cfg.SkillsDir}
		}, Validate: validatePluginSettings},
		{Kind: ContainerRuntimePoliciesKind, Title: "Container runtime policies", ApplyMode: "restart-required", Document: containerRuntimePolicies{}, Seed: seedContainerPolicies, Validate: validateContainers, Normalize: normalizeContainerPolicies},
	}
}

// validatePluginSettings accepts every value, deliberately. The four directories
// are free-form operator paths, and that is the decision recorded for
// archie-core #1143 rather than a validator nobody has written yet.
//
// Two candidate rules were considered and are refused:
//
//   - That the directory must exist. Every consumer reads a missing directory as
//     an empty one by design: plugin.LoadDir and secret.Registry.LoadDir return
//     no entries and no error on os.IsNotExist, loadModules skips a kind whose
//     file is absent, and playbook.Load returns an empty store. The no-error half
//     is pinned by internal/secret's TestLoadDirNonexistent. An existence rule
//     would turn each of those deliberate no-ops into a refused startup.
//   - That the path must be absolute, or that skills_dir must sit under
//     plugin_dir. No directory setting in this repository is validated for shape
//     (work_dir, state_dir, chat.workspace, the routing and playbook paths and an
//     MCP server's work_dir are all free-form), so a rule for these four alone
//     would be arbitrary. It would also fail closed in the wrong place: a stored
//     document is layered over the file and the boot path validates the result,
//     so a rule added here refuses to start a daemon whose only problem is a
//     value the store already holds.
//
// A "~/..." value is not the missing rule either: it is a path-expansion
// concern, handled where the file is read (configuration.applyGeneralDefaults),
// not a reason to refuse the document.
//
// It stays a real function rather than an inline no-op so that a rule, if one is
// ever settled, has one obvious home.
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
		// The pairing rule the file layer's validateDispatch enforces on the
		// effective document, judged here on the data the resource carries:
		// RuntimeConfig layers this label over the file's (an absent field
		// leaves the file's label in force, which is what legacy policies
		// decode with), so a stored label-requiring trigger with no label was
		// a value the store blessed and boot refused -- the two-layer
		// disagreement the parity test refuses to allow (archie-core-7pyj).
		if workintake.RequiresLabel(policy.Dispatch.Trigger) && (policy.Label == nil || *policy.Label == "") {
			return fmt.Errorf("label is required when dispatch.trigger is %q (an empty label matches every open issue)", policy.Dispatch.Trigger)
		}
		return nil
	})
}
