package webui

// ConfigFieldType is how the frontend's generic renderer decides what
// control to show for a field. FieldStructured fields (repositories,
// models, providers) opt out of the generic renderer entirely and keep
// their dedicated editors.
type ConfigFieldType string

const (
	FieldString     ConfigFieldType = "string"
	FieldInt        ConfigFieldType = "int"
	FieldBool       ConfigFieldType = "bool"
	FieldDuration   ConfigFieldType = "duration"
	FieldEnum       ConfigFieldType = "enum"
	FieldStructured ConfigFieldType = "structured"
)

// ConfigField is one field's rendering contract: what it is, what it means,
// and what changing it would require. Value is attached by the caller
// building the schema against a live ConfigView -- the
// descriptor itself is data-independent.
type ConfigField struct {
	// Key is the dotted path this field is addressed by.
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Description string          `json:"description,omitempty"`
	Type        ConfigFieldType `json:"type"`
	Value       any             `json:"value,omitempty"`
	// LockedReason is set per instance from the running config's denied
	// keys (configuration.DeniedKeys), not hand-authored here.
	LockedReason string `json:"locked_reason,omitempty"`
	// Options lists the valid values for a FieldEnum field.
	Options []string `json:"options,omitempty"`
	// RestartRequired reports that a change takes effect only after a restart.
	// Kept in step with internal/app/archied/reload.go by hand.
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

// configFieldDescriptors lists every ConfigView field with its section,
// label, type and whether it needs a restart.
func configFieldDescriptors() []ConfigSection {
	return []ConfigSection{
		{
			ID:          "identity",
			Label:       "Identity",
			Description: "Who Archie is on the forge, and how it addresses commits and comments.",
			Fields: []ConfigField{
				{Key: "bot_user", Label: "Bot account", Type: FieldString},
				{Key: "bot_email", Label: "Commit author email", Type: FieldString},
				{Key: "label", Label: "Pickup label", Type: FieldString},
				{Key: "forge.type", Label: "Forge type", Type: FieldString, RestartRequired: true},
				{Key: "forge.host", Label: "Forge host", Type: FieldString},
				{Key: "diff_cap_lines", Label: "Max diff size (lines)", Description: "0 switches the cap off; leave unset for the default.", Type: FieldInt},
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
				},
			},
		},
		{
			ID:          "models",
			Label:       "Models & providers",
			Description: "Which model handles each stage of work. Only the environment variable NAME is shown, never its value.",
			Fields: []ConfigField{
				{Key: "models", Label: "Model roles", Type: FieldStructured},
				{Key: "providers", Label: "Providers", Type: FieldStructured},
			},
		},
		{
			ID:          "budgets",
			Label:       "Budgets",
			Description: "The limits every autonomous stage runs under, so a stuck task cannot run forever.",
			Fields: []ConfigField{
				{Key: "budgets.max_steps", Label: "Max steps", Description: "0 means unlimited.", Type: FieldInt},
				{Key: "budgets.wall_clock", Label: "Wall clock", Type: FieldDuration},
				{Key: "budgets.gate_max_failures", Label: "Max gate failures before parking", Description: "0 means unlimited.", Type: FieldInt},
			},
		},
		{
			ID:          "storage",
			Label:       "Storage",
			Description: "Where archied keeps its state.",
			Fields: []ConfigField{
				{Key: "work_dir", Label: "Work directory", Type: FieldString},
				{Key: "state_dir", Label: "State directory", Description: "Embedded NATS store, task logs and the readiness disk probe.", Type: FieldString},
				{Key: "database_url", Label: "PostgreSQL URL", Description: "The State Store's database connection; set at boot, restart required.", Type: FieldString},
			},
		},
		{
			ID:          "web",
			Label:       "Dashboard",
			Description: "This dashboard's listen address.",
			Fields: []ConfigField{
				{Key: "web.listen", Label: "Listen address", Description: `"off" disables the dashboard.`, Type: FieldString, RestartRequired: true},
			},
		},
	}
}

// configFieldValues maps each descriptor key to its value in view.
func configFieldValues(view ConfigView) map[string]any {
	return map[string]any{
		"bot_user":                  view.Identity.BotUser,
		"bot_email":                 view.Identity.BotEmail,
		"label":                     view.Identity.Label,
		"forge.type":                view.Identity.ForgeType,
		"forge.host":                view.Identity.ForgeHost,
		"diff_cap_lines":            view.Identity.DiffCapLines,
		"repos":                     view.Repositories,
		"models":                    view.Models,
		"providers":                 view.Providers,
		"budgets.max_steps":         view.Budgets.MaxSteps,
		"budgets.wall_clock":        view.Budgets.WallClock,
		"budgets.gate_max_failures": view.Budgets.GateMaxFailures,
		"work_dir":                  view.Storage.WorkDir,
		"state_dir":                 view.Storage.StateDir,
		"database_url":              view.Storage.DatabaseURL,
		"web.listen":                view.Web.Listen,
	}
}

// buildConfigSchema attaches view's values and locked reasons to the
// descriptor catalog.
func buildConfigSchema(view ConfigView) []ConfigSection {
	values := configFieldValues(view)
	sections := configFieldDescriptors()
	for i := range sections {
		for j := range sections[i].Fields {
			f := &sections[i].Fields[j]
			f.Value = values[f.Key]
			f.LockedReason = view.Locked[f.Key]
		}
	}
	return sections
}
