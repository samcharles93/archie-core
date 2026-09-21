package workflowsteps

import (
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// TestShippedVocabularyCoversEveryShippedStage is the guard that the bundled
// provider set really carries the shipped stages: a process that registers it
// can compile every shipped workflow, and one that does not has no vocabulary
// at all.
func TestShippedVocabularyCoversEveryShippedStage(t *testing.T) {
	t.Parallel()

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	want := make([]string, 0, len(workflow.BuiltinStepRegistry()))
	for name := range workflow.BuiltinStepRegistry() {
		want = append(want, name)
	}
	slices.Sort(want)
	if got := manager.StepTypes(); !slices.Equal(got, want) {
		t.Fatalf("registered vocabulary = %v, want the shipped stages %v", got, want)
	}

	// The vocabulary arrives by registration, not by implication: a manager
	// that was handed no provider set resolves nothing, which is what makes
	// every resolution site's use of the injected manager observable.
	if got := workflow.NewManager().StepTypes(); len(got) != 0 {
		t.Fatalf("an unregistered manager resolves %v, want nothing", got)
	}
}

// TestShippedStagesKeepTheirBehaviourThroughTheManager pins that the provider
// hands the builtin factories through unchanged. The shipped stages are typed,
// non-interpreted steps: each compiles to the stage the builtin registry
// carries and refuses settings. The one way this enumeration could keep every
// name and lose that guard is to rebuild the factories instead of forwarding
// them, and definition_test.go's assertion cannot see it -- that test drives
// BuiltinStepRegistry, which production no longer calls.
func TestShippedStagesKeepTheirBehaviourThroughTheManager(t *testing.T) {
	t.Parallel()

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	registry := manager.Registry()

	for _, entry := range workflow.ShippedDefinitions().Definitions {
		definition, err := workflow.ParseDefinition(entry.YAML, registry)
		if err != nil {
			t.Fatalf("parse shipped workflow %q through the registered vocabulary: %v", entry.ID, err)
		}
		compiled, err := workflow.ParseAndCompile(entry.YAML, registry)
		if err != nil {
			t.Fatalf("compile shipped workflow %q through the registered vocabulary: %v", entry.ID, err)
		}
		if len(compiled.Stages) != len(definition.Steps) {
			t.Fatalf("%s compiled %d stages, want %d", entry.ID, len(compiled.Stages), len(definition.Steps))
		}
		for i, step := range definition.Steps {
			want := strings.TrimPrefix(step.Type, entry.ID+".")
			if compiled.Stages[i].Name != want {
				t.Errorf("%s step %d (%s) compiled to stage %q, want the shipped stage %q: the registered factory is not the builtin one", entry.ID, i, step.Type, compiled.Stages[i].Name, want)
			}
		}
	}

	if _, err := workflow.ParseAndCompile("id: probe\nsteps:\n  - type: bootstrap.apply\n    settings:\n      command: rm -rf /\n", registry); err == nil || !strings.Contains(err.Error(), "accepts no settings") {
		t.Fatalf("ParseAndCompile() error = %v, want the shipped stage's settings refusal", err)
	}
}

// TestNewManagerIsRepeatableWithinOneProcess is the regression guard for
// registering the provider set into shared state: every construction is
// independent, so building the vocabulary twice in one process — as `-count=2`
// does — must succeed and produce the same vocabulary.
func TestNewManagerIsRepeatableWithinOneProcess(t *testing.T) {
	t.Parallel()

	first, err := NewManager()
	if err != nil {
		t.Fatalf("first NewManager: %v", err)
	}
	second, err := NewManager()
	if err != nil {
		t.Fatalf("second NewManager: %v", err)
	}
	if !slices.Equal(first.StepTypes(), second.StepTypes()) {
		t.Fatalf("two managers resolved different vocabularies: %v and %v", first.StepTypes(), second.StepTypes())
	}
	if !slices.Equal(first.Providers(), second.Providers()) {
		t.Fatalf("two managers registered different providers: %v and %v", first.Providers(), second.Providers())
	}
}
