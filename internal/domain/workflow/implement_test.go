package workflow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
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

// alwaysFailingRunner reports the gate as unfixable, so StageBaselineGate
// falls through to its terminal error without needing a real agent loop.
var alwaysFailingRunner = agentRunnerFunc(func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
	return agentexec.Result{Status: "failed"}, nil
})

// TestStageBaselineGateParkErrorCarriesGateOutput guards against the gate's
// real failure output being thrown away. StageBaselineGate used to build the
// park error from res.Status alone -- the actual compiler/test error was
// never in it at all, so a park reason like "go build ./... fails ...
// (status: failed)" gave no way to see why. It also guards the direction and
// size of the bound: baselineParkOutputBytes keeps the *tail* of the output
// (where a test runner's failure detail lives, after pages of "ok" lines),
// not the head, and must actually truncate rather than always including
// everything regardless of the constant's value.
func TestStageBaselineGateParkErrorCarriesGateOutput(t *testing.T) {
	tests := []struct {
		name           string
		script         string // shell body; markers must appear only in output, never in argv
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:         "short output is included in full",
			script:       "echo shortmarker",
			wantContains: []string{"shortmarker"},
		},
		{
			name: "long output is bounded to the tail",
			// filler comfortably exceeds baselineParkOutputBytes so the
			// head marker falls outside the kept window and the tail
			// marker falls inside it.
			script: "echo headmarker; " +
				"head -c " + strconv.Itoa(baselineParkOutputBytes*2) + " /dev/zero | tr '\\0' 'y'; " +
				"echo; echo tailmarker",
			wantContains:   []string{"tailmarker"},
			wantNotContain: []string{"headmarker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "gate.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\n"+tt.script+"\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}

			tc := &TaskContext{
				Task:  &store.Task{ID: 1, Owner: "o", Repo: "r"},
				Repo:  config.Repo{Gate: [][]string{{"sh", script}}},
				Cfg:   config.Config{},
				Agent: alwaysFailingRunner,
				Dir:   dir,
				Log:   slog.New(slog.DiscardHandler),
			}

			err := StageBaselineGate().Run(context.Background(), tc)
			if err == nil {
				t.Fatal("expected an error when the baseline gate is unfixable")
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("park error missing %q: %v", want, err)
				}
			}
			for _, notWant := range tt.wantNotContain {
				if strings.Contains(err.Error(), notWant) {
					t.Errorf("park error contains %q, want truncated away: %v", notWant, err)
				}
			}
		})
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
