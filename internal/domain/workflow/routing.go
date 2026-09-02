package workflow

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
)

// KindWorkflows maps an intake kind to the registered workflow name it
// prefers. The zero value is not itself a valid override -- pass the result
// of LoadKindWorkflowsYAML to SetKindWorkflows, or nil to restore defaults.
type KindWorkflows map[workintake.Kind]string

// defaultKindWorkflows names the registry entry each intake kind prefers
// when no override has been loaded. Bootstrap exercises the full pipeline
// deterministically (no LLM spend) -- invites, clone, push, PR, labels.
//
// The label vocabulary itself belongs to workintake, which is what reads
// forge issues. This package owns only the choice of workflow for a kind:
// keeping both here meant the transport and the registry each had their own
// copy of the label table.
var defaultKindWorkflows = KindWorkflows{
	workintake.KindBug:       "tdd",
	workintake.KindFeature:   "feasibility",
	workintake.KindBootstrap: "bootstrap",
}

// activeKindWorkflows is what workflowForLabels actually reads. Set once at
// daemon startup by SetKindWorkflows; nil means "use defaultKindWorkflows".
// This is package state deliberately, matching the once-at-startup,
// never-concurrent-with-Route lifecycle SkillsDir/PluginDir already have --
// Route() is never called before the daemon finishes loading configuration.
var activeKindWorkflows KindWorkflows

// SetKindWorkflows overrides the kind-to-workflow-name bindings Route()
// consults, e.g. from a file loaded by LoadKindWorkflowsYAML. Passing nil
// restores the built-in defaults -- the first slice of
// docs/prds/eda-playbook-engine.md: which workflow a Kind prefers is data,
// not a Go literal, without touching labelKinds or the closed NATS-subject
// set that Kind itself still governs.
func SetKindWorkflows(kw KindWorkflows) {
	activeKindWorkflows = kw
}

// LoadKindWorkflowsYAML reads a kind-to-workflow-name binding file. An empty
// path returns (nil, nil) -- "no file configured" means "use defaults",
// matching SkillsDir's own empty-means-built-ins-only convention rather than
// requiring callers to stat first.
//
// A kind the workintake package does not recognise is a definition failure,
// per the design doc's rule: the schema (here, the closed Kind vocabulary)
// defines what's accepted, and anything else is the caller's fault --
// reported as an error, not silently dropped or guessed at.
func LoadKindWorkflowsYAML(path string) (KindWorkflows, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read kind-workflows file %s: %w", path, err)
	}
	raw := map[string]string{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse kind-workflows file %s: %w", path, err)
	}
	kw := make(KindWorkflows, len(raw))
	for k, name := range raw {
		kind := workintake.Kind(k)
		if err := kind.Validate(); err != nil {
			return nil, fmt.Errorf("kind-workflows file %s: %w", path, err)
		}
		kw[kind] = name
	}
	return kw, nil
}

// workflowForLabels returns the registered workflow name for a task's
// labels, trying each recognised kind in label order so a "bug,feature" task
// still reaches feasibility when no tdd workflow is registered.
func workflowForLabels(reg Registry, labels string) (Workflow, bool) {
	kw := activeKindWorkflows
	if kw == nil {
		kw = defaultKindWorkflows
	}
	for _, kind := range workintake.KindsForLabels(workintake.SplitLabels(labels)) {
		name, ok := kw[kind]
		if !ok {
			continue
		}
		if wf, ok := reg[name]; ok {
			return wf, true
		}
	}
	return Workflow{}, false
}
