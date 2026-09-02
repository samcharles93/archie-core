package webui

import "testing"

// TestConfigFieldDescriptorsAreComplete guards the deliberateness the design
// doc asks for (docs/prds/config-schema.md): every field must carry a key,
// label, and type, and every field's Editable/RestartRequired must have been
// set by a case in configFieldDescriptors rather than defaulted to Go's
// zero value. Since Go cannot distinguish "explicitly false" from
// "unset," this test instead pins the exact set of keys and their
// deliberate values, so a new field added to the catalog without visiting
// this test fails loudly rather than silently inheriting Editable: false.
func TestConfigFieldDescriptorsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, section := range configFieldDescriptors() {
		if section.ID == "" {
			t.Errorf("section with empty ID, label %q", section.Label)
		}
		if section.Label == "" {
			t.Errorf("section %q has no label", section.ID)
		}
		if section.Description == "" {
			t.Errorf("section %q has no description", section.ID)
		}
		if len(section.Fields) == 0 {
			t.Errorf("section %q has no fields", section.ID)
		}
		for _, f := range section.Fields {
			if f.Key == "" {
				t.Fatalf("section %q has a field with no key", section.ID)
			}
			if seen[f.Key] {
				t.Errorf("duplicate field key %q", f.Key)
			}
			seen[f.Key] = true
			if f.Label == "" {
				t.Errorf("field %q has no label", f.Key)
			}
			if f.Type == "" {
				t.Errorf("field %q has no type", f.Key)
			}
			if f.Type == FieldEnum && len(f.Options) == 0 {
				t.Errorf("field %q is type enum but has no options", f.Key)
			}
			if f.Type != FieldEnum && len(f.Options) != 0 {
				t.Errorf("field %q has options but is not type enum", f.Key)
			}
			// Value/LockedReason/Overridden are attached per-instance
			// against a live ConfigView (archie-core-b6ew.2), not part of
			// the static catalog -- the catalog must not pre-populate them.
			if f.Value != nil {
				t.Errorf("field %q has a static Value; that is attached per-request", f.Key)
			}
			if f.LockedReason != "" {
				t.Errorf("field %q has a static LockedReason; that is attached per-request", f.Key)
			}
			if f.Overridden {
				t.Errorf("field %q has a static Overridden; that is attached per-request", f.Key)
			}
		}
	}

	// Every dotted key settings.js hardcodes today must have a descriptor,
	// so the schema is a superset (ideally exact set) of what the frontend
	// currently renders -- see ui/src/settings/settings.js.
	wantKeys := []string{
		"bot_user", "bot_email", "label", "forge.type", "forge.host", "diff_cap_lines",
		"repos", "models", "providers",
		"budgets.max_steps", "budgets.wall_clock", "budgets.gate_max_failures",
		"work_dir", "db_path", "skills_dir", "plugin_dir", "secret_engine_dir",
		"containers.image", "containers.max_concurrency", "containers.max_uptime",
		"containers.volume_ttl", "containers.pull_policy", "containers.network",
		"web.listen",
	}
	for _, key := range wantKeys {
		if !seen[key] {
			t.Errorf("missing descriptor for %q, which settings.js already hardcodes", key)
		}
	}
	if len(seen) != len(wantKeys) {
		t.Errorf("configFieldDescriptors has %d keys, want exactly %d (extra keys need adding to wantKeys deliberately)", len(seen), len(wantKeys))
	}
}

// TestConfigFieldDescriptorsStructuredFieldsAreNotEditable pins the design
// decision that structured fields (repositories, models, providers) stay
// read-only until archie-core-b6ew.4 gives them a dedicated editor -- the
// generic scalar renderer (archie-core-b6ew.3) must skip them, not attempt
// a text-input edit on a field whose Value is an array or map.
func TestConfigFieldDescriptorsStructuredFieldsAreNotEditable(t *testing.T) {
	for _, section := range configFieldDescriptors() {
		for _, f := range section.Fields {
			if f.Type == FieldStructured && f.Editable {
				t.Errorf("field %q is type structured but marked editable; structured fields need archie-core-b6ew.4's dedicated editor first", f.Key)
			}
		}
	}
}

// TestConfigFieldDescriptorsLockedStorageFieldsAreNotEditable pins
// work_dir/db_path as Editable: false in the static catalog. They are also
// reported LockedReason at runtime via overlay.DeniedKeys (api_config.go),
// but the catalog marks them non-editable independently so the generic
// renderer does not need runtime state to know not to offer an edit
// affordance for a key that can never succeed.
func TestConfigFieldDescriptorsLockedStorageFieldsAreNotEditable(t *testing.T) {
	locked := map[string]bool{"work_dir": true, "db_path": true}
	for _, section := range configFieldDescriptors() {
		for _, f := range section.Fields {
			if locked[f.Key] && f.Editable {
				t.Errorf("field %q is denied at runtime (overlay.DeniedKeys) but marked editable in the static catalog", f.Key)
			}
		}
	}
}
