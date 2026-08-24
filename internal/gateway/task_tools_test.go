package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools"
)

type fakeLister struct {
	tasks []ChatTaskSummary
	err   error

	gotIdentity string
	gotLimit    int
}

func (f *fakeLister) ListChatTasks(_ context.Context, identity string, limit int) ([]ChatTaskSummary, error) {
	f.gotIdentity, f.gotLimit = identity, limit
	return f.tasks, f.err
}

type fakeCreator struct {
	id  int64
	err error

	got SpawnRequest
}

func (f *fakeCreator) CreateTask(_ context.Context, req SpawnRequest) (int64, error) {
	f.got = req
	return f.id, f.err
}

// toolNamed pulls one entry out of the built set.
func toolNamed(t *testing.T, entries []tools.ToolEntry, name string) tools.ToolEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("TaskTools() has no %q entry", name)
	return tools.ToolEntry{}
}

func TestTaskToolsValidate(t *testing.T) {
	entries := TaskTools(&fakeLister{}, &fakeCreator{}, nil, nil, "archie")
	if len(entries) == 0 {
		t.Fatal("TaskTools() returned nothing")
	}
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			t.Errorf("entry %q: %v", e.Name, err)
		}
	}
}

// TestTaskToolsOmitNilBackends checks that a daemon without chat task support
// advertises nothing, rather than offering a tool that always fails.
func TestTaskToolsOmitNilBackends(t *testing.T) {
	tests := []struct {
		name    string
		lister  ChatTaskLister
		creator TaskCreator
		want    []string
	}{
		{name: "both", lister: &fakeLister{}, creator: &fakeCreator{}, want: []string{"task_list", "task_spawn"}},
		{name: "lister only", lister: &fakeLister{}, want: []string{"task_list"}},
		{name: "creator only", creator: &fakeCreator{}, want: []string{"task_spawn"}},
		{name: "neither"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries := TaskTools(tc.lister, tc.creator, nil, nil, "archie")
			var got []string
			for _, e := range entries {
				got = append(got, e.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("tools = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTaskListUsesBoundIdentity is the authorization property: the identity
// comes from the gateway that built the tool, never from the model. A model
// that could name an identity could read another instance's work.
func TestTaskListUsesBoundIdentity(t *testing.T) {
	lister := &fakeLister{tasks: []ChatTaskSummary{{ID: 7, Title: "wire the thing", Status: "running"}}}
	entry := toolNamed(t, TaskTools(lister, nil, nil, nil, "archie"), "task_list")

	// The model supplies an identity; it must be ignored.
	if _, err := entry.Handler(context.Background(), map[string]any{"identity": "someone-else"}); err != nil {
		t.Fatal(err)
	}
	if lister.gotIdentity != "archie" {
		t.Errorf("listed identity %q, want the bound %q", lister.gotIdentity, "archie")
	}
}

func TestTaskListLimit(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  int
	}{
		{name: "default when absent", input: map[string]any{}, want: defaultTaskListLimit},
		{name: "explicit", input: map[string]any{"limit": float64(5)}, want: 5},
		{name: "clamped to the maximum", input: map[string]any{"limit": float64(10_000)}, want: maxTaskListLimit},
		{name: "non-positive falls back to the default", input: map[string]any{"limit": float64(0)}, want: defaultTaskListLimit},
		{name: "negative falls back to the default", input: map[string]any{"limit": float64(-3)}, want: defaultTaskListLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeLister{}
			entry := toolNamed(t, TaskTools(lister, nil, nil, nil, "archie"), "task_list")
			if _, err := entry.Handler(context.Background(), tc.input); err != nil {
				t.Fatal(err)
			}
			if lister.gotLimit != tc.want {
				t.Errorf("limit = %d, want %d", lister.gotLimit, tc.want)
			}
		})
	}
}

func TestTaskListFiltersByStatus(t *testing.T) {
	lister := &fakeLister{tasks: []ChatTaskSummary{
		{ID: 1, Status: "running"},
		{ID: 2, Status: "queued"},
		{ID: 3, Status: "running"},
	}}
	entry := toolNamed(t, TaskTools(lister, nil, nil, nil, "archie"), "task_list")

	out, err := entry.Handler(context.Background(), map[string]any{"status": "running"})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := out.(TaskListResult)
	if !ok {
		t.Fatalf("handler returned %T, want TaskListResult", out)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("got %d tasks, want the 2 running ones", len(result.Tasks))
	}
	for _, task := range result.Tasks {
		if task.Status != "running" {
			t.Errorf("task %d has status %q, want only running", task.ID, task.Status)
		}
	}
}

// TestTaskListEmptyIsNotAnError guards against the model reading an empty
// queue as a broken tool and retrying.
func TestTaskListEmptyIsNotAnError(t *testing.T) {
	entry := toolNamed(t, TaskTools(&fakeLister{}, nil, nil, nil, "archie"), "task_list")

	out, err := entry.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("empty list returned an error: %v", err)
	}
	result, ok := out.(TaskListResult)
	if !ok {
		t.Fatalf("handler returned %T, want TaskListResult", out)
	}
	if result.Tasks == nil {
		t.Error("Tasks is nil, want an empty slice so it marshals as [] not null")
	}
}

func TestTaskListPropagatesStoreError(t *testing.T) {
	lister := &fakeLister{err: errors.New("database is gone")}
	entry := toolNamed(t, TaskTools(lister, nil, nil, nil, "archie"), "task_list")

	if _, err := entry.Handler(context.Background(), map[string]any{}); err == nil {
		t.Fatal("handler error = nil, want the store failure surfaced")
	}
}

func TestTaskSpawn(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]any
		creator   *fakeCreator
		wantErr   bool
		wantTitle string
		wantRepo  string
	}{
		{
			name:      "title only",
			input:     map[string]any{"title": "fix the parser"},
			creator:   &fakeCreator{id: 42},
			wantTitle: "fix the parser",
		},
		{
			name:      "with repo and workflow",
			input:     map[string]any{"title": "port it", "repo": "acme/app", "workflow": "tdd"},
			creator:   &fakeCreator{id: 43},
			wantTitle: "port it",
			wantRepo:  "acme/app",
		},
		{
			name:    "missing title is refused",
			input:   map[string]any{},
			creator: &fakeCreator{id: 44},
			wantErr: true,
		},
		{
			name:    "blank title is refused",
			input:   map[string]any{"title": "   "},
			creator: &fakeCreator{id: 45},
			wantErr: true,
		},
		{
			name:    "creator rejection surfaces",
			input:   map[string]any{"title": "x", "repo": "not/allowed"},
			creator: &fakeCreator{err: fmt.Errorf("repo %q is not configured for this identity", "not/allowed")},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry := toolNamed(t, TaskTools(nil, tc.creator, nil, nil, "archie"), "task_spawn")
			out, err := entry.Handler(context.Background(), tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("handler error = nil, want a refusal")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.creator.got.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", tc.creator.got.Title, tc.wantTitle)
			}
			if tc.creator.got.Repo != tc.wantRepo {
				t.Errorf("repo = %q, want %q", tc.creator.got.Repo, tc.wantRepo)
			}
			// Identity is bound, never taken from the model.
			if tc.creator.got.Identity != "archie" {
				t.Errorf("identity = %q, want the bound %q", tc.creator.got.Identity, "archie")
			}
			result, ok := out.(TaskSpawnResult)
			if !ok {
				t.Fatalf("handler returned %T, want TaskSpawnResult", out)
			}
			if result.ID != tc.creator.id {
				t.Errorf("id = %d, want %d", result.ID, tc.creator.id)
			}
		})
	}
}

// TestTaskSpawnIgnoresModelIdentity is the counterpart to the list case: a
// model must not be able to file work under another instance's identity.
func TestTaskSpawnIgnoresModelIdentity(t *testing.T) {
	creator := &fakeCreator{id: 1}
	entry := toolNamed(t, TaskTools(nil, creator, nil, nil, "archie"), "task_spawn")

	if _, err := entry.Handler(context.Background(), map[string]any{
		"title":    "sneaky",
		"identity": "other-archie",
	}); err != nil {
		t.Fatal(err)
	}
	if creator.got.Identity != "archie" {
		t.Errorf("identity = %q, want the bound %q", creator.got.Identity, "archie")
	}
}

// TestTaskToolsExcludeApprovalTools records a deliberate omission: approving a
// waiting_human task is the one human checkpoint in the lifecycle, and chat is
// an untrusted input path. If these are ever added they must be a considered
// decision, not an accident.
func TestTaskToolsExcludeApprovalTools(t *testing.T) {
	entries := TaskTools(&fakeLister{}, &fakeCreator{}, nil, nil, "archie")
	for _, e := range entries {
		switch e.Name {
		case "task_approve", "task_cancel":
			t.Errorf("%q is exposed to the model; approval must stay a human action", e.Name)
		}
	}
}

// ── task_logs tests ──────────────────────────────────────────────────────

type fakeLogReader struct {
	result ChatTaskLogResult
	err    error

	gotIdentity string
	gotTaskID   int64
	gotAttempt  int
	gotQuery    ChatTaskLogQuery
}

func (f *fakeLogReader) ReadChatTaskLogs(_ context.Context, identity string, taskID int64, attempt int, q ChatTaskLogQuery) (ChatTaskLogResult, error) {
	f.gotIdentity = identity
	f.gotTaskID = taskID
	f.gotAttempt = attempt
	f.gotQuery = q
	return f.result, f.err
}

func TestTaskLogsUsesBoundIdentity(t *testing.T) {
	reader := &fakeLogReader{result: ChatTaskLogResult{Attempt: 1}}
	entry := toolNamed(t, TaskTools(nil, nil, reader, nil, "archie"), "task_logs")

	if _, err := entry.Handler(context.Background(), map[string]any{
		"task_id": float64(7), "identity": "someone-else",
	}); err != nil {
		t.Fatal(err)
	}
	if reader.gotIdentity != "archie" {
		t.Errorf("identity = %q, want the bound %q", reader.gotIdentity, "archie")
	}
}

func TestTaskLogsRequiresTaskID(t *testing.T) {
	reader := &fakeLogReader{}
	entry := toolNamed(t, TaskTools(nil, nil, reader, nil, "archie"), "task_logs")

	if _, err := entry.Handler(context.Background(), map[string]any{}); err == nil {
		t.Error("expected error for missing task_id, got nil")
	}
}

func TestTaskLogsDefaultsAttemptToZero(t *testing.T) {
	reader := &fakeLogReader{result: ChatTaskLogResult{Attempt: 0}}
	entry := toolNamed(t, TaskTools(nil, nil, reader, nil, "archie"), "task_logs")

	if _, err := entry.Handler(context.Background(), map[string]any{
		"task_id": float64(7),
	}); err != nil {
		t.Fatal(err)
	}
	if reader.gotAttempt != 0 {
		t.Errorf("attempt = %d, want 0 (zero = 'latest', resolved by the adapter)", reader.gotAttempt)
	}
}

func TestTaskLogsHonoursExplicitAttempt(t *testing.T) {
	reader := &fakeLogReader{result: ChatTaskLogResult{Attempt: 2}}
	entry := toolNamed(t, TaskTools(nil, nil, reader, nil, "archie"), "task_logs")

	if _, err := entry.Handler(context.Background(), map[string]any{
		"task_id": float64(7), "attempt": float64(2),
	}); err != nil {
		t.Fatal(err)
	}
	if reader.gotAttempt != 2 {
		t.Errorf("attempt = %d, want 2", reader.gotAttempt)
	}
}

func TestTaskLogsLimit(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  int
	}{
		{name: "default when absent", input: map[string]any{"task_id": float64(1)}, want: defaultTaskLogLimit},
		{name: "explicit", input: map[string]any{"task_id": float64(1), "limit": float64(5)}, want: 5},
		{name: "clamped to the maximum", input: map[string]any{"task_id": float64(1), "limit": float64(10_000)}, want: maxTaskLogLimit},
		{name: "non-positive falls back to the default", input: map[string]any{"task_id": float64(1), "limit": float64(0)}, want: defaultTaskLogLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeLogReader{result: ChatTaskLogResult{}}
			entry := toolNamed(t, TaskTools(nil, nil, reader, nil, "archie"), "task_logs")
			if _, err := entry.Handler(context.Background(), tc.input); err != nil {
				t.Fatal(err)
			}
			if reader.gotQuery.Limit != tc.want {
				t.Errorf("limit = %d, want %d", reader.gotQuery.Limit, tc.want)
			}
		})
	}
}

func TestTaskLogsForwardsComponentAndContains(t *testing.T) {
	reader := &fakeLogReader{result: ChatTaskLogResult{}}
	entry := toolNamed(t, TaskTools(nil, nil, reader, nil, "archie"), "task_logs")

	if _, err := entry.Handler(context.Background(), map[string]any{
		"task_id": float64(7), "component": "gate", "q": "build failed",
	}); err != nil {
		t.Fatal(err)
	}
	if reader.gotQuery.Component != "gate" {
		t.Errorf("Component = %q, want gate", reader.gotQuery.Component)
	}
	if reader.gotQuery.Contains != "build failed" {
		t.Errorf("Contains = %q, want build failed", reader.gotQuery.Contains)
	}
}

func TestTaskLogsOmittedWhenReaderIsNil(t *testing.T) {
	entries := TaskTools(&fakeLister{}, &fakeCreator{}, nil, nil, "archie")
	for _, e := range entries {
		if e.Name == "task_logs" {
			t.Fatal("task_logs is in the tool set when reader is nil -- a tool that always fails must not be advertised")
		}
	}
}

func TestTaskLogsAppearsWhenReaderIsNonNil(t *testing.T) {
	entries := TaskTools(nil, nil, &fakeLogReader{}, nil, "archie")
	toolNamed(t, entries, "task_logs") // fails the test if absent
}
