package agentworker

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// stepVocabularyProbe is a provider set of one, used only to prove the
// manager a root builds is live: registering through it must reach its
// vocabulary.
type stepVocabularyProbe struct{}

func (stepVocabularyProbe) Name() string { return "probe" }

func (stepVocabularyProbe) StepTypes() []workflow.StepType {
	return []workflow.StepType{{Name: "probe.registered", Factory: func(yaml.Node) (workflow.Stage, error) {
		return workflow.Stage{Name: "probe.registered"}, nil
	}}}
}

// TestProductionWorkerDependenciesRegisterTheSharedStepVocabulary pins the
// executing side's composition root. productionWorkerDependencies is reached
// only by Run, so nothing else fails if it stops registering the shared
// provider set (internal/infrastructure/workflowsteps) and hands the worker a
// manager with no shipped stage: every compile then fails at run time, in a
// container, per task.
func TestProductionWorkerDependenciesRegisterTheSharedStepVocabulary(t *testing.T) {
	dependencies, err := productionWorkerDependencies()
	if err != nil {
		t.Fatalf("productionWorkerDependencies: %v", err)
	}
	requireSharedStepVocabulary(t, dependencies.steps)
}

// requireSharedStepVocabulary holds a composition root's manager to both halves
// of the contract: it resolves the shipped vocabulary, and it is live enough to
// take a provider registered through it.
func requireSharedStepVocabulary(t *testing.T, steps *workflow.Manager) {
	t.Helper()
	if steps == nil {
		t.Fatal("the composition root registered no workflow step vocabulary")
	}
	got := steps.StepTypes()
	// Containment, not equality: adding a provider to the shared set
	// (workflowsteps.Providers) is the documented way a step type arrives, and
	// that must not have to edit this guard. What it holds is that the shipped
	// stages are all present, which is what a root regressing to
	// workflow.NewManager() loses.
	for name := range workflow.BuiltinStepRegistry() {
		if !slices.Contains(got, name) {
			t.Errorf("the composition root resolves %v, which is missing the shipped stage %q", got, name)
		}
	}
	// The other half is provenance: every name the root resolves must be
	// declared by the shared provider set, not by a near-identical provider of
	// the root's own, because that set is what ties this process's vocabulary to
	// the State Store that validates against it. It is content, not identity --
	// the shipped provider rebuilds its factories on every call.
	declared := sharedStepTypeNames()
	for _, name := range got {
		if !slices.Contains(declared, name) {
			t.Errorf("the composition root resolves %q, which no provider in the shared set declares", name)
		}
	}
	if err := steps.Register(stepVocabularyProbe{}); err != nil {
		t.Fatalf("register a provider through the root's manager: %v", err)
	}
	if _, resolved := steps.Registry()["probe.registered"]; !resolved {
		t.Fatal("a provider registered through the root's manager did not reach its vocabulary")
	}
}

// sharedStepTypeNames returns the step types the shared provider set declares,
// read through the same Providers() seam the composition roots register.
func sharedStepTypeNames() []string {
	names := make([]string, 0)
	for _, provider := range workflowsteps.Providers() {
		for _, stepType := range provider.StepTypes() {
			names = append(names, stepType.Name)
		}
	}
	slices.Sort(names)
	return names
}
