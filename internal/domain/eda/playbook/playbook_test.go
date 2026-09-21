package playbook

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
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
	if !strings.Contains(err.Error(), "pb.yaml") {
		t.Errorf("Load error = %q, want the playbook path named", err.Error())
	}
	if !strings.Contains(err.Error(), "when condition") {
		t.Errorf("Load error = %q, want the `when condition` field label named", err.Error())
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

// TestLoadWhenActionsReferenceFails: a when that reads the `actions` context
// root in a form that cannot be pinned to a prior action id at load fails the
// whole load, naming the playbook path -- never a runtime miss. Under the
// per-playbook object type these forms are rejected at compile time: the
// field-selection spelling names the undefined id, while a dynamic index, an
// `in` test, or a size read fails with cel-go's overload error (no id name).
func TestLoadWhenActionsReferenceFails(t *testing.T) {
	tests := []struct {
		name   string
		when   string
		wantID string
	}{
		{name: "undeclared field selection", when: `actions.a.result.x == true`, wantID: "a"},
		{name: "dynamic index on actions", when: `actions["a"].result.x == true`},
		{name: "non-literal index key", when: `actions[key].result.x == true`},
		{name: "in operator on actions", when: `"notify" in actions`},
		{name: "size of actions", when: `size(actions) > 0`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - position: workflow\n    workflow: tdd\n    when: "+tc.when+"\n")
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load = nil, want load failure")
			}
			if !strings.Contains(err.Error(), "pb.yaml") {
				t.Errorf("Load error = %q, want the playbook path named", err.Error())
			}
			if tc.wantID != "" && !strings.Contains(err.Error(), tc.wantID) {
				t.Errorf("Load error = %q, want the unknown id %q named", err.Error(), tc.wantID)
			}
		})
	}
}

// TestUnknownActionReferenceGeneralRule: the reference check compares the
// statically-resolved ids against the declared ids of earlier actions, so it
// already behaves correctly when the one-action boundary later relaxes (the
// Load path can only exercise the empty-prior set today). The ids are passed
// directly: under the per-playbook object type an undeclared `actions.<id>`
// read is rejected at compile time, so there is no longer an env that compiles
// one to classify.
func TestUnknownActionReferenceGeneralRule(t *testing.T) {
	// a declared on the prior action: known.
	if id, unknown := unknownActionReference([]rawAction{{ID: "a"}}, 1, []string{"a"}); unknown {
		t.Fatalf("unknownActionReference(declared prior) = (%q, true), want known", id)
	}
	// Two static reads of a in one expression resolve to the one declared id
	// (ActionReferences de-duplicates), so the de-duplicated id is known.
	if id, unknown := unknownActionReference([]rawAction{{ID: "a"}}, 1, []string{"a", "a"}); unknown {
		t.Fatalf("unknownActionReference(deduplicated declared id) = (%q, true), want known", id)
	}
	// A different prior id leaves b unknown.
	if id, unknown := unknownActionReference([]rawAction{{ID: "a"}}, 1, []string{"b"}); !unknown || id != "b" {
		t.Fatalf("unknownActionReference(undeclared) = (%q, %v), want (b, true)", id, unknown)
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

// TestLoadActionArgsEvaluates: a workflow action's args values are compiled as
// CEL at load (a quoted string literal, a number literal, and an event read)
// and EvalArgs evaluates them against the dispatch context to the expected
// name->value map.
func TestLoadActionArgsEvaluates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    args:
      message: '"build finished"'
      priority: '3'
      label: 'event.label'
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pb := store.Playbooks[0]
	if len(pb.Actions[0].Args) != 3 {
		t.Fatalf("Action.Args = %#v, want 3 compiled programs", pb.Actions[0].Args)
	}
	for _, key := range []string{"message", "priority", "label"} {
		if pb.Actions[0].Args[key] == nil {
			t.Errorf("Action.Args[%q] = nil, want a compiled program", key)
		}
	}

	got, err := store.EvalArgs(pb.Actions[0], DispatchInput{
		Event: map[string]any{"label": "bug"},
	})
	if err != nil {
		t.Fatalf("EvalArgs: %v", err)
	}
	want := map[string]any{
		"message":  "build finished",
		"priority": int64(3),
		"label":    "bug",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EvalArgs = %#v, want %#v", got, want)
	}
}

// TestLoadActionArgsFailures: every args value is compiled at load and the
// same reject-at-load rule as `when` applies -- a CEL syntax error, an
// undeclared root, a reference to an unknown actions.<id>, and an
// unresolvable actions reference all fail the whole load, naming the playbook
// path and the offending args key.
func TestLoadActionArgsFailures(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantSub []string
	}{
		{
			name: "syntax error",
			doc: `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    args:
      message: 'build finished'
`,
			wantSub: []string{"pb.yaml", `args["message"]`},
		},
		{
			name: "undeclared root",
			doc: `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    args:
      label: 'foo.bar == 1'
`,
			wantSub: []string{"pb.yaml", `args["label"]`, "foo"},
		},
		{
			name: "unknown action id",
			doc: `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    args:
      label: 'actions.notify.result.x == true'
`,
			wantSub: []string{"pb.yaml", `args["label"]`, "notify"},
		},
		{
			name: "unresolvable actions reference",
			doc: `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    args:
      label: 'actions[event.kind].result.x == true'
`,
			wantSub: []string{"pb.yaml", `args["label"]`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", tc.doc)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load = nil, want load failure")
			}
			for _, want := range tc.wantSub {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestCompileArgsReportsFirstKeyDeterministically: ranging a Go map would
// report a random offending key when more than one args value is invalid; the
// loader must report the alphabetically-first key every time.
func TestCompileArgsReportsFirstKeyDeterministically(t *testing.T) {
	env := expr.NewEnv()
	raw := map[string]string{
		"z.bad": "event.missing ==",
		"a.bad": "event.missing ==",
	}
	for range 64 {
		_, err := compileArgs("pb.yaml", raw, env, nil)
		if err == nil {
			t.Fatal("compileArgs = nil, want error")
		}
		if !strings.Contains(err.Error(), `args["a.bad"]`) {
			t.Fatalf("compileArgs error = %q, want the alphabetically-first key args[%q]", err.Error(), "a.bad")
		}
		if strings.Contains(err.Error(), `args["z.bad"]`) {
			t.Fatalf("compileArgs error = %q, must name the alphabetically-first key, not %q", err.Error(), "z.bad")
		}
	}
}

// TestEvalArgsMissingEventFieldReturnsError: evaluating an args value that
// reads a missing event field returns an error (never a panic).
func TestEvalArgsMissingEventFieldReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    args:
      label: 'event.missing'
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = store.EvalArgs(store.Playbooks[0].Actions[0], DispatchInput{Event: map[string]any{}})
	if err == nil {
		t.Fatal("EvalArgs(missing event field) = nil error, want error (not panic)")
	}
}

// TestEvalArgsNoArgsReturnsEmptyMap: an action that declares no args
// evaluates to an empty map and no error.
func TestEvalArgsNoArgsReturnsEmptyMap(t *testing.T) {
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
	got, err := store.EvalArgs(store.Playbooks[0].Actions[0], DispatchInput{Event: map[string]any{}})
	if err != nil {
		t.Fatalf("EvalArgs: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("EvalArgs = %#v, want a non-nil empty map", got)
	}
}

// TestEvalArgsSkipsNilProgram: a nil entry in the exported Args map is
// expressible and must be skipped like the when path tolerates a nil program,
// not panic inside expr.Eval.
func TestEvalArgsSkipsNilProgram(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    args:
      message: '"hello"'
`)
	store, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := store.Playbooks[0].Actions[0]
	a.Args["missing"] = nil
	got, err := store.EvalArgs(a, DispatchInput{Event: map[string]any{}})
	if err != nil {
		t.Fatalf("EvalArgs: %v", err)
	}
	want := map[string]any{"message": "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EvalArgs = %#v, want %#v", got, want)
	}
}

// TestEvalArgsNilStoreSafe: a nil store (the pre-load composition phase) is
// an empty args map rather than a panic.
func TestEvalArgsNilStoreSafe(t *testing.T) {
	// A real compiled program on the action proves the early return is the
	// nil-store guard and not the no-args guard: with an empty Action, the
	// len(a.Args) == 0 branch would satisfy this test even without the s == nil
	// check.
	env := expr.NewEnv()
	prg, err := env.Compile(`"hello"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	a := Action{Args: map[string]*expr.Program{"message": prg}}
	var store *Store
	got, err := store.EvalArgs(a, DispatchInput{Event: map[string]any{}})
	if err != nil {
		t.Fatalf("EvalArgs(nil store) = %v, want nil error", err)
	}
	if !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("EvalArgs(nil store) = %#v, want empty non-nil map", got)
	}
}
