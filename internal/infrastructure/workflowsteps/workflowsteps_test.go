package workflowsteps

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

	got := manager.StepTypes()
	// Every shipped stage is present...
	for name := range workflow.BuiltinStepRegistry() {
		if !slices.Contains(got, name) {
			t.Errorf("registered vocabulary %v is missing the shipped stage %q", got, name)
		}
	}
	// ...and the vocabulary is exactly what the provider set declares. The
	// expectation is derived from Providers() rather than from
	// workflow.BuiltinStepRegistry(): adding a provider to this set is the
	// documented way a step type arrives, and that must move both sides of this
	// assertion together instead of having to edit it.
	if want := declaredStepTypes(); !slices.Equal(got, want) {
		t.Fatalf("registered vocabulary = %v, want the provider set's declared step types %v", got, want)
	}

	// The vocabulary arrives by registration, not by implication: a manager
	// that was handed no provider set resolves nothing, which is what makes
	// every resolution site's use of the injected manager observable.
	if got := workflow.NewManager().StepTypes(); len(got) != 0 {
		t.Fatalf("an unregistered manager resolves %v, want nothing", got)
	}
}

// declaredStepTypes returns every step type the provider set contributes,
// sorted, so a test can hold a manager to the set it was built from.
func declaredStepTypes() []string {
	names := make([]string, 0)
	for _, provider := range Providers() {
		for _, stepType := range provider.StepTypes() {
			names = append(names, stepType.Name)
		}
	}
	slices.Sort(names)
	return names
}

// TestShippedStagesKeepTheirBehaviourThroughTheManager pins that the provider
// hands the builtin factories through unchanged. The shipped stages are typed,
// non-interpreted steps: each compiles to the stage the builtin registry's own
// factory builds for that step type -- the same name, and Run present or absent
// the same way -- and refuses settings. The one way this enumeration could keep
// every name and lose that guard is to rebuild the factories instead of
// forwarding them, and definition_test.go's assertion cannot see it -- that test
// drives BuiltinStepRegistry directly, which no resolution site does any more:
// production reaches it only inside the shipped provider below.
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
			// The expectation is the stage the builtin registry's own factory
			// builds for this step type, not a name derived from the step type:
			// that is what "the registered factory is the builtin one" means
			// for a Stage, which is a name plus an uncomparable func.
			shipped, err := workflow.BuiltinStepRegistry()[step.Type](yaml.Node{})
			if err != nil {
				t.Fatalf("build shipped stage %s: %v", step.Type, err)
			}
			if compiled.Stages[i].Name != shipped.Name {
				t.Errorf("%s step %d (%s) compiled to stage %q, want the shipped stage %q: the registered factory is not the builtin one", entry.ID, i, step.Type, compiled.Stages[i].Name, shipped.Name)
			}
			if (compiled.Stages[i].Run != nil) != (shipped.Run != nil) {
				t.Errorf("%s step %d (%s) compiled to a stage whose Run is present=%t, want the shipped stage's present=%t", entry.ID, i, step.Type, compiled.Stages[i].Run != nil, shipped.Run != nil)
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
