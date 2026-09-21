package workflowsteps

import (
	"slices"
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
