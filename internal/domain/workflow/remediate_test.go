package workflow

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
)

func TestDecodeReviewUnitRejectsEmptyPayload(t *testing.T) {
	if _, err := DecodeReviewUnit(""); !errors.Is(err, ErrNoReviewPayload) {
		t.Fatalf("DecodeReviewUnit(\"\") error = %v, want ErrNoReviewPayload", err)
	}
}

func TestEncodeDecodeReviewUnitRoundTrips(t *testing.T) {
	want := ReviewUnit{
		ReviewID: 42, Author: "reviewer-bot", State: "requested_changes", Body: "please fix",
		Comments: []ReviewUnitComment{{CommentID: 7, Path: "a.go", Line: 3, Body: "nil check missing"}},
	}
	payload, err := EncodeReviewUnit(want)
	if err != nil {
		t.Fatalf("EncodeReviewUnit: %v", err)
	}
	got, err := DecodeReviewUnit(payload)
	if err != nil {
		t.Fatalf("DecodeReviewUnit: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-tripped unit = %+v, want %+v", got, want)
	}
}

func TestStageCheckReviewPayloadFailsOnEmptyPayload(t *testing.T) {
	tc := &TaskContext{Task: &Task{ID: 1}}
	if err := StageCheckReviewPayload().Run(t.Context(), tc); !errors.Is(err, ErrNoReviewPayload) {
		t.Fatalf("StageCheckReviewPayload error = %v, want ErrNoReviewPayload", err)
	}
}

func TestStageRemediationRoundCapCountsARoundUnderTheCap(t *testing.T) {
	forge := &fakeForge{}
	tc := &TaskContext{
		Task: &Task{ID: 1, RemediationRounds: 1}, Repo: config.Repo{MaxRetries: 3}, Forge: forge,
		Log: slog.New(slog.DiscardHandler),
	}
	if err := StageRemediationRoundCap().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageRemediationRoundCap: %v", err)
	}
	if tc.Task.RemediationRounds != 2 {
		t.Fatalf("RemediationRounds = %d, want 2", tc.Task.RemediationRounds)
	}
	if tc.Outcome.Status != "" {
		t.Fatalf("Outcome = %+v, want zero value (workflow continues)", tc.Outcome)
	}
	if len(forge.commented) != 0 {
		t.Fatalf("commented = %v, want no comment posted under the cap", forge.commented)
	}
}

func TestStageRemediationRoundCapParksAtTheCapAndPostsOneComment(t *testing.T) {
	forge := &fakeForge{}
	tc := &TaskContext{
		Task: &Task{ID: 1, RemediationRounds: 3, PRNumber: 9}, Repo: config.Repo{MaxRetries: 3}, Forge: forge,
		Log: slog.New(slog.DiscardHandler),
	}
	if err := StageRemediationRoundCap().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageRemediationRoundCap: %v", err)
	}
	if tc.Task.RemediationRounds != 3 {
		t.Fatalf("RemediationRounds = %d, want unchanged at 3", tc.Task.RemediationRounds)
	}
	if tc.Outcome.Status != StatusParked {
		t.Fatalf("Outcome.Status = %q, want parked", tc.Outcome.Status)
	}
	if len(forge.commented) != 1 {
		t.Fatalf("commented = %v, want exactly one comment", forge.commented)
	}
}

func TestStageRemediationRoundCapZeroMeansUnlimited(t *testing.T) {
	tc := &TaskContext{
		Task: &Task{ID: 1, RemediationRounds: 50}, Repo: config.Repo{}, Cfg: config.Config{MaxRetries: 0},
		Forge: &fakeForge{}, Log: slog.New(slog.DiscardHandler),
	}
	if err := StageRemediationRoundCap().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageRemediationRoundCap: %v", err)
	}
	if tc.Outcome.Status == StatusParked {
		t.Fatal("Outcome = parked, want no cap enforced when max_retries is unset")
	}
	if tc.Task.RemediationRounds != 51 {
		t.Fatalf("RemediationRounds = %d, want 51", tc.Task.RemediationRounds)
	}
}

func TestStageRemediationCommitPushSkipsAnEmptyTreeWithoutError(t *testing.T) {
	trees := &fakeTrees{commitAllChanged: false}
	tc := &TaskContext{
		Task: &Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 1}, Trees: trees,
		Dir: "/tmp/x", Branch: "archie/issue-1",
	}
	if err := StageRemediationCommitPush().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageRemediationCommitPush: %v", err)
	}
	if trees.pushed {
		t.Fatal("Push called with nothing committed, want no push")
	}
}

func TestStageRemediationCommitPushCommitsAndPushesAChange(t *testing.T) {
	trees := &fakeTrees{commitAllChanged: true}
	tc := &TaskContext{
		Task: &Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 1}, Trees: trees,
		Dir: "/tmp/x", Branch: "archie/issue-1", Log: slog.New(slog.DiscardHandler),
	}
	if err := StageRemediationCommitPush().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageRemediationCommitPush: %v", err)
	}
	if !trees.pushed || trees.pushBranch != "archie/issue-1" {
		t.Fatalf("pushed=%v pushBranch=%q, want true archie/issue-1", trees.pushed, trees.pushBranch)
	}
}

func TestStageRemediationReplyThreadsOntoTheFirstComment(t *testing.T) {
	forge := &fakeForge{}
	tc := &TaskContext{
		Task:         &Task{ID: 1, Owner: "o", Repo: "r", PRNumber: 9, ReviewPayload: "stale"},
		Forge:        forge,
		BuildSummary: "fixed the nil check",
		reviewUnit:   ReviewUnit{Comments: []ReviewUnitComment{{CommentID: 55, Body: "fix this"}}},
	}
	if err := StageRemediationReply().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageRemediationReply: %v", err)
	}
	if forge.replyCommentID != 55 {
		t.Fatalf("replyCommentID = %d, want 55", forge.replyCommentID)
	}
	if len(forge.commented) != 0 {
		t.Fatalf("commented = %v, want the plain-comment fallback unused", forge.commented)
	}
	if tc.Task.ReviewPayload != "" {
		t.Fatalf("ReviewPayload = %q, want cleared after the run consumed it", tc.Task.ReviewPayload)
	}
	if tc.Outcome.Status != StatusPROpen {
		t.Fatalf("Outcome.Status = %q, want pr_open", tc.Outcome.Status)
	}
}

func TestStageRemediationReplyFallsBackToAPlainCommentForABareReview(t *testing.T) {
	forge := &fakeForge{}
	tc := &TaskContext{
		Task:         &Task{ID: 1, Owner: "o", Repo: "r", PRNumber: 9},
		Forge:        forge,
		BuildSummary: "no change needed",
		reviewUnit:   ReviewUnit{Body: "looks fine overall"},
	}
	if err := StageRemediationReply().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageRemediationReply: %v", err)
	}
	if forge.replyCommentID != 0 {
		t.Fatalf("replyCommentID = %d, want 0 (no threaded reply)", forge.replyCommentID)
	}
	if len(forge.commented) != 1 {
		t.Fatalf("commented = %v, want exactly one plain comment", forge.commented)
	}
}

func TestStagePrepareWorktreeOnBranchRequiresABranch(t *testing.T) {
	tc := &TaskContext{Task: &Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 5}, Trees: &fakeTrees{}}
	if err := StagePrepareWorktreeOnBranch().Run(t.Context(), tc); err == nil {
		t.Fatal("StagePrepareWorktreeOnBranch with no branch = nil error, want a fail-closed error")
	}
}

func TestStagePrepareWorktreeOnBranchBindsThePRBranch(t *testing.T) {
	trees := &fakeTrees{dir: "/worktrees/o-r-5", prepareBranch: "unused/fresh"}
	tc := &TaskContext{
		Task:  &Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 5, Branch: "archie/issue-5"},
		Trees: trees,
	}
	if err := StagePrepareWorktreeOnBranch().Run(t.Context(), tc); err != nil {
		t.Fatalf("StagePrepareWorktreeOnBranch: %v", err)
	}
	if trees.prepareTarget != "archie/issue-5" {
		t.Fatalf("prepare target = %q, want the task branch archie/issue-5", trees.prepareTarget)
	}
	if tc.Dir != "/worktrees/o-r-5" || tc.Branch != "archie/issue-5" {
		t.Fatalf("tc.Dir/Branch = %q/%q, want /worktrees/o-r-5/archie/issue-5", tc.Dir, tc.Branch)
	}
	if tc.Task.Branch != "archie/issue-5" {
		t.Fatalf("tc.Task.Branch = %q, want archie/issue-5", tc.Task.Branch)
	}
}

func TestStagePrepareWorktreePreparesFresh(t *testing.T) {
	trees := &fakeTrees{dir: "/worktrees/o-r-5", prepareBranch: "archie/issue-5"}
	tc := &TaskContext{Task: &Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 5}, Trees: trees}
	if err := StagePrepareWorktree().Run(t.Context(), tc); err != nil {
		t.Fatalf("StagePrepareWorktree: %v", err)
	}
	if trees.prepareTarget != PrepareFresh {
		t.Fatalf("prepare target = %q, want the fresh target", trees.prepareTarget)
	}
	if tc.Dir != "/worktrees/o-r-5" || tc.Branch != "archie/issue-5" {
		t.Fatalf("tc.Dir/Branch = %q/%q, want /worktrees/o-r-5/archie/issue-5", tc.Dir, tc.Branch)
	}
}

// TestRemediateBindsItsWorktreeBeforeTheBuilderRuns is the release-blocker
// regression for a remediate run that never bound its worktree. The
// in-container resume stage was retired in favour of daemon-side preparation,
// but nothing in the workflow resolved the directory the daemon prepared and
// the branch its pull request lives on. The builder therefore ran with an empty
// Dir and the final stage pushed an empty branch.
func TestRemediateBindsItsWorktreeBeforeTheBuilderRuns(t *testing.T) {
	const (
		prBranch = "archie/issue-1"
		worktree = "/worktrees/o-r-1"
	)
	trees := &fakeTrees{dir: worktree, prepareBranch: prBranch, commitAllChanged: true}
	forge := &fakeForge{}
	payload, err := EncodeReviewUnit(ReviewUnit{
		ReviewID: 1, State: "requested_changes",
		Comments: []ReviewUnitComment{{CommentID: 10, Path: "a.go", Line: 4, Body: "nil check missing"}},
	})
	if err != nil {
		t.Fatalf("EncodeReviewUnit: %v", err)
	}
	task := &Task{
		ID: 1, Owner: "o", Repo: "r", IssueNumber: 1, PRNumber: 9,
		Branch: prBranch, ReviewPayload: payload,
	}
	var builderDir string
	runner := agentRunnerFunc(func(_ context.Context, dir string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		builderDir = dir
		return agentexec.Result{
			Version: agentexec.ProtocolVersion, TaskID: req.TaskID, Attempt: req.Attempt, Stage: req.Stage,
			Status: agentexec.StatusPassed, Summary: "added the nil check", Changes: []string{"a.go"},
		}, nil
	})
	tc := &TaskContext{
		Task: task, Repo: config.Repo{Owner: "o", Name: "r", MaxRetries: 3},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner, Trees: trees, Forge: forge, Log: slog.New(slog.DiscardHandler),
	}

	for _, stage := range Remediate().Stages {
		if err := stage.Run(t.Context(), tc); err != nil {
			t.Fatalf("stage %s: %v", stage.Name, err)
		}
		if tc.Outcome.Status != "" {
			break
		}
	}

	if tc.Dir == "" {
		t.Fatal("remediate left tc.Dir empty: no stage bound the task's worktree")
	}
	if tc.Dir != worktree {
		t.Fatalf("tc.Dir = %q, want the prepared worktree %q", tc.Dir, worktree)
	}
	if tc.Branch != prBranch {
		t.Fatalf("tc.Branch = %q, want the task's PR branch %q", tc.Branch, prBranch)
	}
	if builderDir != worktree {
		t.Fatalf("builder ran with dir %q, want the bound worktree %q", builderDir, worktree)
	}
	if trees.pushBranch != prBranch {
		t.Fatalf("pushed branch = %q, want the task's PR branch %q", trees.pushBranch, prBranch)
	}
	if trees.prepareTarget != PrepareTarget(prBranch) {
		t.Fatalf("prepare target = %q, want the task's PR branch %q (a fresh prepare would have reset onto base)", trees.prepareTarget, prBranch)
	}
}

func TestRemediateEndToEndAddressesCommentsAndReplies(t *testing.T) {
	trees := &fakeTrees{dir: "/worktrees/o-r-1", commitAllChanged: true}
	forge := &fakeForge{}
	payload, err := EncodeReviewUnit(ReviewUnit{
		ReviewID: 1, State: "requested_changes",
		Comments: []ReviewUnitComment{{CommentID: 10, Path: "a.go", Line: 4, Body: "nil check missing"}},
	})
	if err != nil {
		t.Fatalf("EncodeReviewUnit: %v", err)
	}
	task := &Task{
		ID: 1, Owner: "o", Repo: "r", IssueNumber: 1, PRNumber: 9,
		Branch: "archie/issue-1", ReviewPayload: payload,
	}
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{
			Version: agentexec.ProtocolVersion, TaskID: req.TaskID, Attempt: req.Attempt, Stage: req.Stage,
			Status: agentexec.StatusPassed, Summary: "added the nil check", Changes: []string{"a.go"},
		}, nil
	})
	tc := &TaskContext{
		Task: task, Repo: config.Repo{Owner: "o", Name: "r", MaxRetries: 3},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner, Trees: trees, Forge: forge, Log: slog.New(slog.DiscardHandler),
	}

	wf := Remediate()
	for _, stage := range wf.Stages {
		if err := stage.Run(t.Context(), tc); err != nil {
			t.Fatalf("stage %s: %v", stage.Name, err)
		}
		if tc.Outcome.Status != "" {
			break
		}
	}

	if !trees.pushed {
		t.Fatal("remediation commit was never pushed")
	}
	if forge.replyCommentID != 10 {
		t.Fatalf("replyCommentID = %d, want 10", forge.replyCommentID)
	}
	if task.RemediationRounds != 1 {
		t.Fatalf("RemediationRounds = %d, want 1 (one round consumed)", task.RemediationRounds)
	}
	if task.ReviewPayload != "" {
		t.Fatalf("ReviewPayload = %q, want cleared", task.ReviewPayload)
	}
	if tc.Outcome.Status != StatusPROpen {
		t.Fatalf("Outcome.Status = %q, want pr_open", tc.Outcome.Status)
	}
}
