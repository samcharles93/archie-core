package playbook

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestLoadSingleWorkflowActionRoundTrip: a playbook with trigger + one
// workflow-kind action + optional when loads and dispatches to the correct
// registered workflow when trigger+when match.
func TestLoadSingleWorkflowActionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    when: event.priority == 3
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(store.Playbooks) != 1 {
		t.Fatalf("loaded %d playbooks, want 1", len(store.Playbooks))
	}
	pb := store.Playbooks[0]
	if pb.Actions[0].Position != "workflow" {
		t.Errorf("position = %q, want workflow", pb.Actions[0].Position)
	}

	// Match + dispatch: kind bug, priority 3 -> tdd.
	decision, ok := store.Dispatch(DispatchInput{
		Labels: []string{"bug"},
		Kind:   "bug",
		Event:  map[string]any{"priority": 3},
	})
	if !ok {
		t.Fatal("Dispatch matched nothing, want the tdd workflow")
	}
	if decision.Workflow != "tdd" {
		t.Errorf("Dispatched workflow = %q, want tdd", decision.Workflow)
	}
	if decision.PlaybookID != "pb.yaml" || decision.Version != pb.Version {
		t.Errorf("decision provenance = (%q, %q), want (pb.yaml, %q)", decision.PlaybookID, decision.Version, pb.Version)
	}
}

// TestDispatchConditionFalseSkips: a when that compiles but evaluates false
// at dispatch correctly skips dispatch (nil returned, not silently ignored).
func TestDispatchConditionFalseSkips(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    when: event.priority == 5
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	decision, ok := store.Dispatch(DispatchInput{
		Labels: []string{"bug"},
		Kind:   "bug",
		Event:  map[string]any{"priority": 3},
	})
	if ok {
		t.Fatalf("Dispatch = %v, want no match (condition false -> skip)", decision)
	}
}

// TestLoadTwoActionsIsLoadFailure: the hard boundary -- a playbook declaring
// 2+ actions is a reported load failure.
func TestLoadTwoActionsIsLoadFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
  - position: workflow
    workflow: implement
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load(2 actions) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "exactly one action") {
		t.Errorf("Load error = %q, want the one-action rule named", err.Error())
	}
}

// TestLoadNonWorkflowPositionIsLoadFailure: a playbook with a non-workflow
// action position is a reported load failure.
func TestLoadNonWorkflowPositionIsLoadFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: module
    kind: log
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load(module action) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "workflow") {
		t.Errorf("Load error = %q, want the position restriction named", err.Error())
	}
}

// TestLoadWhenCompileFailureDropsPlaybook: a when compile failure at load is
// reported and the playbook is dropped (same reject-at-load shape as the
// collision rule).
func TestLoadWhenCompileFailureDropsPlaybook(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    when: event.label ==
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load(bad when) = nil, want compile failure reported")
	}
}

// TestLoadEmptyTriggerIsLoadFailure: a playbook without a trigger cannot
// match anything -- rejected at load.
func TestLoadEmptyTriggerIsLoadFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
actions:
  - position: workflow
    workflow: tdd
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load(no trigger) = nil, want load failure")
	}
}

// TestDispatchTriggerMismatchSkips: a playbook whose trigger does not match
// the incoming labels does not dispatch.
func TestDispatchTriggerMismatchSkips(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: feature
actions:
  - position: workflow
    workflow: tdd
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	decision, ok := store.Dispatch(DispatchInput{
		Labels: []string{"bug"},
		Kind:   "bug",
		Event:  map[string]any{},
	})
	if ok {
		t.Fatalf("Dispatch = %v, want no match (trigger kind mismatch)", decision)
	}
}

// TestDispatchNoWhenMatchesOnTriggerAlone: without a when, trigger match is
// sufficient to dispatch.
func TestDispatchNoWhenMatchesOnTriggerAlone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	decision, ok := store.Dispatch(DispatchInput{
		Labels: []string{"bug"},
		Kind:   "bug",
		Event:  map[string]any{},
	})
	if !ok || decision.Workflow != "tdd" {
		t.Fatalf("Dispatch = %v, want tdd (no when = always match)", decision)
	}
}

// TestLoadOneInvalidFileFailsWholeLoad: the reject-at-load rule is store-
// level -- one malformed playbook in a directory fails the entire load, so
// a partially-valid set never starts (same shape as a routing collision).
func TestLoadOneInvalidFileFailsWholeLoad(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
`)
	writeFile(t, dir, "bad.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
  - position: workflow
    workflow: implement
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load(dir with one bad playbook) = nil, want whole-load failure")
	}
}

// TestLoadMissingDirIsEmptyStore: a nonexistent directory is an empty store
// (no playbooks), matching the flat binding loaders' convention.
func TestLoadMissingDirIsEmptyStore(t *testing.T) {
	store, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load(missing dir) = %v, want nil", err)
	}
	if len(store.Playbooks) != 0 {
		t.Fatalf("loaded %d playbooks, want 0", len(store.Playbooks))
	}
}

// TestPlaybookIDIsPathRelative: the playbook's ID is its path relative to the
// configured directory root, so it is stable across re-Loads of the same file
// and unique within one directory (the EDA loader has no collision detector;
// per-directory-unique filenames guarantee it).
func TestPlaybookIDIsPathRelative(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(store.Playbooks) != 1 {
		t.Fatalf("loaded %d playbooks, want 1", len(store.Playbooks))
	}
	pb := store.Playbooks[0]
	if pb.ID != "pb.yaml" {
		t.Errorf("ID = %q, want %q (path relative to dir root)", pb.ID, "pb.yaml")
	}
}

// TestPlaybookVersionIsContentDerived: the version is a content hash of the
// loaded file, recomputed on every load -- stable for unchanged content,
// changing when content changes (mirrors Binding.Version's provenance pin).
func TestPlaybookVersionIsContentDerived(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
`)
	storeA, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if storeA.Playbooks[0].Version == "" {
		t.Fatal("Version = empty, want a content hash")
	}

	// Re-load unchanged content: identical version.
	storeB, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if storeA.Playbooks[0].Version != storeB.Playbooks[0].Version {
		t.Fatalf("Version changed for unchanged content: %q -> %q",
			storeA.Playbooks[0].Version, storeB.Playbooks[0].Version)
	}

	// Change content: version must change.
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: feature
actions:
  - position: workflow
    workflow: implement
`)
	storeC, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if storeA.Playbooks[0].Version == storeC.Playbooks[0].Version {
		t.Fatal("Version unchanged for changed content, want a change")
	}
}

// TestDispatchInputCarriesTaskIdentity: the originating task's identity is
// carried on the dispatch input end to end, so a caller can derive the
// event_id half of the playbook_dispatches ledger key. The chosen identity is
// the TaskEnvelope.IdempotencyKey() string ("archie:owner/repo/number") --
// the value available at the discovery/dispatch point (pollNATS + webhook
// receiver both compute kind/labels from a TaskEnvelope before any
// workflow.Task row exists), NOT a workflow.Task.ID int64.
func TestDispatchInputCarriesTaskIdentity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	input := DispatchInput{
		Labels: []string{"bug"},
		Kind:   "bug",
		TaskID: "archie:samcharles93/archie-core/42",
		Event:  map[string]any{"priority": 3},
	}
	if _, ok := store.Dispatch(input); !ok {
		t.Fatal("Dispatch matched nothing, want the tdd workflow")
	}
	// The input still carries the identity after dispatch; the caller uses it
	// to key the ledger.
	if got, want := input.TaskID, "archie:samcharles93/archie-core/42"; got != want {
		t.Fatalf("TaskID = %q, want %q", got, want)
	}
}

// TestShippedExamplePlaybookLoads verifies the operator-facing example
// (examples/eda-playbooks/bug-tdd.yaml) loads through the real loader --
// the proof the shipped shape works, not just test fixtures.
func TestShippedExamplePlaybookLoads(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "examples", "eda-playbooks")
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(shipped example): %v", err)
	}
	byID := map[string]*Playbook{}
	for _, pb := range store.Playbooks {
		byID[pb.ID] = pb
	}
	pb, ok := byID["bug-tdd.yaml"]
	if !ok {
		t.Fatalf("loaded %v, want the bug-tdd example", slices.Sorted(maps.Keys(byID)))
	}
	if pb.Actions[0].Workflow != "tdd" {
		t.Errorf("example workflow = %q, want tdd", pb.Actions[0].Workflow)
	}
	// The label-trigger example is the instance-defined path: a label the kind
	// vocabulary does not recognise, bound to a workflow with no code change.
	labelled, ok := byID["label-trigger.yaml"]
	if !ok {
		t.Fatalf("loaded %v, want the label-trigger example", slices.Sorted(maps.Keys(byID)))
	}
	if labelled.Trigger.Kind != "" || len(labelled.Trigger.Labels) == 0 {
		t.Errorf("label-trigger example = %+v, want a labels-only trigger", labelled.Trigger)
	}
	if got, _ := store.Dispatch(DispatchInput{
		Labels: labelled.Trigger.Labels,
		Kind:   "default",
		Event:  map[string]any{"labels": labelled.Trigger.Labels},
	}); got.Workflow != labelled.Actions[0].Workflow {
		t.Errorf("Dispatch(label-trigger) = %q, want %q", got.Workflow, labelled.Actions[0].Workflow)
	}
	// The example's when reads event.labels, one of the fields the daemon's
	// intake really supplies -- so this exercises the shipped condition
	// against the production event surface, not a test-only field.
	got, ok := store.Dispatch(DispatchInput{
		Labels: []string{"bug", "regression"},
		Kind:   "bug",
		Event:  map[string]any{"kind": "bug", "labels": []string{"bug", "regression"}},
	})
	if !ok || got.Workflow != "tdd" {
		t.Fatalf("Dispatch(example) = %v, want tdd", got)
	}
	// And skips when the label the condition names is absent.
	if got, ok := store.Dispatch(DispatchInput{
		Labels: []string{"bug"},
		Kind:   "bug",
		Event:  map[string]any{"kind": "bug", "labels": []string{"bug"}},
	}); ok {
		t.Fatalf("Dispatch(unlabelled) = %v, want no match (when false)", got)
	}
}

// TestLoadActionIDDispatches: a valid action id survives load and is
// threaded into the dispatch Decision's ActionID.
func TestLoadActionIDDispatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    id: notify
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pb := store.Playbooks[0]
	if pb.Actions[0].ID != "notify" {
		t.Errorf("action id = %q, want notify", pb.Actions[0].ID)
	}
	decision, ok := store.Dispatch(DispatchInput{Kind: "bug", Event: map[string]any{}})
	if !ok {
		t.Fatal("Dispatch matched nothing, want the tdd workflow")
	}
	if decision.ActionID != "notify" {
		t.Errorf("decision ActionID = %q, want notify", decision.ActionID)
	}
}

// TestLoadMalformedActionIDFails: an action id outside the stable-identifier
// shape is a reported load failure (the same reject-at-load rule).
func TestLoadMalformedActionIDFails(t *testing.T) {
	for _, id := range []string{"Notify", "foo bar", "1st", "-dash", "trailing-", "a..b"} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - position: workflow\n    workflow: tdd\n    id: "+id+"\n")
			_, err := Load(dir)
			if err == nil {
				t.Fatalf("Load(id %q) = nil, want load failure", id)
			}
		})
	}
}

// TestValidateActionIDs: the shape/uniqueness helper is the load-boundary's
// id gate, unit-tested directly because the one-action boundary makes
// duplicates unreachable through Load today.
func TestValidateActionIDs(t *testing.T) {
	if err := validateActionIDs([]rawAction{{ID: "notify"}, {ID: "notify"}}); err == nil {
		t.Fatal("validateActionIDs(duplicate) = nil, want error")
	}
	if err := validateActionIDs([]rawAction{{ID: "notify"}, {ID: "build"}}); err != nil {
		t.Fatalf("validateActionIDs(distinct) = %v, want nil", err)
	}
	if err := validateActionIDs([]rawAction{{ID: ""}, {ID: "build"}}); err != nil {
		t.Fatalf("validateActionIDs(optional empty id) = %v, want nil", err)
	}
}

// TestLoadWhenReferencesUnknownActionIDFails: a when reading actions.<id>
// with no prior action declaring that id is a load failure naming the
// playbook path and the id -- never a runtime miss.
func TestLoadWhenReferencesUnknownActionIDFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    when: actions.notify.result.delivered == true
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load(when referencing undeclared action id) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "pb.yaml") {
		t.Errorf("Load error = %q, want the playbook path named", err.Error())
	}
	if !strings.Contains(err.Error(), "notify") {
		t.Errorf("Load error = %q, want the unknown id named", err.Error())
	}
}

// TestDispatchNilStoreMatchesNothing: a nil store is the state a composition
// root is in before the playbook load runs, and callers hold it through an
// interface where a typed nil is not a nil interface.
func TestDispatchNilStoreMatchesNothing(t *testing.T) {
	var store *Store
	if decision, ok := store.Dispatch(DispatchInput{Labels: []string{"bug"}, Kind: "bug"}); ok {
		t.Fatalf("Dispatch = %v, want no match from a nil store", decision)
	}
}
