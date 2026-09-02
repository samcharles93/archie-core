package webui

// Configuration schema contract (archie-core-b6ew.1): descriptor types and
// hand-authored field/section metadata for the Configuration page.
//
// This is deliberately NOT a reflection-based schema over config.Config.
// internal/config/config.go's own package doc marks it "scheduled for
// dissolution: its types and methods are to be reassigned to the domains
// whose behaviour they describe" -- a generic schema-from-struct-tags
// mechanism would be built on a foundation this repo has already decided to
// remove. Every field below is already a deliberate ConfigView allowlist
// entry (see api_config.go's own comment on why that allowlist exists); this
// attaches a label to a value that was already vetted as safe to show,
// rather than inventing a new safety boundary.
//
// See docs/prds/config-schema.md for the full design.

// ConfigFieldType is how the frontend's generic renderer decides what
// control to show for a field. FieldStructured fields (repositories,
// models, providers) opt out of the generic renderer entirely and keep
// their dedicated editors (archie-core-b6ew.4).
type ConfigFieldType string

const (
	FieldString     ConfigFieldType = "string"
	FieldInt        ConfigFieldType = "int"
	FieldBool       ConfigFieldType = "bool"
	FieldDuration   ConfigFieldType = "duration"
	FieldEnum       ConfigFieldType = "enum"
	FieldStructured ConfigFieldType = "structured"
)

// ConfigField is one field's rendering and editing contract: what it is,
// what it means, and whether the dashboard may change it right now. Value
// is attached by the caller building the schema against a live ConfigView
// (archie-core-b6ew.2) -- the descriptor itself is data-independent.
type ConfigField struct {
	// Key is the dotted path this field is addressed by, matching the key
	// space handleConfigUpdate/UpdateConfig already accepts.
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Description string          `json:"description,omitempty"`
	Type        ConfigFieldType `json:"type"`
	Value       any             `json:"value,omitempty"`
	// Editable is false for a field this page shows but the dashboard
	// cannot change yet (structured fields without an editor). It is
	// distinct from LockedReason: Editable is a schema-time property of
	// the field itself; LockedReason is a runtime property of the running
	// config (overlay.DeniedKeys).
	Editable bool `json:"editable"`
	// LockedReason is set per instance from the running config's denied
	// keys (overlay.DeniedKeys), not hand-authored here.
	LockedReason string `json:"locked_reason,omitempty"`
	// Overridden is set per instance from the running config's overlay
	// state, not hand-authored here.
	Overridden bool `json:"overridden"`
	// Options lists the valid values for a FieldEnum field.
	Options []string `json:"options,omitempty"`
	// RestartRequired reports that changing this field through the
	// dashboard will not take effect until archied restarts. Sourced from
	// internal/app/archied/reload.go's reloadableFields/reloadableSubFields
	// allowlist -- webui does not import that app-layer package (dependency
	// direction: app -> infrastructure, not the reverse), so the answer is
	// copied by hand per field below and must be re-checked whenever
	// reload.go's allowlist changes.
	RestartRequired bool `json:"restart_required"`
}

// ConfigSection groups fields the same way the settings page's cards
// already do -- no new sections, this is metadata on existing rows.
type ConfigSection struct {
	ID          string        `json:"id"`
	Label       string        `json:"label"`
	Description string        `json:"description,omitempty"`
	Fields      []ConfigField `json:"fields"`
}

// configFieldDescriptors is the hand-authored catalog: one entry per field
// ConfigView exposes today, in the section grouping settings.js already
// renders. Values, LockedReason and Overridden are attached separately
// against a live ConfigView (archie-core-b6ew.2); this list is the
// data-independent half of the contract -- key, label, type, and the two
// safety-relevant properties (editable, restart_required) that must be
// decided deliberately rather than defaulted.
//
// RestartRequired is set from internal/app/archied/reload.go as of this
// writing:
//   - reloadableFields lists BotUser, BotEmail, Label, DiffCapLines, Budgets,
//     Models -- all reloadable.
//   - reloadableSubFields["Containers"] allows only LegacyEnabled and
//     VolumeTTL -- every other Containers field (Image, MaxConcurrency,
//     MaxUptime, PullPolicy, Network) requires a restart.
//   - reloadableSubFields["Forge"] allows only Host -- Forge.Type requires a
//     restart even though the dashboard already renders it as editable.
//   - Web, SkillsDir, PluginDir, SecretEngineDir are absent from both
//     allowlists entirely, so they require a restart.
//   - WorkDir and DBPath are locked (overlay.DeniedKeys), not merely
//     restart-required -- the dashboard cannot change them at all.
func configFieldDescriptors() []ConfigSection {
	return []ConfigSection{
		{
			ID:          "identity",
			Label:       "Identity",
			Description: "Who Archie is on the forge, and how it addresses commits and comments.",
			Fields: []ConfigField{
				{Key: "bot_user", Label: "Bot account", Type: FieldString, Editable: true},
				{Key: "bot_email", Label: "Commit author email", Type: FieldString, Editable: true},
				{Key: "label", Label: "Pickup label", Type: FieldString, Editable: true},
				{Key: "forge.type", Label: "Forge type", Type: FieldString, Editable: true, RestartRequired: true},
				{Key: "forge.host", Label: "Forge host", Type: FieldString, Editable: true},
				{Key: "diff_cap_lines", Label: "Max diff size (lines)", Description: "0 means unlimited.", Type: FieldInt, Editable: true},
			},
		},
		{
			ID:          "repositories",
			Label:       "Repositories",
			Description: "Each repository Archie polls, and the quality gate a change must pass before it opens a pull request.",
			Fields: []ConfigField{
				{
					Key:         "repos",
					Label:       "Repositories",
					Description: "Each repository Archie polls, and the quality gate a change must pass before it opens a pull request.",
					Type:        FieldStructured,
					Editable:    false,
				},
			},
		},
		{
			ID:          "models",
			Label:       "Models & providers",
			Description: "Which model handles each stage of work, and which LLM providers are wired up. Only the environment variable NAME is shown, never its value.",
			Fields: []ConfigField{
				{Key: "models", Label: "Model roles", Type: FieldStructured, Editable: false},
				{Key: "providers", Label: "Providers", Type: FieldStructured, Editable: false},
			},
		},
		{
			ID:          "budgets",
			Label:       "Budgets",
			Description: "The limits every autonomous stage runs under, so a stuck task cannot run forever.",
			Fields: []ConfigField{
				{Key: "budgets.max_steps", Label: "Max steps", Description: "0 means unlimited.", Type: FieldInt, Editable: true},
				{Key: "budgets.wall_clock", Label: "Wall clock", Type: FieldDuration, Editable: true},
				{Key: "budgets.gate_max_failures", Label: "Max gate failures before parking", Description: "0 means unlimited.", Type: FieldInt, Editable: true},
			},
		},
		{
			ID:          "storage",
			Label:       "Storage & sandboxing",
			Description: "Where archied keeps its state, and how it isolates task execution.",
			Fields: []ConfigField{
				{Key: "work_dir", Label: "Work directory", Type: FieldString, Editable: false},
				{Key: "db_path", Label: "State path prefix", Type: FieldString, Editable: false},
				{Key: "skills_dir", Label: "Shared skills directory", Description: "Empty uses the work directory.", Type: FieldString, Editable: true, RestartRequired: true},
				{Key: "plugin_dir", Label: "Daemon plugin directory", Description: "Empty means no daemon plugins.", Type: FieldString, Editable: true, RestartRequired: true},
				{Key: "secret_engine_dir", Label: "Secret engine plugin directory", Description: "Empty means built-in secret engines only.", Type: FieldString, Editable: true, RestartRequired: true},
				{Key: "containers.image", Label: "Agent image", Type: FieldString, Editable: true, RestartRequired: true},
				{Key: "containers.max_concurrency", Label: "Max concurrent tasks", Description: "0 means unlimited.", Type: FieldInt, Editable: true, RestartRequired: true},
				{Key: "containers.max_uptime", Label: "Container max lifetime", Type: FieldDuration, Editable: true, RestartRequired: true},
				{Key: "containers.volume_ttl", Label: "Persistent volume retention", Type: FieldDuration, Editable: true},
				{Key: "containers.pull_policy", Label: "How images are refreshed", Description: `"missing" pulls only when the image isn't present locally; "always" pulls before every run.`, Type: FieldEnum, Options: []string{"missing", "always"}, Editable: true, RestartRequired: true},
				{Key: "containers.network", Label: "Docker network", Description: "Empty auto-detects the Docker network.", Type: FieldString, Editable: true, RestartRequired: true},
			},
		},
		{
			ID:          "web",
			Label:       "Dashboard",
			Description: "This dashboard's own listen address.",
			Fields: []ConfigField{
				{Key: "web.listen", Label: "Listen address", Description: `"off" disables the dashboard.`, Type: FieldString, Editable: true, RestartRequired: true},
			},
		},
	}
}

// configFieldValues maps each descriptor key to its current value out of a
// live ConfigView. A plain switch, not reflection, for the same reason
// configFieldDescriptors is hand-authored: every value here already passed
// through ConfigView's own secret-safe allowlist, so this only has to
// answer "which already-safe field does this key mean," never "is this
// field safe to show."
func configFieldValues(view ConfigView) map[string]any {
	return map[string]any{
		"bot_user":                   view.Identity.BotUser,
		"bot_email":                  view.Identity.BotEmail,
		"label":                      view.Identity.Label,
		"forge.type":                 view.Identity.ForgeType,
		"forge.host":                 view.Identity.ForgeHost,
		"diff_cap_lines":             view.Identity.DiffCapLines,
		"repos":                      view.Repositories,
		"models":                     view.Models,
		"providers":                  view.Providers,
		"budgets.max_steps":          view.Budgets.MaxSteps,
		"budgets.wall_clock":         view.Budgets.WallClock,
		"budgets.gate_max_failures":  view.Budgets.GateMaxFailures,
		"work_dir":                   view.Storage.WorkDir,
		"db_path":                    view.Storage.DBPath,
		"skills_dir":                 view.Storage.SkillsDir,
		"plugin_dir":                 view.Storage.PluginDir,
		"secret_engine_dir":          view.Storage.SecretEngineDir,
		"containers.image":           view.Containers.Image,
		"containers.max_concurrency": view.Containers.MaxConcurrency,
		"containers.max_uptime":      view.Containers.MaxUptime,
		"containers.volume_ttl":      view.Containers.VolumeTTL,
		"containers.pull_policy":     view.Containers.PullPolicy,
		"containers.network":         view.Containers.Network,
		"web.listen":                 view.Web.Listen,
	}
}

// buildConfigSchema attaches a live ConfigView's values, locked reasons, and
// overridden markers to the static descriptor catalog, producing the
// sections handleConfig returns to the dashboard. The catalog
// (configFieldDescriptors) and the per-request state (configFieldValues,
// view.Locked, view.Overridden) are kept as two separate functions
// deliberately: one is data-independent and safe to unit-test as a fixed
// catalog, the other is "what does this specific running config say."
func buildConfigSchema(view ConfigView) []ConfigSection {
	values := configFieldValues(view)
	overridden := make(map[string]bool, len(view.Overridden))
	for _, key := range view.Overridden {
		overridden[key] = true
	}

	sections := configFieldDescriptors()
	for i := range sections {
		for j := range sections[i].Fields {
			f := &sections[i].Fields[j]
			f.Value = values[f.Key]
			f.LockedReason = view.Locked[f.Key]
			f.Overridden = overridden[f.Key]
		}
	}
	return sections
}
