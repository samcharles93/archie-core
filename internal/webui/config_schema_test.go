package webui

import "testing"

// TestConfigFieldDescriptorsAreComplete guards the deliberateness the design
// doc asks for (docs/prds/config-schema.md): every field must carry a key,
// label, and type, and every field's RestartRequired must have been set by a
// case in configFieldDescriptors rather than defaulted to Go's zero value.
// Since Go cannot distinguish "explicitly false" from "unset," this test
// instead pins the exact set of keys and their deliberate values, so a new
// field added to the catalog without visiting this test fails loudly rather
// than silently inheriting a zero value.
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
			// Value/LockedReason are attached per-instance
			// against a live ConfigView (archie-core-b6ew.2), not part of
			// the static catalog -- the catalog must not pre-populate them.
			if f.Value != nil {
				t.Errorf("field %q has a static Value; that is attached per-request", f.Key)
			}
			if f.LockedReason != "" {
				t.Errorf("field %q has a static LockedReason; that is attached per-request", f.Key)
			}
		}
	}

	// Keys the dashboard settings pages address by name must keep a descriptor.
	wantKeys := []string{
		"bot_user", "bot_email", "label", "forge.type", "forge.host", "diff_cap_lines",
		"repos", "models", "providers",
		"budgets.max_steps", "budgets.wall_clock", "budgets.gate_max_failures",
		"work_dir", "state_dir", "database_url",
		"web.listen",
	}
	for _, key := range wantKeys {
		if !seen[key] {
			t.Errorf("missing descriptor for %q", key)
		}
	}
}

// TestConfigFieldDescriptorsRestartRequiredIsDeliberate pins the exact
// RestartRequired value per field against the evidence in
// configFieldDescriptors' own doc comment (internal/app/archied/reload.go's
// reloadableFields/reloadableSubFields allowlist), so a field added or
// reclassified without checking reload.go fails loudly instead of quietly
// defaulting to false ("this takes effect immediately") when it does not.
func TestConfigFieldDescriptorsRestartRequiredIsDeliberate(t *testing.T) {
	want := map[string]bool{
		"bot_user":                  false,
		"bot_email":                 false,
		"label":                     false,
		"forge.type":                true, // reloadableSubFields["Forge"] allows only Host
		"forge.host":                false,
		"diff_cap_lines":            false,
		"repos":                     false,
		"models":                    false,
		"providers":                 false,
		"budgets.max_steps":         false,
		"budgets.wall_clock":        false,
		"budgets.gate_max_failures": false,
		"work_dir":                  false, // locked, not merely restart-required
		"state_dir":                 false, // locked, not merely restart-required
		"database_url":              false, // locked, not merely restart-required
		"web.listen":                true,  // Web is absent from both allowlists entirely
	}
	for _, section := range configFieldDescriptors() {
		for _, f := range section.Fields {
			wantRestart, ok := want[f.Key]
			if !ok {
				t.Errorf("field %q has no RestartRequired expectation in this test; add one against internal/app/archied/reload.go before adding the field", f.Key)
				continue
			}
			if f.RestartRequired != wantRestart {
				t.Errorf("field %q RestartRequired = %v, want %v", f.Key, f.RestartRequired, wantRestart)
			}
		}
	}
}
