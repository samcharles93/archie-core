package workflow

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
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

// LoadPlaybookDirs reads every *.yaml / *.yml file across the configured
// directories, treats each as a binding file (kind keys or arbitrary label
// keys), and merges them into a single kind map and a single label map.
// This is the third slice of docs/prds/eda-playbook-engine.md (t2db.11):
// a list of directories is an additional, independent input to the two
// single-file fields, which remain supported unchanged -- it is not a
// replacement.
//
// Collision rule (the load-bearing case this slice exists to exercise): a
// binding key (kind or label) declared by more than one source -- two
// files in one directory, or two configured directories -- is a reported
// load failure. Nothing is silently arbitrated by source order or version;
// the colliding definitions are dropped and the error returned to the
// caller. Source order is deterministic (configured dir order, then sorted
// filenames) so a reported error is reproducible.
//
// An empty list, or directories that do not exist, returns (nil, nil):
// "no playbook dir configured" means "no bindings", matching the
// single-file fields' empty-means-defaults convention.
func LoadPlaybookDirs(dirs []string) (KindWorkflows, LabelWorkflows, error) {
	kw := make(KindWorkflows)
	lw := make(LabelWorkflows)
	for _, dir := range dirs {
		names, err := sortedPlaybookFilenames(dir)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range names {
			if err := loadPlaybookFile(filepath.Join(dir, name), kw, lw); err != nil {
				return nil, nil, err
			}
		}
	}

	if len(kw) == 0 {
		kw = nil
	}
	if len(lw) == 0 {
		lw = nil
	}
	return kw, lw, nil
}

// sortedPlaybookFilenames lists the *.yaml / *.yml files directly inside dir,
// sorted so LoadPlaybookDirs' source order is deterministic. A missing
// directory is not an error -- it contributes no filenames, matching
// LoadPlaybookDirs' "no playbook dir configured" convention.
func sortedPlaybookFilenames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read playbook dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".yaml" || ext == ".yml" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// loadPlaybookFile reads one playbook binding file and merges its keys into
// kw/lw via bindPlaybookKey, mutating both in place.
func loadPlaybookFile(path string, kw KindWorkflows, lw LabelWorkflows) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read playbook file %s: %w", path, err)
	}
	raw := map[string]string{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse playbook file %s: %w", path, err)
	}
	for key, value := range raw {
		if err := bindPlaybookKey(path, key, value, kw, lw); err != nil {
			return err
		}
	}
	return nil
}

// bindPlaybookKey classifies one playbook binding key as a closed-vocabulary
// kind or an arbitrary label, then merges it into kw/lw -- applying the same
// collision and empty-value rules LoadPlaybookDirs' doc comment describes.
func bindPlaybookKey(path, key, value string, kw KindWorkflows, lw LabelWorkflows) error {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return fmt.Errorf("playbook file %s: empty binding key", path)
	}
	if value == "" {
		return fmt.Errorf("playbook file %s: key %q has no workflow name", path, trimmed)
	}
	if err := workintake.Kind(trimmed).Validate(); err == nil {
		// It is a kind binding: the closed Kind vocabulary accepts it.
		if existing, ok := kw[workintake.Kind(trimmed)]; ok {
			return fmt.Errorf("playbook dirs: kind %q bound in two sources (%q and %q)", trimmed, existing, value)
		}
		kw[workintake.Kind(trimmed)] = value
		return nil
	}
	// Otherwise it is an arbitrary-label binding. Reject kind-owned labels
	// and empty labels with the same rules the single-file loader applies.
	if _, ok := defaultKindWorkflows[workintake.Kind(trimmed)]; ok {
		return fmt.Errorf("playbook file %s: label %q is already owned by the kind routing layer", path, trimmed)
	}
	if existing, ok := lw[trimmed]; ok {
		return fmt.Errorf("playbook dirs: label %q bound in two sources (%q and %q)", trimmed, existing, value)
	}
	lw[trimmed] = value
	return nil
}

// MergeKindWorkflows merges extra into base, failing on a key bound by
// both. The single-file field and the playbook directory are independent
// sources; a key each claims is a collision, not a precedence question
// (t2db.11). Nil arguments are treated as empty. A nil nil result is
// returned only when both inputs are nil/empty, so Set* can keep its
// nil-means-defaults convention. This is a domain-package naming helper
// the composition layer calls.
func MergeKindWorkflows(base, extra KindWorkflows) (KindWorkflows, error) {
	merged := make(KindWorkflows, len(base)+len(extra))
	maps.Copy(merged, base)
	for k, v := range extra {
		if _, exists := merged[k]; exists {
			return nil, fmt.Errorf("workflow binding collision: kind %q bound in two sources", k)
		}
		merged[k] = v
	}
	if len(merged) == 0 {
		return nil, nil
	}
	return merged, nil
}

// MergeLabelWorkflows is the label counterpart of MergeKindWorkflows.
func MergeLabelWorkflows(base, extra LabelWorkflows) (LabelWorkflows, error) {
	merged := make(LabelWorkflows, len(base)+len(extra))
	maps.Copy(merged, base)
	for k, v := range extra {
		if _, exists := merged[k]; exists {
			return nil, fmt.Errorf("workflow binding collision: label %q bound in two sources", k)
		}
		merged[k] = v
	}
	if len(merged) == 0 {
		return nil, nil
	}
	return merged, nil
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
