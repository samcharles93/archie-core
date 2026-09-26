package playbook

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
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

// testSchemas is the real module kind-schema source the loader consults in
// tests: *module.ModuleRegistry satisfies playbook.KindSchemas. It consults
// the built-in kind registry, so no module file needs to be installed for the
// schema lookup to work.
func testSchemas(t *testing.T) Modules {
	t.Helper()
	return module.New()
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
	store, err := Load(dir, testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(2 actions) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "exactly one workflow action") {
		t.Errorf("Load error = %q, want the one-workflow-action rule named", err.Error())
	}
}

// TestLoadModuleActionIsValidActionPlaybook: one module action is now a valid
// action playbook shape (one or more module actions in order).
func TestLoadModuleActionIsValidActionPlaybook(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build started"'
`)
	store, err := Load(dir, testSchemas(t))
	if err != nil {
		t.Fatalf("Load(module action) = %v, want a valid action playbook", err)
	}
	if len(store.Playbooks) != 1 {
		t.Fatalf("loaded %d playbooks, want 1", len(store.Playbooks))
	}
	a := store.Playbooks[0].Actions[0]
	if a.Position != "module" || a.Kind != "log" {
		t.Errorf("action = (%q, %q), want (module, log)", a.Position, a.Kind)
	}
	if a.ID != "build" {
		t.Errorf("action id = %q, want build", a.ID)
	}
	if len(a.Args) != 1 || a.Args["message"] == nil {
		t.Errorf("Action.Args = %#v, want the message program compiled", a.Args)
	}
	// Action playbooks are loaded and validated but not routed (D1).
	if decision, ok := store.Dispatch(DispatchInput{Kind: "bug", Event: map[string]any{}}); ok {
		t.Fatalf("Dispatch(action playbook) = %v, want no match (not routed)", decision)
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
	_, err := Load(dir, testSchemas(t))
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
	_, err := Load(dir, testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(dir with one bad playbook) = nil, want whole-load failure")
	}
}

// TestLoadMissingDirIsEmptyStore: a nonexistent directory is an empty store
// (no playbooks), matching the flat binding loaders' convention.
func TestLoadMissingDirIsEmptyStore(t *testing.T) {
	store, err := Load(filepath.Join(t.TempDir(), "does-not-exist"), testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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
	storeA, err := Load(dir, testSchemas(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if storeA.Playbooks[0].Version == "" {
		t.Fatal("Version = empty, want a content hash")
	}

	// Re-load unchanged content: identical version.
	storeB, err := Load(dir, testSchemas(t))
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
	storeC, err := Load(dir, testSchemas(t))
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
// the TaskEnvelope.IdempotencyKey() string
// ("archie:org/identity/owner/repo/number", empty org and identity resolving
// to the default org) --
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
	store, err := Load(dir, testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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
			_, err := Load(dir, testSchemas(t))
			if err == nil {
				t.Fatalf("Load(id %q) = nil, want load failure", id)
			}
		})
	}
}

// TestValidateActionIDs: the shape/uniqueness helper is the load-boundary's
// id gate for both playbook shapes.
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
// `in` test, or a size read fails with cel-go's overload error on the actions
// type. wantErr pins that stage, so a case cannot pass on a YAML parse error
// or an undeclared root instead.
func TestLoadWhenActionsReferenceFails(t *testing.T) {
	tests := []struct {
		name    string
		when    string
		wantErr string
	}{
		{name: "undeclared field selection", when: `actions.build.result.x == true`, wantErr: "undefined field 'build'"},
		{name: "dynamic index on actions", when: `actions["a"].result.x == true`, wantErr: "'_[_]' applied to '(actions, string)'"},
		{name: "non-literal index key", when: `actions[event.kind].result.x == true`, wantErr: "'_[_]' applied to '(actions, dyn)'"},
		{name: "in operator on actions", when: `"notify" in actions`, wantErr: "'@in' applied to '(string, actions)'"},
		{name: "size of actions", when: `size(actions) > 0`, wantErr: "'size' applied to '(actions)'"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - position: workflow\n    workflow: tdd\n    when: '"+tc.when+"'\n")
			_, err := Load(dir, testSchemas(t))
			if err == nil {
				t.Fatal("Load = nil, want load failure")
			}
			if !strings.Contains(err.Error(), "pb.yaml") {
				t.Errorf("Load error = %q, want the playbook path named", err.Error())
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Load error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
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
	store, err := Load(dir, testSchemas(t))
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
			_, err := Load(dir, testSchemas(t))
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
		_, err := compileArgs("pb.yaml", nil, "", "", raw, env, nil)
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

// TestValidateArgsKeysReportsFirstKeyDeterministically: validateArgsKeys
// ranges a map, so it must sort keys before reporting -- otherwise the
// offending key named depends on map iteration order.
func TestValidateArgsKeysReportsFirstKeyDeterministically(t *testing.T) {
	argsType, _, ok := testSchemas(t).KindSchema("log")
	if !ok {
		t.Fatal("KindSchema(log) = not ok")
	}
	raw := map[string]string{
		"z.bad": "x",
		"a.bad": "x",
	}
	for range 64 {
		err := validateArgsKeys("pb.yaml", nil, "", "log", argsType, raw)
		if err == nil {
			t.Fatal("validateArgsKeys = nil, want error")
		}
		if !strings.Contains(err.Error(), `args["a.bad"]`) {
			t.Fatalf("validateArgsKeys error = %q, want the alphabetically-first key args[%q]", err.Error(), "a.bad")
		}
		if strings.Contains(err.Error(), `args["z.bad"]`) {
			t.Fatalf("validateArgsKeys error = %q, must name the alphabetically-first key, not %q", err.Error(), "z.bad")
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
	store, err := Load(dir, testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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
	store, err := Load(dir, testSchemas(t))
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

// TestLoadActionPlaybookReadsPriorResult: a later module action reads an
// earlier action's declared result by its kind-typed Result field, so the
// per-action environment (D3) must declare prior ids. This is the first new
// test that fails if the loader compiled every action against one env instead
// of an env built from the prior actions' ids.
func TestLoadActionPlaybookReadsPriorResult(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build started"'
  - id: done
    position: module
    kind: log
    args:
      message: '"done"'
    when: actions.build.result.written == true
`)
	store, err := Load(dir, testSchemas(t))
	if err != nil {
		t.Fatalf("Load(action playbook reading a prior result): %v", err)
	}
	pb := store.Playbooks[0]
	if len(pb.Actions) != 2 {
		t.Fatalf("loaded %d actions, want 2", len(pb.Actions))
	}
	if pb.Actions[1].When == nil {
		t.Fatal("second action when = nil, want the compiled prior-result read")
	}
	if pb.Actions[1].Args["message"] == nil {
		t.Fatal("second action args[message] = nil, want a compiled program")
	}
}

// TestLoadActionPlaybookArgsValueReadsPriorResult: the approved doc's own
// example reads a prior action's result inside a later action's args VALUE
// (not only inside `when`). That spelling must compile and load through the
// real loader.
func TestLoadActionPlaybookArgsValueReadsPriorResult(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build started"'
  - id: done
    position: module
    kind: log
    args:
      message: '"done: " + string(actions.build.result.written)'
`)
	store, err := Load(dir, testSchemas(t))
	if err != nil {
		t.Fatalf("Load(args value reading a prior result): %v", err)
	}
	if got := len(store.Playbooks); got != 1 {
		t.Fatalf("loaded %d playbooks, want 1", got)
	}
	if store.Playbooks[0].Actions[1].Args["message"] == nil {
		t.Fatal("second action args[message] = nil, want the compiled prior-result read")
	}
}

// TestLoadRejectsShapeForeignFields: a shape-foreign field is a reject-at-load
// error naming the playbook and the action index, not a silently dropped key.
func TestLoadRejectsShapeForeignFields(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "module action carrying workflow",
			doc: `
trigger:
  kind: bug
actions:
  - position: module
    kind: log
    workflow: tdd
    args:
      message: '"hello"'
`,
			want: []string{"pb.yaml", "action 1", "workflow"},
		},
		{
			name: "workflow action carrying kind",
			doc: `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
    kind: log
`,
			want: []string{"pb.yaml", "action 1", "kind"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", tc.doc)
			_, err := Load(dir, testSchemas(t))
			if err == nil {
				t.Fatal("Load = nil, want load failure")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestLoadModuleActionErrorsNameActionLocation: module-action load failures
// name the 1-based index and the declared id (when present) of the offending
// action, not just the field label, so an operator can locate the action.
func TestLoadModuleActionErrorsNameActionLocation(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "when compile error",
			doc: `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build"'
  - id: done
    position: module
    kind: log
    args:
      message: '"done"'
    when: event.label ==
`,
			want: []string{"pb.yaml", "action 2", "done", "when condition"},
		},
		{
			name: "args value compile error",
			doc: `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build"'
  - id: done
    position: module
    kind: log
    args:
      message: 'foo.bar == 1'
`,
			want: []string{"pb.yaml", "action 2", "done", `args["message"]`},
		},
		{
			name: "unknown kind",
			doc: `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build"'
  - id: done
    position: module
    kind: notify
`,
			want: []string{"pb.yaml", "action 2", "done", "notify"},
		},
		{
			name: "missing kind",
			doc: `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build"'
  - id: done
    position: module
`,
			want: []string{"pb.yaml", "action 2", "done"},
		},
		{
			name: "unknown arg key",
			doc: `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build"'
  - id: done
    position: module
    kind: log
    args:
      message: '"done"'
      bogus: '"x"'
`,
			want: []string{"pb.yaml", "action 2", "done", "bogus"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", tc.doc)
			_, err := Load(dir, testSchemas(t))
			if err == nil {
				t.Fatal("Load = nil, want load failure")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestLoadActionPlaybookForwardReferenceFails: an action may not read a later
// action's id -- the env only declares PRIOR actions' ids, so the forward read
// is an undefined field at compile.
func TestLoadActionPlaybookForwardReferenceFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: module
    kind: log
    args:
      message: '"first"'
    when: actions.done.result.written == true
  - id: done
    position: module
    kind: log
    args:
      message: '"second"'
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(forward reference) = nil, want load failure")
	}
	for _, want := range []string{"pb.yaml", "done"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error = %q, want it to name %q", err.Error(), want)
		}
	}
}

// TestLoadActionPlaybookRejectsUnknownKind: an action's kind must name a
// registered module kind.
func TestLoadActionPlaybookRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: module
    kind: notify
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(unknown kind) = nil, want load failure")
	}
	for _, want := range []string{"pb.yaml", "notify"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error = %q, want it to name %q", err.Error(), want)
		}
	}
}

// TestLoadActionPlaybookRejectsMissingKind: a module action must name a kind.
func TestLoadActionPlaybookRejectsMissingKind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: module
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(missing kind) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "pb.yaml") {
		t.Errorf("Load error = %q, want the playbook path named", err.Error())
	}
}

// TestLoadActionPlaybookRejectsUnsupportedPosition: a position other than
// workflow or module is a load failure naming the playbook.
func TestLoadActionPlaybookRejectsUnsupportedPosition(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: channel
    kind: log
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(channel position) = nil, want load failure")
	}
	for _, want := range []string{"pb.yaml", "channel"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error = %q, want it to name %q", err.Error(), want)
		}
	}
}

// TestLoadActionPlaybookRejectsMixedPositions: workflow and module actions
// never mix in one playbook (D2).
func TestLoadActionPlaybookRejectsMixedPositions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: tdd
  - position: module
    kind: log
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(mixed positions) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "pb.yaml") {
		t.Errorf("Load error = %q, want the playbook path named", err.Error())
	}
}

// TestLoadActionPlaybookRejectsZeroActions: a playbook must declare at least
// one action.
func TestLoadActionPlaybookRejectsZeroActions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions: []
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(zero actions) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "pb.yaml") {
		t.Errorf("Load error = %q, want the playbook path named", err.Error())
	}
}

// TestLoadActionPlaybookRejectsDuplicateIDs: duplicate ids are reachable once
// action playbooks are loadable, and the shared id gate rejects them.
func TestLoadActionPlaybookRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"first"'
  - id: build
    position: module
    kind: log
    args:
      message: '"second"'
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(duplicate ids) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "duplicate action id") {
		t.Errorf("Load error = %q, want the duplicate-id rule named", err.Error())
	}
}

// TestLoadActionPlaybookRejectsUnknownArgKey: an args key the kind's Args
// struct does not define fails the load (D4).
func TestLoadActionPlaybookRejectsUnknownArgKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: module
    kind: log
    args:
      message: '"hello"'
      bogus: '"x"'
`)
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(unknown arg key) = nil, want load failure")
	}
	for _, want := range []string{"pb.yaml", "bogus"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error = %q, want it to name %q", err.Error(), want)
		}
	}
}

// TestLoadActionPlaybookRejectsNonCELID: a module action id must be a
// lowercase CEL field name writable as `actions.<id>`, so a dotted/dashed id
// (legal for a workflow id), an uppercase id, or a CEL keyword all fail the
// load naming the id.
func TestLoadActionPlaybookRejectsNonCELID(t *testing.T) {
	for _, id := range []string{"build.step", "build-step", "Build", "in", "true", "false", "null"} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - id: '"+id+"'\n    position: module\n    kind: log\n    args:\n      message: '\"hello\"'\n")
			_, err := Load(dir, testSchemas(t))
			if err == nil {
				t.Fatalf("Load(id %q) = nil, want load failure", id)
			}
			for _, want := range []string{"pb.yaml", id, "lowercase CEL field name"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestLoadWorkflowActionIDKeepsStableIdentifierGrammar: the CEL-identifier
// restriction is module-only. A workflow action keeps the looser
// stable-identifier grammar (dots/dashes) unchanged.
func TestLoadWorkflowActionIDKeepsStableIdentifierGrammar(t *testing.T) {
	for _, id := range []string{"build.step", "build-step"} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - position: workflow\n    workflow: tdd\n    id: "+id+"\n")
			if _, err := Load(dir, testSchemas(t)); err != nil {
				t.Fatalf("Load(workflow id %q) = %v, want nil error (workflow id grammar unchanged)", id, err)
			}
		})
	}
}

// TestLoadActionPlaybookRejectsMisspelledResultField: a Result field the
// referenced kind does not define is a load failure naming the playbook and
// the field -- the same unknown-field check, exercised at the Load boundary.
func TestLoadActionPlaybookRejectsMisspelledResultField(t *testing.T) {
	misspelled := "wri" + "ten"
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - id: build\n    position: module\n    kind: log\n    args:\n      message: '\"build started\"'\n  - id: done\n    position: module\n    kind: log\n    args:\n      message: '\"done\"'\n    when: actions.build.result."+misspelled+" == true\n")
	_, err := Load(dir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(misspelled result field) = nil, want load failure")
	}
	for _, want := range []string{"pb.yaml", misspelled, "undefined field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestLoadActionPlaybookArgsKeysMatchDecoder ties the loader's accepted args
// key set to the module decoder's: the loader accepts only lower-case keys,
// the decoder decodes only lower-case keys, and the two meet when the
// evaluated args are passed to Invoke.
func TestLoadActionPlaybookArgsKeysMatchDecoder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"hello"'
      level: '"info"'
`)
	store, err := Load(dir, testSchemas(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := store.EvalArgs(store.Playbooks[0].Actions[0], DispatchInput{Event: map[string]any{}})
	if err != nil {
		t.Fatalf("EvalArgs: %v", err)
	}
	want := map[string]any{"message": "hello", "level": "info"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EvalArgs = %#v, want %#v", got, want)
	}

	moduleDir := t.TempDir()
	writeFile(t, moduleDir, "log.go", `package main

import "github.com/samcharles93/archie-core/internal/domain/eda/module/log"

func Run(a log.Args) log.Result {
	return log.Result{Written: a.Message != "", Level: a.Level}
}
`)
	r := module.New()
	if err := r.Register("log", moduleDir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res, err := r.Invoke(context.Background(), "log", got)
	if err != nil {
		t.Fatalf("Invoke(evaluated args): %v", err)
	}
	if res["written"] != true {
		t.Errorf("written = %v, want true", res["written"])
	}
	if res["level"] != "info" {
		t.Errorf("level = %q, want info", res["level"])
	}

	// The decoder matches literal lower-case keys, so a capitalized key the
	// loader accepted under the old lower-casing rule must now fail the load.
	badDir := t.TempDir()
	writeFile(t, badDir, "pb.yaml", `
trigger:
  kind: bug
actions:
  - position: module
    kind: log
    args:
      Message: '"hello"'
`)
	_, err = Load(badDir, testSchemas(t))
	if err == nil {
		t.Fatal("Load(capitalized arg key) = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "Message") {
		t.Errorf("Load error = %q, want the capitalized key named", err.Error())
	}
}

// TestLoadActionPlaybookRejectsActionReferences is the classifier-enumeration
// proof (E): every `actions` spelling the old classifier rejected must still
// be rejected for a typed action playbook. All but the bare `actions` value
// are rejected by CEL's per-playbook object type at compile; the bare value
// read is the one spelling CEL does not reject, kept behind the retained
// resolvable check in expr.ActionReferences.
func TestLoadActionPlaybookRejectsActionReferences(t *testing.T) {
	// Positive control: the same two-action fixture with a valid `when` must
	// load, so the rejections below fail because of the `when` spelling and
	// not because the action-playbook shape itself is rejected.
	controlDir := t.TempDir()
	writeFile(t, controlDir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - id: build\n    position: module\n    kind: log\n    args:\n      message: '\"build started\"'\n  - id: done\n    position: module\n    kind: log\n    args:\n      message: '\"done\"'\n    when: actions.build.result.written == true\n")
	if _, err := Load(controlDir, testSchemas(t)); err != nil {
		t.Fatalf("Load(valid when control) = %v, want nil error", err)
	}

	tests := []struct {
		name string
		when string
	}{
		{name: "unknown id", when: `actions.missing.result.written == true`},
		{name: "literal map index", when: `actions["build"].result.written == true`},
		{name: "computed index", when: `actions[key].result.written == true`},
		{name: "dynamic index by event", when: `actions[event.name].result.written == true`},
		{name: "in operator", when: `"build" in actions`},
		{name: "size", when: `size(actions) > 0`},
		{name: "method call", when: `actions.all(x, true)`},
		{name: "equality with map", when: `actions == {}`},
		{name: "bare actions", when: `actions`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", "\ntrigger:\n  kind: bug\nactions:\n  - id: build\n    position: module\n    kind: log\n    args:\n      message: '\"build started\"'\n  - id: done\n    position: module\n    kind: log\n    args:\n      message: '\"done\"'\n    when: "+tc.when+"\n")
			_, err := Load(dir, testSchemas(t))
			if err == nil {
				t.Fatalf("Load(%s) = nil, want load failure", tc.when)
			}
			if !strings.Contains(err.Error(), "pb.yaml") {
				t.Errorf("Load error = %q, want the playbook path named", err.Error())
			}
		})
	}
}

// An args value whose static CEL type cannot fill the kind's Args field fails
// the load, naming the key. A dyn value (an event read) is accepted: its type
// is known only per event, and the module decode refuses a bad one at run.
func TestLoadArgsValueTypeAgainstArgsSchema(t *testing.T) {
	tests := []struct {
		name    string
		message string
		wantErr bool
	}{
		{name: "string literal", message: `'"hello"'`},
		{name: "string concatenation", message: `'"n=" + string(1)'`},
		{name: "dyn event read", message: `event.title`},
		{name: "int literal", message: `'123'`, wantErr: true},
		{name: "bool expression", message: `'1 == 1'`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "pb.yaml", "trigger:\n  kind: bug\nactions:\n  - position: module\n    kind: log\n    args:\n      message: "+tt.message+"\n")
			_, err := Load(dir, testSchemas(t))
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), `args["message"]`) || !strings.Contains(err.Error(), "string") {
				t.Fatalf("Load error = %v, want args[\"message\"] refused for not being a string", err)
			}
		})
	}
}
