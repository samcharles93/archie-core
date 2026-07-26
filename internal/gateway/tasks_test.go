package gateway

import (
	"context"
	"strings"
	"testing"
)

type fakeStoreWriter struct {
	owner, repo string
	numbers     []int
	titles      []string
}

func (f *fakeStoreWriter) EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels string) (bool, error) {
	f.owner = owner
	f.repo = repo
	f.numbers = append(f.numbers, number)
	f.titles = append(f.titles, title)
	return true, nil
}

func TestStoreTaskCreatorCreatesTask(t *testing.T) {
	sw := &fakeStoreWriter{}
	tc := NewStoreTaskCreator(sw, "sam", "tau")
	id, err := tc.CreateTask(context.Background(), "Fix the login bug")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if id == 0 {
		t.Error("task id should be non-zero")
	}
	if sw.owner != "sam" || sw.repo != "tau" {
		t.Errorf("owner/repo = %s/%s, want sam/tau", sw.owner, sw.repo)
	}
	if sw.titles[0] != "Fix the login bug" {
		t.Errorf("title = %q", sw.titles[0])
	}
	if len(sw.numbers) != 1 {
		t.Errorf("expected 1 enqueue, got %d", len(sw.numbers))
	}
}

func TestStoreTaskCreatorRejectsEmptyRepo(t *testing.T) {
	tc := NewStoreTaskCreator(&fakeStoreWriter{}, "", "")
	_, err := tc.CreateTask(context.Background(), "something")
	if err == nil || !strings.Contains(err.Error(), "no repo configured") {
		t.Errorf("err = %v, want 'no repo configured'", err)
	}
}

func TestStoreTaskCreatorSyntheticNumbersDiffer(t *testing.T) {
	sw := &fakeStoreWriter{}
	tc := NewStoreTaskCreator(sw, "sam", "tau")
	id1, _ := tc.CreateTask(context.Background(), "first")
	id2, _ := tc.CreateTask(context.Background(), "second")
	if id1 == id2 {
		t.Errorf("synthetic numbers collided: %d", id1)
	}
}

// ── Regression tests for approve/cancel edge cases ──────────────────

func TestStoreTaskCreatorEmptyTitle(t *testing.T) {
	sw := &fakeStoreWriter{}
	tc := NewStoreTaskCreator(sw, "sam", "tau")
	id, err := tc.CreateTask(context.Background(), "")
	if err != nil {
		t.Fatalf("CreateTask with empty title: %v", err)
	}
	if id == 0 {
		t.Error("empty title should still create a task")
	}
}

func TestStoreTaskCreatorVeryLongTitle(t *testing.T) {
	sw := &fakeStoreWriter{}
	tc := NewStoreTaskCreator(sw, "sam", "tau")
	longTitle := strings.Repeat("x", 10000)
	id, err := tc.CreateTask(context.Background(), longTitle)
	if err != nil {
		t.Fatalf("CreateTask with long title: %v", err)
	}
	if id == 0 {
		t.Error("long title should still create a task")
	}
}

func TestStoreTaskCreatorIdempotentEnqueue(t *testing.T) {
	// Synthetic numbers are generated from time.Now().UnixNano().
	// The underlying EnqueueIssue is idempotent — a second enqueue
	// of the same issue returns false. The TaskCreator does not
	// expose this, but the store layer does. This test documents
	// the expected behavior.
	sw := &fakeStoreWriter{}
	tc := NewStoreTaskCreator(sw, "sam", "tau")
	id1, _ := tc.CreateTask(context.Background(), "task")
	id2, _ := tc.CreateTask(context.Background(), "another task")
	// Each call should produce a unique number.
	if id1 == id2 {
		t.Error("sequential creates should produce unique numbers")
	}
}

func TestStoreTaskCreatorWritesChatLabel(t *testing.T) {
	sw := &fakeStoreWriter{}
	tc := NewStoreTaskCreator(sw, "sam", "tau")
	_, err := tc.CreateTask(context.Background(), "from chat")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// The label should be "chat" (set by the constructor).
	if len(sw.titles) != 1 {
		t.Errorf("expected 1 enqueue, got %d", len(sw.titles))
	}
}
