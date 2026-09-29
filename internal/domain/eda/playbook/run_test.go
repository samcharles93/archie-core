package playbook

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eda/module"
)

// recordingModules is the real kind-schema source with a fake invoker: it
// records each log message and fails any message listed in fail.
type recordingModules struct {
	*module.ModuleRegistry
	messages []string
	fail     map[string]bool
}

func (m *recordingModules) Invoke(_ context.Context, kind string, args map[string]any) (map[string]any, error) {
	msg, _ := args["message"].(string)
	m.messages = append(m.messages, msg)
	if m.fail[msg] {
		return nil, errors.New("invoke failed")
	}
	return map[string]any{"written": true, "level": "info"}, nil
}

func loadRunStore(t *testing.T, mods *recordingModules, document string) *Store {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "pb.yaml", document)
	store, err := Load(dir, mods)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return store
}

const twoLogActions = `
trigger:
  kind: bug
actions:
  - id: first
    position: module
    kind: log
    args: { message: '"first"' }
  - id: second
    position: module
    kind: log
    args: { message: '"second saw " + string(actions.first.result.written)' }
`

func bugInput() DispatchInput {
	return DispatchInput{Labels: []string{"bug"}, Kind: "bug", TaskID: "acme/widget#7", Event: map[string]any{}}
}

func TestRunExecutesActionsInOrderAndFeedsResults(t *testing.T) {
	mods := &recordingModules{ModuleRegistry: module.New()}
	store := loadRunStore(t, mods, twoLogActions)

	if err := store.Run(t.Context(), &fakePlaybookLedger{}, slog.New(slog.DiscardHandler), bugInput()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"first", "second saw true"}; !slices.Equal(mods.messages, want) {
		t.Fatalf("invoked messages = %q, want %q", mods.messages, want)
	}
}

func TestRunInvokesEachActionOncePerEvent(t *testing.T) {
	mods := &recordingModules{ModuleRegistry: module.New()}
	store := loadRunStore(t, mods, twoLogActions)
	ledger := &fakePlaybookLedger{}
	log := slog.New(slog.DiscardHandler)

	if err := store.Run(t.Context(), ledger, log, bugInput()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The repeat skips "first" as a duplicate, so it has no result and
	// "second" cannot evaluate its args: the playbook stops there
	// (action-playbook-run.md). Neither action is invoked again.
	_ = store.Run(t.Context(), ledger, log, bugInput())
	if len(mods.messages) != 2 {
		t.Fatalf("invoked messages = %q, want each action once", mods.messages)
	}
}

func TestRunSkipsAnActionWhoseWhenIsFalse(t *testing.T) {
	mods := &recordingModules{ModuleRegistry: module.New()}
	store := loadRunStore(t, mods, `
trigger:
  kind: bug
actions:
  - position: module
    kind: log
    when: 'event.kind == "feature"'
    args: { message: '"skipped"' }
  - position: module
    kind: log
    args: { message: '"ran"' }
`)
	if err := store.Run(t.Context(), &fakePlaybookLedger{}, slog.New(slog.DiscardHandler), DispatchInput{
		Labels: []string{"bug"}, Kind: "bug", TaskID: "acme/widget#7", Event: map[string]any{"kind": "bug"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"ran"}; !slices.Equal(mods.messages, want) {
		t.Fatalf("invoked messages = %q, want %q", mods.messages, want)
	}
}

func TestRunStopsThePlaybookOnAnInvokeError(t *testing.T) {
	mods := &recordingModules{ModuleRegistry: module.New(), fail: map[string]bool{"first": true}}
	store := loadRunStore(t, mods, twoLogActions)

	err := store.Run(t.Context(), &fakePlaybookLedger{}, slog.New(slog.DiscardHandler), bugInput())
	if err == nil || !strings.Contains(err.Error(), "pb.yaml") || !strings.Contains(err.Error(), "first") {
		t.Fatalf("Run error = %v, want one naming the playbook and the action", err)
	}
	if want := []string{"first"}; !slices.Equal(mods.messages, want) {
		t.Fatalf("invoked messages = %q, want the run stopped at %q", mods.messages, want)
	}
}

// TestRunAbortsThePlaybookOnAnArgsEvaluationFailure pins J6
// (docs/prds/playbook-expression-syntax.md): an args value that cannot be
// evaluated stops its playbook before the action runs, unlike a `when` that
// errors, which skips the action and continues (J3). The error names the
// playbook and the action whose args failed.
func TestRunAbortsThePlaybookOnAnArgsEvaluationFailure(t *testing.T) {
	mods := &recordingModules{ModuleRegistry: module.New()}
	store := loadRunStore(t, mods, `
trigger:
  kind: bug
actions:
  - id: first
    position: module
    kind: log
    args: { message: 'event.absent' }
  - id: second
    position: module
    kind: log
    args: { message: '"second"' }
`)

	err := store.Run(t.Context(), &fakePlaybookLedger{}, slog.New(slog.DiscardHandler), bugInput())
	if err == nil {
		t.Fatal("Run(args evaluation failure) = nil, want the playbook aborted")
	}
	for _, want := range []string{"pb.yaml", "first", "evaluate args"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run error = %q, want it to contain %q", err.Error(), want)
		}
	}
	if len(mods.messages) != 0 {
		t.Fatalf("invoked messages = %q, want none: the playbook aborts before the first action runs", mods.messages)
	}
}

func TestRunIgnoresNonMatchingAndWorkflowPlaybooks(t *testing.T) {
	mods := &recordingModules{ModuleRegistry: module.New()}
	store := loadRunStore(t, mods, twoLogActions)
	input := bugInput()
	input.Labels, input.Kind = []string{"feature"}, "feature"

	if err := store.Run(t.Context(), &fakePlaybookLedger{}, slog.New(slog.DiscardHandler), input); err != nil {
		t.Fatalf("Run: %v", err)
	}
	workflowStore := loadRunStore(t, mods, "trigger:\n  kind: bug\nactions:\n  - position: workflow\n    workflow: tdd\n")
	if err := workflowStore.Run(t.Context(), &fakePlaybookLedger{}, slog.New(slog.DiscardHandler), bugInput()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(mods.messages) != 0 {
		t.Fatalf("invoked messages = %q, want none", mods.messages)
	}
}
