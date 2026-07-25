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
