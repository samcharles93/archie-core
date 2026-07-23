package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
)

// fakeForge implements forge.Forge for TestStageCommitPushCloses.
type fakeForge struct {
	closed    int
	commented []string
}

func (f *fakeForge) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	f.closed++
	return nil
}

func (f *fakeForge) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	f.commented = append(f.commented, body)
	return 0, nil
}

// Remaining forge.Forge stubs — only CloseIssue and Comment are used by StageCommitPush.
func (f *fakeForge) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (int, error) {
	return 0, nil
}
func (f *fakeForge) Name() string                                { return "fake" }
func (f *fakeForge) AcceptInvitations(ctx context.Context) error { return nil }
func (f *fakeForge) AssignedIssues(ctx context.Context, owner, repo, user string) ([]forge.Issue, error) {
	return nil, nil
}

func (f *fakeForge) IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]forge.Issue, error) {
	return nil, nil
}

func (f *fakeForge) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	return 0, nil
}

func (f *fakeForge) PRState(ctx context.Context, owner, repo string, number int) (string, error) {
	return "", nil
}

func (f *fakeForge) React(ctx context.Context, owner, repo string, number int, reaction string) error {
	return nil
}

func (f *fakeForge) RepliesAfter(ctx context.Context, owner, repo string, number int, afterID int64, exclude string) ([]forge.Reply, error) {
	return nil, nil
}

func (f *fakeForge) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, known []string) {
}
func (f *fakeForge) VerifyPush(ctx context.Context, owner, repo string) error { return nil }

// TestStageCommitPushClosesIssueWhenBuildNoChanges verifies that
// StageCommitPush closes the issue with a comment when BuildNoChanges
// is true, instead of trying to commit and failing.
func TestStageCommitPushClosesIssueWhenBuildNoChanges(t *testing.T) {
	f := &fakeForge{}

	stage := StageCommitPush(func(tc *TaskContext) string {
		return "commit msg"
	})

	tc := &TaskContext{
		Forge:          f,
		BuildSummary:   "already fixed",
		BuildNoChanges: true,
		Dir:            "/tmp/test",
		Branch:         "archie/issue-1",
		Task:           &store.Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 1},
	}

	err := stage.Run(context.Background(), tc)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if f.closed != 1 {
		t.Errorf("expected CloseIssue to be called once, got %d", f.closed)
	} else {
		t.Log("CloseIssue called (no changes needed)")
	}
	if tc.Outcome.Status != store.StatusMerged {
		t.Errorf("expected Outcome=Merged, got %s", tc.Outcome.Status)
	} else {
		t.Log("Outcome set to Merged (workflow stops)")
	}

	found := false
	for _, c := range f.commented {
		if strings.Contains(c, "no changes required") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a comment about no changes required")
	}
}
