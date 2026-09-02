package workflow

import (
	"fmt"
	"os"
	"strings"

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
	// Arbitrary-label bindings first: a playbook-declared label is an
	// explicit, load-time-error-checked signal, so it wins over the kind
	// defaults (which cover only bug/feature/bootstrap).
	if lb := activeLabelWorkflows; lb != nil {
		for _, label := range workintake.SplitLabels(labels) {
			name, ok := lb[label]
			if !ok {
				continue
			}
			if wf, ok := reg[name]; ok {
				return wf, true
			}
		}
	}
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

// LabelWorkflows maps a forge issue label to the registered workflow name
// it prefers. This is the second slice of docs/prds/eda-playbook-engine.md:
// the label vocabulary itself -- not just the closed bug/feature/bootstrap
// kind set -- becomes playbook data. The closed Kind/NATS-subject set
// (workintake) is deliberately untouched; this map only extends binding
// authority for labels the kind layer does not own.
type LabelWorkflows map[string]string

// activeLabelWorkflows is what workflowForLabels consults for arbitrary
// labels. Set once at daemon startup by SetLabelWorkflows; nil means "no
// arbitrary-label bindings" (there is no built-in default -- the closed
// kind set already owns bug/feature/bootstrap, and loading bindings for
// those is rejected as a collision), mirroring activeKindWorkflows'
// lifecycle.
var activeLabelWorkflows LabelWorkflows

// SetLabelWorkflows overrides the label-to-workflow-name bindings Route()
// consults, e.g. from a file loaded by LoadLabelWorkflowsYAML. Passing nil
// restores kind-only routing.
func SetLabelWorkflows(lw LabelWorkflows) {
	activeLabelWorkflows = lw
}

// LoadLabelWorkflowsYAML reads a label-to-workflow-name binding file. An
// empty path returns (nil, nil) -- "no file configured" means "no
// arbitrary-label bindings", matching the kind layer's convention.
//
// A label already owned by the closed kind set (bug/feature/bootstrap) is a
// definition collision and returns an error: two authorities claiming one
// label is the caller's fault, reported not arbitrated. Empty labels or
// empty workflow names are likewise rejected -- the schema defines what is
// accepted, anything else is dropped-and-reported per the design doc.
func LoadLabelWorkflowsYAML(path string) (LabelWorkflows, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read label-workflows file %s: %w", path, err)
	}
	raw := map[string]string{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse label-workflows file %s: %w", path, err)
	}
	lw := make(LabelWorkflows, len(raw))
	for label, name := range raw {
		trimmed := strings.TrimSpace(label)
		if trimmed == "" {
			return nil, fmt.Errorf("label-workflows file %s: empty label is not a valid binding", path)
		}
		if name == "" {
			return nil, fmt.Errorf("label-workflows file %s: label %q has no workflow name", path, trimmed)
		}
		if _, ok := defaultKindWorkflows[workintake.Kind(trimmed)]; ok {
			return nil, fmt.Errorf("label-workflows file %s: label %q is already owned by the kind routing layer", path, trimmed)
		}
		if _, exists := lw[trimmed]; exists {
			return nil, fmt.Errorf("label-workflows file %s: duplicate binding for label %q", path, trimmed)
		}
		lw[trimmed] = name
	}
	return lw, nil
}
