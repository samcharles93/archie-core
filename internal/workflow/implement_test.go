package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
)

// fakeForge implements forge.Forge for TestStageCommitPushCloses.
type fakeForge struct {
	closed    int
	commented []string
	calls     []string
	linkErr   error
	prNumber  int
}

func (f *fakeForge) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	f.closed++
	return nil
}

func (f *fakeForge) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	f.commented = append(f.commented, body)
	return 0, nil
}

// Remaining forge.Forge stubs  --  only CloseIssue and Comment are used by StageCommitPush.
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
	f.calls = append(f.calls, "pr:"+head)
	// A real forge never answers 0, and returning it let a test pass while
	// the PR number was never recorded.
	f.prNumber++
	return f.prNumber, nil
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

func (f *fakeForge) LinkBranch(ctx context.Context, owner, repo string, number int, branch string) error {
	f.calls = append(f.calls, "link:"+branch)
	return f.linkErr
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

	if len(f.commented) != 0 {
		t.Errorf("expected no forge comments, got %d", len(f.commented))
	}
}

func TestOpenPRLinksSourceBranchBeforeCreatingPR(t *testing.T) {
	f := &fakeForge{}
	tc := &TaskContext{
		Forge:  f,
		Task:   &store.Task{ID: 1, Owner: "acme", Repo: "widget", IssueNumber: 42, Title: "Fix bug"},
		Repo:   config.Repo{Owner: "acme", Name: "widget", Base: "main"},
		Branch: "fix/42-bug",
	}
	if err := OpenPR(context.Background(), tc, "summary"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[0] != "link:fix/42-bug" || f.calls[1] != "pr:fix/42-bug" {
		t.Fatalf("forge calls = %v, want [link:fix/42-bug pr:fix/42-bug]", f.calls)
	}
}

func TestStageCommitPushDoesNotUseSyntheticIssueForChatNoOp(t *testing.T) {
	f := &fakeForge{}
	stage := StageCommitPush(func(*TaskContext) string { return "commit msg" })
	tc := &TaskContext{
		Forge:          f,
		BuildSummary:   "already fixed",
		BuildNoChanges: true,
		Task: &store.Task{
			ID:          1,
			Owner:       "o",
			Repo:        "r",
			IssueNumber: 999_001,
			Source:      store.SourceChat,
		},
	}

	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("StageCommitPush.Run(): %v", err)
	}
	if f.closed != 0 || len(f.commented) != 0 {
		t.Fatalf("chat no-op used synthetic issue: close=%d comments=%d", f.closed, len(f.commented))
	}
	if tc.Outcome.Status != store.StatusMerged {
		t.Errorf("Outcome.Status = %q, want %q", tc.Outcome.Status, store.StatusMerged)
	}
}

// LinkBranch is cosmetic: it puts the branch in the issue's sidebar on Gitea
// and does nothing at all on GitHub. Failing the stage on it meant the pull
// request -- the entire point of the run -- was never opened, and the task
// parked. Worse, a retry re-links the same branch, which Gitea answers with
// a conflict, so every retry parked again.
func TestOpenPRSurvivesALinkBranchFailure(t *testing.T) {
	tests := []struct {
		name    string
		linkErr error
	}{
		{name: "already linked", linkErr: errors.New("409 Conflict: branch already linked")},
		{name: "endpoint unavailable", linkErr: errors.New("404 Not Found")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeForge{linkErr: tc.linkErr}
			taskCtx := &TaskContext{
				Forge:  f,
				Task:   &store.Task{ID: 1, Owner: "acme", Repo: "widget", IssueNumber: 42, Title: "Fix bug"},
				Repo:   config.Repo{Owner: "acme", Name: "widget", Base: "main"},
				Branch: "fix/42-bug",
			}

			if err := OpenPR(context.Background(), taskCtx, "summary"); err != nil {
				t.Fatalf("OpenPR: %v\nthe PR is the load-bearing step; cosmetic "+
					"linkage must not stop it", err)
			}
			if taskCtx.Outcome.Status != store.StatusPROpen {
				t.Errorf("Outcome.Status = %q, want %q", taskCtx.Outcome.Status, store.StatusPROpen)
			}
			if taskCtx.Task.PRNumber == 0 {
				t.Error("PRNumber is 0: the pull request was never opened")
			}
			// The link is still attempted -- it is useful when it works.
			if len(f.calls) != 2 || f.calls[0] != "link:fix/42-bug" {
				t.Errorf("forge calls = %v, want the link attempted before the PR", f.calls)
			}
		})
	}
}
