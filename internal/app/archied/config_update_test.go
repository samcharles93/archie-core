package archied

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestApplyDottedOverlayNestsBeforeDecode pins the PATCH-path fix: the
// UI sends dotted keys ("budgets.max_steps") and yaml cannot decode a
// dotted key as a struct field, so the updates must be nested first. A
// regression here silently drops every dotted update while returning 200.
func TestApplyDottedOverlayNestsBeforeDecode(t *testing.T) {
	cfg := config.Config{Budgets: config.Budgets{MaxSteps: 10}}
	if err := applyDottedOverlay(&cfg, map[string]any{"budgets.max_steps": 20}); err != nil {
		t.Fatal(err)
	}
	if cfg.Budgets.MaxSteps != 20 {
		t.Fatalf("MaxSteps = %d, want 20 (dotted key must be nested before decode)", cfg.Budgets.MaxSteps)
	}
}

// TestApplyDottedOverlayRejectsEmptyKey pins that a malformed dotted key
// fails instead of silently nesting under a synthetic root.
func TestApplyDottedOverlayRejectsEmptyKey(t *testing.T) {
	cfg := config.Config{}
	if err := applyDottedOverlay(&cfg, map[string]any{"": 1}); err == nil {
		t.Fatal("empty dotted key accepted")
	}
}

func twoRepoFixture() []config.Repo {
	return []config.Repo{
		{Owner: "acme", Name: "widget", Base: "main", Preflight: [][]string{{"go", "version"}}},
		{Owner: "acme", Name: "gadget", Base: "main"},
	}
}

// TestApplyRepoFieldUpdateChangesOnlyTheMatchedRepo proves the update
// targets exactly one element by owner/name and leaves every other repo,
// and every other field on the matched repo, untouched -- including
// Preflight, which webui.RepoView never carries on the wire (the whole
// point of reading b.d.Cfg's live Repos instead of the trimmed view).
func TestApplyRepoFieldUpdateChangesOnlyTheMatchedRepo(t *testing.T) {
	repos, err := applyRepoFieldUpdate(twoRepoFixture(), "acme", "widget", "allow_concurrent", true)
	if err != nil {
		t.Fatalf("applyRepoFieldUpdate: %v", err)
	}
	if !repos[0].AllowConcurrent {
		t.Error("widget.AllowConcurrent = false, want true")
	}
	if len(repos[0].Preflight) != 1 || repos[0].Preflight[0][0] != "go" {
		t.Errorf("widget.Preflight = %v, want it untouched", repos[0].Preflight)
	}
	if repos[1].AllowConcurrent {
		t.Error("gadget.AllowConcurrent changed, want the non-matched repo left alone")
	}
}

func TestApplyRepoFieldUpdateSetsEachEditableField(t *testing.T) {
	repos, err := applyRepoFieldUpdate(twoRepoFixture(), "acme", "widget", "max_retries", float64(7))
	if err != nil {
		t.Fatalf("applyRepoFieldUpdate: %v", err)
	}
	if repos[0].MaxRetries != 7 {
		t.Errorf("MaxRetries = %d, want 7 (JSON numbers decode as float64)", repos[0].MaxRetries)
	}

	repos, err = applyRepoFieldUpdate(twoRepoFixture(), "acme", "widget", "review_enabled", true)
	if err != nil {
		t.Fatalf("applyRepoFieldUpdate: %v", err)
	}
	if !repos[0].ReviewEnabled {
		t.Error("ReviewEnabled = false, want true")
	}
}

func TestApplyRepoFieldUpdateRejectsUnknownRepo(t *testing.T) {
	_, err := applyRepoFieldUpdate(twoRepoFixture(), "acme", "does-not-exist", "allow_concurrent", true)
	if err == nil {
		t.Fatal("expected an error for a repo that is not configured")
	}
}

func TestApplyRepoFieldUpdateRejectsUneditableField(t *testing.T) {
	_, err := applyRepoFieldUpdate(twoRepoFixture(), "acme", "widget", "gate", []any{})
	if err == nil {
		t.Fatal("expected an error for a field outside RepoEditableFields")
	}
}

// TestApplyRepoFieldUpdateRejectsWrongValueType proves a bool field given a
// number (or vice versa) fails with a clear error instead of a silent
// zero-value write -- the JSON body is caller-controlled (the dashboard),
// not type-checked before it reaches this function.
func TestApplyRepoFieldUpdateRejectsWrongValueType(t *testing.T) {
	cases := []struct {
		field string
		value any
	}{
		{"allow_concurrent", "yes"},
		{"review_enabled", 1.0},
		{"max_retries", true},
	}
	for _, tc := range cases {
		if _, err := applyRepoFieldUpdate(twoRepoFixture(), "acme", "widget", tc.field, tc.value); err == nil {
			t.Errorf("field %q accepted wrong-typed value %#v", tc.field, tc.value)
		}
	}
}
