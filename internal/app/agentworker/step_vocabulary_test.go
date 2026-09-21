package agentworker

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
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
	want := make([]string, 0, len(workflow.BuiltinStepRegistry()))
	for name := range workflow.BuiltinStepRegistry() {
		want = append(want, name)
	}
	slices.Sort(want)
	if got := steps.StepTypes(); !slices.Equal(got, want) {
		t.Fatalf("the composition root resolves %v, want the shared provider set's shipped stages %v", got, want)
	}
	if err := steps.Register(stepVocabularyProbe{}); err != nil {
		t.Fatalf("register a provider through the root's manager: %v", err)
	}
	if _, resolved := steps.Registry()["probe.registered"]; !resolved {
		t.Fatal("a provider registered through the root's manager did not reach its vocabulary")
	}
}
