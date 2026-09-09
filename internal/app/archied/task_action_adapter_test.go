package archied

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// testTaskActor formats taskactions.Service's result the same way the real
// NATS actor (infrastructure/taskactions.Client) does, so this test exercises
// the shared service through the same chatTaskActorAdapter → LocalChatAdapter
// → ChatTaskActor chain production traffic uses.
type testTaskActor struct{ b *boot }

func (a testTaskActor) ApplyChatTaskAction(
	ctx context.Context, identity *string, taskID int64, action taskstate.Action,
) (gateway.TaskActionResult, error) {
	if err := a.b.taskActions().Apply(ctx, identity, taskID, action); err != nil {
		return gateway.TaskActionResult{}, err
	}
	return gateway.TaskActionResult{
		TaskID:  taskID,
		Action:  string(action),
		Message: fmt.Sprintf("Applied %s to task %d.", action, taskID),
	}, nil
}

func newChatTaskActorForTest(t *testing.T, st store.TaskStore, cfg config.Config) chatTaskActorAdapter {
	t.Helper()
	b := &boot{
		st:         st,
		stateStore: st,
		cfg:        cfg,
		log:        slog.Default(),
	}
	return chatTaskActorAdapter{contract: &gateway.LocalChatAdapter{TaskActor: testTaskActor{b}}}
}

func TestChatTaskActorAdapterCrossIdentityRefused(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	task, err := st.EnqueueChatTask(ctx, "acme", "widget", "parked job", "", "", "identity-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Transition(ctx, task.ID, workflow.StatusQueued, workflow.StatusParked, "needs help"); err != nil {
		t.Fatal(err)
	}

	adapter := newChatTaskActorForTest(t, st, config.Config{})

	tests := []struct {
		name          string
		actorIdentity string
		action        taskstate.Action
	}{
		{
			name:          "different identity cannot abandon",
			actorIdentity: "identity-attacker",
			action:        taskstate.ActionAbandon,
		},
		{
			name:          "different identity cannot retry",
			actorIdentity: "identity-attacker",
			action:        taskstate.ActionRetry,
		},
		{
			name:          "different identity cannot archive",
			actorIdentity: "identity-attacker",
			action:        taskstate.ActionArchive,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := adapter.ApplyChatTaskAction(ctx, &tc.actorIdentity, task.ID, tc.action)
			if err == nil {
				t.Fatalf("ApplyChatTaskAction() allowed cross-identity action %s by %q on task owned by %q",
					tc.action, tc.actorIdentity, task.Identity)
			}
			if !strings.Contains(err.Error(), "identity-owner") || !strings.Contains(err.Error(), tc.actorIdentity) {
				t.Errorf("error = %q, want mention of both identities", err.Error())
			}

			// Confirm store state was NOT mutated.
			current, err := st.TaskByID(ctx, task.ID)
			if err != nil || current == nil {
				t.Fatalf("TaskByID = (%+v, %v)", current, err)
			}
			if current.Status != workflow.StatusParked {
				t.Errorf("task status mutated to %q despite cross-identity refusal", current.Status)
			}
		})
	}
}

func TestChatTaskActorAdapterRefusesDisallowedStateAction(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	adapter := newChatTaskActorForTest(t, st, config.Config{})

	tests := []struct {
		name          string
		initialStatus string
		attemptAction taskstate.Action
	}{
		{
			name:          "cannot abandon running task",
			initialStatus: workflow.StatusRunning,
			attemptAction: taskstate.ActionAbandon,
		},
		{
			name:          "cannot archive parked task",
			initialStatus: workflow.StatusParked,
			attemptAction: taskstate.ActionArchive,
		},
		{
			name:          "cannot retry queued task",
			initialStatus: workflow.StatusQueued,
			attemptAction: taskstate.ActionRetry,
		},
		{
			name:          "cannot approve running task",
			initialStatus: workflow.StatusRunning,
			attemptAction: taskstate.ActionApprove,
		},
		{
			name:          "cannot reject merged task",
			initialStatus: workflow.StatusMerged,
			attemptAction: taskstate.ActionReject,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task, err := st.EnqueueChatTask(ctx, "acme", "widget", "test task", "", "", "archie")
			if err != nil {
				t.Fatal(err)
			}
			if tc.initialStatus != workflow.StatusQueued {
				if err := st.Transition(ctx, task.ID, workflow.StatusQueued, tc.initialStatus, "setup"); err != nil {
					t.Fatal(err)
				}
			}

			_, err = adapter.ApplyChatTaskAction(ctx, new("archie"), task.ID, tc.attemptAction)
			if err == nil {
				t.Fatalf("expected error attempting %s on %s task, got nil", tc.attemptAction, tc.initialStatus)
			}

			// Assert error matches taskstate rules.
			wantErr := taskstate.CheckAction(tc.initialStatus, tc.attemptAction)
			if wantErr != nil && !strings.Contains(err.Error(), wantErr.Error()) {
				t.Errorf("error = %q, want substring %q", err.Error(), wantErr.Error())
			}

			// Ensure task status did not change.
			current, err := st.TaskByID(ctx, task.ID)
			if err != nil || current == nil {
				t.Fatalf("TaskByID = (%+v, %v)", current, err)
			}
			if current.Status != tc.initialStatus {
				t.Errorf("task status changed to %q, want %q", current.Status, tc.initialStatus)
			}
		})
	}
}

func TestChatTaskActorAdapterTaskNotFound(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	adapter := newChatTaskActorForTest(t, st, config.Config{})

	_, err = adapter.ApplyChatTaskAction(ctx, new("archie"), 999999, taskstate.ActionAbandon)
	if err == nil {
		t.Fatal("expected error for non-existent task, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestChatTaskActorAdapterAppliesActionsToStore(t *testing.T) {
	tests := []struct {
		name          string
		initialStatus string
		action        taskstate.Action
		wantStatus    string
		wantArchived  bool
	}{
		{
			name:          "abandon parked task closes it won't do",
			initialStatus: workflow.StatusParked,
			action:        taskstate.ActionAbandon,
			wantStatus:    workflow.StatusClosedWontDo,
		},
		{
			name:          "retry parked task requeues it",
			initialStatus: workflow.StatusParked,
			action:        taskstate.ActionRetry,
			wantStatus:    workflow.StatusQueued,
		},
		{
			name:          "cancel queued task closes it",
			initialStatus: workflow.StatusQueued,
			action:        taskstate.ActionCancel,
			wantStatus:    workflow.StatusClosedWontDo,
		},
		{
			name:          "approve waiting_human task queues it",
			initialStatus: workflow.StatusWaitingHuman,
			action:        taskstate.ActionApprove,
			wantStatus:    workflow.StatusQueued,
		},
		{
			name:          "reject waiting_human task closes it",
			initialStatus: workflow.StatusWaitingHuman,
			action:        taskstate.ActionReject,
			wantStatus:    workflow.StatusClosedWontDo,
		},
		{
			name:          "archive dead task removes it from store",
			initialStatus: workflow.StatusDead,
			action:        taskstate.ActionArchive,
			wantArchived:  true,
		},
		{
			name:          "archive closed_wont_do task removes it from store",
			initialStatus: workflow.StatusClosedWontDo,
			action:        taskstate.ActionArchive,
			wantArchived:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })

			adapter := newChatTaskActorForTest(t, st, config.Config{MaxRetries: 3})

			task, err := st.EnqueueChatTask(ctx, "acme", "widget", "actionable task", "", "", "archie")
			if err != nil {
				t.Fatal(err)
			}
			if tc.initialStatus != workflow.StatusQueued {
				if err := st.Transition(ctx, task.ID, workflow.StatusQueued, tc.initialStatus, "setup"); err != nil {
					t.Fatal(err)
				}
			}

			result, err := adapter.ApplyChatTaskAction(ctx, new("archie"), task.ID, tc.action)
			if err != nil {
				t.Fatalf("ApplyChatTaskAction(%s) error = %v", tc.action, err)
			}
			if result.TaskID != task.ID {
				t.Errorf("result.TaskID = %d, want %d", result.TaskID, task.ID)
			}
			if result.Action != string(tc.action) {
				t.Errorf("result.Action = %q, want %q", result.Action, tc.action)
			}

			current, err := st.TaskByID(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantArchived {
				if current != nil {
					t.Errorf("task %d still exists in store after archive: %+v", task.ID, current)
				}
			} else {
				if current == nil {
					t.Fatalf("task %d not found in store", task.ID)
				}
				if current.Status != tc.wantStatus {
					t.Errorf("task status = %q, want %q", current.Status, tc.wantStatus)
				}
			}
		})
	}
}

func TestChatTaskActorAdapterNilContract(t *testing.T) {
	ctx := context.Background()
	adapter := chatTaskActorAdapter{contract: nil}

	_, err := adapter.ApplyChatTaskAction(ctx, new("archie"), 1, taskstate.ActionAbandon)
	if err == nil {
		t.Fatal("expected error for nil contract, got nil")
	}
}
