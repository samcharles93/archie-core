// Declared workflow outputs on the task row: the attempt-scoped structured
// results a run writes (docs/prds/workflow-call-outputs.md, "Storage and
// wire").
package postgres

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// newStoreForOutputs is a migrated pool with the Store built over it.
func newStoreForOutputs(t *testing.T) *Store {
	t.Helper()
	pool, _ := migrated(t)
	return New(pool)
}

// TestTaskOutputs pins the task row half of the outputs model:
//
//   - Update persists the run's written set, decoded exactly on the way out;
//   - an encoded set past the structured-payload bound is refused, not
//     clipped (clipped JSON does not parse);
//   - a claim starts the next attempt with an empty set, so nothing a
//     retried attempt does not rewrite survives.
func TestTaskOutputs(t *testing.T) {
	t.Run("Update persists the run's outputs and TaskByID reads them back", func(t *testing.T) {
		st := newStoreForOutputs(t)
		ctx := t.Context()
		task, err := st.EnqueueChatTask(ctx, "acme", "widget", "callee", "body", "w", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ClaimNext(ctx); err != nil {
			t.Fatal(err)
		}
		row, err := st.TaskByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		row.Outputs = map[string]any{"contained": true, "n": "3"}
		if err := st.Update(ctx, row); err != nil {
			t.Fatalf("Update: %v", err)
		}
		back, err := st.TaskByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if back.Outputs["contained"] != true || back.Outputs["n"] != "3" {
			t.Fatalf("row outputs = %+v, want the written set read back", back.Outputs)
		}
	})

	t.Run("an encoded set past the structured-payload bound is refused", func(t *testing.T) {
		st := newStoreForOutputs(t)
		ctx := t.Context()
		task, err := st.EnqueueChatTask(ctx, "acme", "widget", "callee", "body", "w", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ClaimNext(ctx); err != nil {
			t.Fatal(err)
		}
		row, err := st.TaskByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		row.Outputs = map[string]any{"summary": strings.Repeat("x", 5000)}
		err = st.Update(ctx, row)
		if err == nil {
			t.Fatal("Update accepted a set past the structured-payload bound; it must be refused, not clipped")
		}
		if !strings.Contains(err.Error(), "outputs") {
			t.Fatalf("refusal = %v, want it to name the outputs", err)
		}
	})

	t.Run("a retried attempt starts with an empty set", func(t *testing.T) {
		st := newStoreForOutputs(t)
		ctx := t.Context()
		task, err := st.EnqueueChatTask(ctx, "acme", "widget", "callee", "body", "w", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		first, err := st.ClaimNext(ctx)
		if err != nil || first == nil {
			t.Fatalf("first claim: (%+v, %v)", first, err)
		}
		if first.Outputs != nil {
			t.Fatalf("first claim outputs = %+v, want the empty set the fresh attempt starts with", first.Outputs)
		}
		row, err := st.TaskByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		row.Outputs = map[string]any{"contained": true}
		if err := st.Update(ctx, row); err != nil {
			t.Fatalf("Update: %v", err)
		}
		back, err := st.TaskByID(ctx, task.ID)
		if err != nil || back.Outputs["contained"] != true {
			t.Fatalf("setup: the attempt's outputs did not reach the row: (%+v, %v)", back.Outputs, err)
		}
		if err := st.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusParked, "needs a look"); err != nil {
			t.Fatal(err)
		}
		if err := st.RetryTask(ctx, task.ID, workflow.StatusParked, ""); err != nil {
			t.Fatal(err)
		}
		second, err := st.ClaimNext(ctx)
		if err != nil || second == nil {
			t.Fatalf("retried claim: (%+v, %v)", second, err)
		}
		if task.ID != second.ID {
			t.Skipf("another queued task claimed first; the idempotent queue handed out %d", second.ID)
		}
		if second.Outputs != nil {
			t.Fatalf("retried attempt outputs = %+v, want the empty set", second.Outputs)
		}
		final, err := st.TaskByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, still := final.Outputs["contained"]; still {
			t.Fatalf("row outputs = %+v, want the emptiness the new attempt starts with", final.Outputs)
		}
	})
}
