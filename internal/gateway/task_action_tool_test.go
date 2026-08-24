package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/tools"
)

type fakeActor struct {
	result TaskActionResult
	err    error

	gotIdentity string
	gotTaskID   int64
	gotAction   taskstate.Action
}

func (f *fakeActor) ApplyChatTaskAction(_ context.Context, identity string, taskID int64, action taskstate.Action) (TaskActionResult, error) {
	f.gotIdentity = identity
	f.gotTaskID = taskID
	f.gotAction = action
	return f.result, f.err
}

func TestTaskActionToolProperties(t *testing.T) {
	actor := &fakeActor{}
	entry := toolNamed(t, TaskTools(nil, nil, nil, actor, "archie"), "task_action")

	if err := entry.Validate(); err != nil {
		t.Fatalf("entry.Validate() = %v", err)
	}
	if entry.Name != "task_action" {
		t.Errorf("Name = %q, want task_action", entry.Name)
	}
	if entry.Toolset != "tasks" {
		t.Errorf("Toolset = %q, want tasks", entry.Toolset)
	}
	wantClass := tools.ClassMutating | tools.RequiresApproval
	if entry.Classification != wantClass {
		t.Errorf("Classification = %v, want %v", entry.Classification, wantClass)
	}
	if entry.BuildApprovalDescription == nil {
		t.Fatal("BuildApprovalDescription is nil, want a description builder for confirmation")
	}
}

func TestTaskActionOmittedWhenActorIsNil(t *testing.T) {
	entries := TaskTools(&fakeLister{}, &fakeCreator{}, &fakeLogReader{}, nil, "archie")
	for _, e := range entries {
		if e.Name == "task_action" {
			t.Fatal("task_action is present when actor is nil -- a tool that cannot run must not be advertised")
		}
	}
}

func TestTaskActionAppearsWhenActorIsNonNil(t *testing.T) {
	entries := TaskTools(nil, nil, nil, &fakeActor{}, "archie")
	toolNamed(t, entries, "task_action") // fails if absent
}

func TestTaskActionUsesBoundIdentity(t *testing.T) {
	actor := &fakeActor{result: TaskActionResult{TaskID: 42, Action: "abandon", Message: "abandoned"}}
	entry := toolNamed(t, TaskTools(nil, nil, nil, actor, "archie"), "task_action")

	_, err := entry.Handler(context.Background(), map[string]any{
		"task_id":  float64(42),
		"action":   "abandon",
		"identity": "other-identity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if actor.gotIdentity != "archie" {
		t.Errorf("identity = %q, want bound %q", actor.gotIdentity, "archie")
	}
}

func TestTaskActionInputValidation(t *testing.T) {
	tests := []struct {
		name       string
		input      map[string]any
		actor      *fakeActor
		wantErr    bool
		wantTaskID int64
		wantAction taskstate.Action
	}{
		{
			name:       "valid float64 task_id",
			input:      map[string]any{"task_id": float64(10), "action": "abandon"},
			actor:      &fakeActor{result: TaskActionResult{TaskID: 10, Action: "abandon"}},
			wantTaskID: 10,
			wantAction: taskstate.ActionAbandon,
		},
		{
			name:       "valid int task_id",
			input:      map[string]any{"task_id": 11, "action": "archive"},
			actor:      &fakeActor{result: TaskActionResult{TaskID: 11, Action: "archive"}},
			wantTaskID: 11,
			wantAction: taskstate.ActionArchive,
		},
		{
			name:       "valid int64 task_id",
			input:      map[string]any{"task_id": int64(12), "action": "retry"},
			actor:      &fakeActor{result: TaskActionResult{TaskID: 12, Action: "retry"}},
			wantTaskID: 12,
			wantAction: taskstate.ActionRetry,
		},
		{
			name:       "normalizes action casing and whitespace",
			input:      map[string]any{"task_id": float64(13), "action": "  ABANDON  "},
			actor:      &fakeActor{result: TaskActionResult{TaskID: 13, Action: "abandon"}},
			wantTaskID: 13,
			wantAction: taskstate.ActionAbandon,
		},
		{
			name:    "missing task_id",
			input:   map[string]any{"action": "abandon"},
			actor:   &fakeActor{},
			wantErr: true,
		},
		{
			name:    "invalid task_id type",
			input:   map[string]any{"task_id": "not-a-number", "action": "abandon"},
			actor:   &fakeActor{},
			wantErr: true,
		},
		{
			name:    "missing action",
			input:   map[string]any{"task_id": float64(10)},
			actor:   &fakeActor{},
			wantErr: true,
		},
		{
			name:    "blank action",
			input:   map[string]any{"task_id": float64(10), "action": "   "},
			actor:   &fakeActor{},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry := toolNamed(t, TaskTools(nil, nil, nil, tc.actor, "archie"), "task_action")
			out, err := entry.Handler(context.Background(), tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Handler(%v) = nil error, want error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Handler(%v) = %v, want nil error", tc.input, err)
			}
			if tc.actor.gotTaskID != tc.wantTaskID {
				t.Errorf("task_id = %d, want %d", tc.actor.gotTaskID, tc.wantTaskID)
			}
			if tc.actor.gotAction != tc.wantAction {
				t.Errorf("action = %q, want %q", tc.actor.gotAction, tc.wantAction)
			}
			res, ok := out.(TaskActionResult)
			if !ok {
				t.Fatalf("Handler output = %T, want TaskActionResult", out)
			}
			if res.TaskID != tc.wantTaskID {
				t.Errorf("res.TaskID = %d, want %d", res.TaskID, tc.wantTaskID)
			}
		})
	}
}

func TestTaskActionAllActions(t *testing.T) {
	actions := []struct {
		action taskstate.Action
		input  string
	}{
		{action: taskstate.ActionAbandon, input: "abandon"},
		{action: taskstate.ActionArchive, input: "archive"},
		{action: taskstate.ActionRetry, input: "retry"},
		{action: taskstate.ActionStop, input: "stop"},
		{action: taskstate.ActionCancel, input: "cancel"},
		{action: taskstate.ActionApprove, input: "approve"},
		{action: taskstate.ActionReject, input: "reject"},
	}

	for _, tc := range actions {
		t.Run(string(tc.action), func(t *testing.T) {
			actor := &fakeActor{result: TaskActionResult{
				TaskID:  99,
				Action:  string(tc.action),
				Message: fmt.Sprintf("Applied %s to task 99.", tc.action),
			}}
			entry := toolNamed(t, TaskTools(nil, nil, nil, actor, "archie"), "task_action")
			out, err := entry.Handler(context.Background(), map[string]any{
				"task_id": float64(99),
				"action":  tc.input,
			})
			if err != nil {
				t.Fatalf("Handler(%s) error = %v", tc.input, err)
			}
			res, ok := out.(TaskActionResult)
			if !ok {
				t.Fatalf("Handler output = %T, want TaskActionResult", out)
			}
			if res.Action != string(tc.action) {
				t.Errorf("res.Action = %q, want %q", res.Action, tc.action)
			}
		})
	}
}

func TestTaskActionPropagatesActorError(t *testing.T) {
	actor := &fakeActor{err: errors.New("action not available while task is running")}
	entry := toolNamed(t, TaskTools(nil, nil, nil, actor, "archie"), "task_action")

	_, err := entry.Handler(context.Background(), map[string]any{
		"task_id": float64(42),
		"action":  "abandon",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "action not available while task is running") {
		t.Errorf("error = %v, want substring %q", err, "action not available while task is running")
	}
}

func TestTaskActionBuildApprovalDescription(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{
			name:  "abandon parked task",
			input: map[string]any{"task_id": float64(42), "action": "abandon"},
			want:  "Apply action \"abandon\" to task 42.",
		},
		{
			name:  "archive dead task",
			input: map[string]any{"task_id": int64(101), "action": "archive"},
			want:  "Apply action \"archive\" to task 101.",
		},
		{
			name:  "missing fields fallback cleanly",
			input: map[string]any{},
			want:  "Apply action \"\" to task 0.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actor := &fakeActor{}
			entry := toolNamed(t, TaskTools(nil, nil, nil, actor, "archie"), "task_action")
			got := entry.BuildApprovalDescription(tc.input)
			if got != tc.want {
				t.Errorf("BuildApprovalDescription(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
