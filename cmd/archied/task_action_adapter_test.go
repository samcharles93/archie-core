package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/webui"
)

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
	if err := st.Transition(ctx, task.ID, store.StatusQueued, store.StatusParked, "needs help"); err != nil {
		t.Fatal(err)
	}

	srv := &webui.Server{Store: st, Cfg: config.NewHolder(config.Config{})}
	adapter := chatTaskActorAdapter{
		tasks:   st.TaskByID,
		handler: srv.Handler(),
		token:   func() string { return srv.Token },
	}

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
			_, err := adapter.ApplyChatTaskAction(ctx, tc.actorIdentity, task.ID, tc.action)
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
			if current.Status != store.StatusParked {
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

	srv := &webui.Server{Store: st, Cfg: config.NewHolder(config.Config{})}
	adapter := chatTaskActorAdapter{
		tasks:   st.TaskByID,
		handler: srv.Handler(),
		token:   func() string { return srv.Token },
	}

	tests := []struct {
		name          string
		initialStatus string
		attemptAction taskstate.Action
	}{
		{
			name:          "cannot abandon running task",
			initialStatus: store.StatusRunning,
			attemptAction: taskstate.ActionAbandon,
		},
		{
			name:          "cannot archive parked task",
			initialStatus: store.StatusParked,
			attemptAction: taskstate.ActionArchive,
		},
		{
			name:          "cannot retry queued task",
			initialStatus: store.StatusQueued,
			attemptAction: taskstate.ActionRetry,
		},
		{
			name:          "cannot approve running task",
			initialStatus: store.StatusRunning,
			attemptAction: taskstate.ActionApprove,
		},
		{
			name:          "cannot reject merged task",
			initialStatus: store.StatusMerged,
			attemptAction: taskstate.ActionReject,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task, err := st.EnqueueChatTask(ctx, "acme", "widget", "test task", "", "", "archie")
			if err != nil {
				t.Fatal(err)
			}
			if tc.initialStatus != store.StatusQueued {
				if err := st.Transition(ctx, task.ID, store.StatusQueued, tc.initialStatus, "setup"); err != nil {
					t.Fatal(err)
				}
			}

			_, err = adapter.ApplyChatTaskAction(ctx, "archie", task.ID, tc.attemptAction)
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

	srv := &webui.Server{Store: st, Cfg: config.NewHolder(config.Config{})}
	adapter := chatTaskActorAdapter{
		tasks:   st.TaskByID,
		handler: srv.Handler(),
		token:   func() string { return srv.Token },
	}

	_, err = adapter.ApplyChatTaskAction(ctx, "archie", 999999, taskstate.ActionAbandon)
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
			initialStatus: store.StatusParked,
			action:        taskstate.ActionAbandon,
			wantStatus:    store.StatusClosedWontDo,
		},
		{
			name:          "retry parked task requeues it",
			initialStatus: store.StatusParked,
			action:        taskstate.ActionRetry,
			wantStatus:    store.StatusQueued,
		},
		{
			name:          "cancel queued task closes it",
			initialStatus: store.StatusQueued,
			action:        taskstate.ActionCancel,
			wantStatus:    store.StatusClosedWontDo,
		},
		{
			name:          "approve waiting_human task queues it",
			initialStatus: store.StatusWaitingHuman,
			action:        taskstate.ActionApprove,
			wantStatus:    store.StatusQueued,
		},
		{
			name:          "reject waiting_human task closes it",
			initialStatus: store.StatusWaitingHuman,
			action:        taskstate.ActionReject,
			wantStatus:    store.StatusClosedWontDo,
		},
		{
			name:          "archive dead task removes it from store",
			initialStatus: store.StatusDead,
			action:        taskstate.ActionArchive,
			wantArchived:  true,
		},
		{
			name:          "archive closed_wont_do task removes it from store",
			initialStatus: store.StatusClosedWontDo,
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

			srv := &webui.Server{Store: st, Cfg: config.NewHolder(config.Config{MaxRetries: 3})}
			adapter := chatTaskActorAdapter{
				tasks:   st.TaskByID,
				handler: srv.Handler(),
				token:   func() string { return srv.Token },
			}

			task, err := st.EnqueueChatTask(ctx, "acme", "widget", "actionable task", "", "", "archie")
			if err != nil {
				t.Fatal(err)
			}
			if tc.initialStatus != store.StatusQueued {
				if err := st.Transition(ctx, task.ID, store.StatusQueued, tc.initialStatus, "setup"); err != nil {
					t.Fatal(err)
				}
			}

			result, err := adapter.ApplyChatTaskAction(ctx, "archie", task.ID, tc.action)
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

func TestChatTaskActorAdapterWithTokenAuth(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := &webui.Server{
		Store: st,
		Cfg:   config.NewHolder(config.Config{MaxRetries: 3}),
		Token: "secret-token-12345",
	}
	adapter := chatTaskActorAdapter{
		tasks:   st.TaskByID,
		handler: srv.Handler(),
		token:   func() string { return srv.Token },
	}

	task, err := st.EnqueueChatTask(ctx, "acme", "widget", "auth task", "", "", "archie")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Transition(ctx, task.ID, store.StatusQueued, store.StatusParked, "park"); err != nil {
		t.Fatal(err)
	}

	result, err := adapter.ApplyChatTaskAction(ctx, "archie", task.ID, taskstate.ActionAbandon)
	if err != nil {
		t.Fatalf("ApplyChatTaskAction with token auth failed: %v", err)
	}
	if result.TaskID != task.ID {
		t.Errorf("result.TaskID = %d, want %d", result.TaskID, task.ID)
	}

	current, err := st.TaskByID(ctx, task.ID)
	if err != nil || current == nil {
		t.Fatalf("TaskByID = (%+v, %v)", current, err)
	}
	if current.Status != store.StatusClosedWontDo {
		t.Errorf("status = %q, want %q", current.Status, store.StatusClosedWontDo)
	}
}
