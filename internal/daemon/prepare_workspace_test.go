package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// pushedImplementRun models what a remediation task starts from: the implement
// run prepared the clone, committed, and pushed its branch, so origin/<branch>
// holds work base does not. title is the issue title the implement run saw,
// which a later rename can move past the branch the PR actually lives on.
func pushedImplementRun(t *testing.T, trees *worktree.Manager, title string) (dir, branch string, tip plumbing.Hash) {
	t.Helper()
	ctx := context.Background()
	dir, branch, err := trees.Prepare(ctx, "acme", "widget", "main", 7, title, "", "bug", worktree.Fresh)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "widget.go"), []byte("package widget\n"), 0o600); err != nil {
		t.Fatalf("write widget.go: %v", err)
	}
	changed, err := trees.CommitAll(ctx, dir, "fix: the PR work")
	if err != nil || !changed {
		t.Fatalf("CommitAll() = (%v, %v), want (true, nil)", changed, err)
	}
	if err := trees.Push(ctx, dir, branch); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	return dir, branch, headOf(t, dir)
}

// claimedTask returns the running row a dispatch leaves behind, carrying the
// workflow and branch prepareWorkspace must route on. The row is read back from
// the store, so the test drives prepareWorkspace from persisted state rather
// than from a struct the test assembled.
func claimedTask(t *testing.T, st *pgstore.TaskDB, title, workflowName, branch string) *workflow.Task {
	t.Helper()
	ctx := context.Background()
	if _, err := st.EnqueueIssue(ctx, "acme", "widget", 7, title, "body", "bug", ""); err != nil {
		t.Fatalf("EnqueueIssue: %v", err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}
	task.Workflow, task.Branch = workflowName, branch
	if err := st.Update(ctx, task); err != nil {
		t.Fatalf("persist task fields: %v", err)
	}

	persisted, err := st.TaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("TaskByID: %v", err)
	}
	if persisted.Workflow != workflowName || persisted.Branch != branch {
		t.Fatalf("persisted row = (workflow %q, branch %q), want (%q, %q)",
			persisted.Workflow, persisted.Branch, workflowName, branch)
	}
	return persisted
}

func headOf(t *testing.T, dir string) plumbing.Hash {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen(%s): %v", dir, err)
	}
	ref, err := r.Head()
	if err != nil {
		t.Fatalf("Head(%s): %v", dir, err)
	}
	return ref.Hash()
}

// TestPrepareWorkspaceResumesThePersistedBranchForARemediation pins the defect
// a remediation run used to hit: prepareWorkspace always called Manager.Prepare,
// whose refresh resets the worktree onto origin/<base>, so a task continuing an
// open PR branch lost that branch's commits before its container started and
// the agent worked from base. It must instead resume the branch persisted on
// the task row -- here a branch the task's current title would no longer name,
// so a recomputed branch name cannot masquerade as the right answer.
func TestPrepareWorkspaceResumesThePersistedBranchForARemediation(t *testing.T) {
	ctx := context.Background()
	trees := newTestTrees(t, newLocalRemote(t, "acme", "widget"))

	// The implement run pushed its PR work on the branch its then-title named.
	dir, branch, tip := pushedImplementRun(t, trees, "fix: original issue")

	d, st, _ := testDaemon(t, 3, 0)
	// The issue was retitled since; the PR's branch is still the pushed one.
	task := claimedTask(t, st, "fix: retitled issue", "remediate", branch)

	gotDir, ok := d.prepareWorkspace(ctx, task, trees, config.Repo{Owner: "acme", Name: "widget", Base: "main"})
	if !ok {
		t.Fatal("prepareWorkspace() = false, want the remediation prepared on its PR branch")
	}
	if gotDir != dir {
		t.Errorf("prepareWorkspace() dir = %q, want the task's worktree %q", gotDir, dir)
	}
	if _, err := os.Stat(filepath.Join(gotDir, "widget.go")); err != nil {
		t.Fatalf("widget.go missing after prepare: %v (the worktree was reset to base, discarding the PR branch's commits)", err)
	}
	if got := headOf(t, gotDir); got != tip {
		t.Errorf("worktree HEAD = %s, want the PR branch tip %s", got, tip)
	}
}

// TestPrepareWorkspaceResumesAPRBranchAfterTheWorktreeWasRemoved pins the
// retry failure archie-core-866m reports. Manager.Resume opens an
// already-prepared worktree, so a remediation whose local clone is gone -- a
// first run on this host, an expired volume, a worktree the terminal cleanup
// removed before a retry -- can never be resumed, and the retry path offers
// only fresh-from-base or that unrunnable resume. Preparing a resume must
// clone when the worktree is missing, exactly as a fresh prepare does, and
// land on the PR branch tip rather than base.
func TestPrepareWorkspaceResumesAPRBranchAfterTheWorktreeWasRemoved(t *testing.T) {
	ctx := context.Background()
	trees := newTestTrees(t, newLocalRemote(t, "acme", "widget"))
	dir, branch, tip := pushedImplementRun(t, trees, "fix: original issue")

	// The clone is gone before the retry reaches prepareWorkspace.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	d, st, _ := testDaemon(t, 3, 0)
	task := claimedTask(t, st, "fix: original issue", "remediate", branch)

	gotDir, ok := d.prepareWorkspace(ctx, task, trees, config.Repo{Owner: "acme", Name: "widget", Base: "main"})
	if !ok {
		t.Fatal("prepareWorkspace() = false, want the remediation prepared on its PR branch after the worktree was removed")
	}
	if gotDir != dir {
		t.Errorf("prepareWorkspace() dir = %q, want the task's worktree %q", gotDir, dir)
	}
	if _, err := os.Stat(filepath.Join(gotDir, "widget.go")); err != nil {
		t.Fatalf("widget.go missing after prepare: %v (the PR branch's work was not restored)", err)
	}
	if got := headOf(t, gotDir); got != tip {
		t.Errorf("worktree HEAD = %s, want the PR branch tip %s", got, tip)
	}
}

// TestPrepareWorkspaceFailsClosedWhenTheRemediationBranchIsGone pins the other
// half of the same routing: a remediation whose branch is not on the remote
// must park, not quietly fall back to preparing base -- the fallback is exactly
// the state the fix removes, and a remediation run that cannot see its PR
// branch has nothing correct to do.
func TestPrepareWorkspaceFailsClosedWhenTheRemediationBranchIsGone(t *testing.T) {
	ctx := context.Background()
	trees := newTestTrees(t, newLocalRemote(t, "acme", "widget"))

	// The implement run's clone exists and holds its work, but the branch the
	// task row names was never pushed (a row pointing at the wrong branch is
	// the dispatch bug this guards).
	dir, _, tip := pushedImplementRun(t, trees, "fix: original issue")

	d, st, _ := testDaemon(t, 3, 0)
	task := claimedTask(t, st, "fix: original issue", "remediate", "fix/7-never-pushed")

	gotDir, ok := d.prepareWorkspace(ctx, task, trees, config.Repo{Owner: "acme", Name: "widget", Base: "main"})
	if ok {
		t.Errorf("prepareWorkspace() = true (dir %q), want a fail-closed park: a missing PR branch must not be replaced by a base checkout", gotDir)
	}
	if gotDir != "" {
		t.Errorf("prepareWorkspace() dir = %q, want empty on failure", gotDir)
	}

	persisted, err := st.TaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("TaskByID: %v", err)
	}
	if persisted.Status != workflow.StatusParked {
		t.Errorf("task status = %q, want parked", persisted.Status)
	}
	if !strings.Contains(persisted.ParkReason, "resume") {
		t.Errorf("park reason = %q, want it to name the failed resume", persisted.ParkReason)
	}
	if got := headOf(t, dir); got != tip {
		t.Errorf("worktree HEAD = %s, want it left on the PR branch tip %s", got, tip)
	}
}

// TestPrepareWorkspaceStartsBaseForANonRemediationTask keeps the resume route
// from widening: a task that is not a remediation starts fresh on the branch
// its current title names, even when its row carries a branch from an earlier
// lifecycle (the row an operator requeue leaves behind).
func TestPrepareWorkspaceStartsBaseForANonRemediationTask(t *testing.T) {
	ctx := context.Background()
	trees := newTestTrees(t, newLocalRemote(t, "acme", "widget"))

	_, pushedBranch, tip := pushedImplementRun(t, trees, "fix: original issue")

	d, st, _ := testDaemon(t, 3, 0)
	task := claimedTask(t, st, "fix: retitled issue", "implement", pushedBranch)

	gotDir, ok := d.prepareWorkspace(ctx, task, trees, config.Repo{Owner: "acme", Name: "widget", Base: "main"})
	if !ok {
		t.Fatal("prepareWorkspace() = false, want a fresh preparation")
	}
	if got := headOf(t, gotDir); got == tip {
		t.Errorf("worktree HEAD = %s, want base rather than the persisted branch's tip: only a remediation resumes a branch", got)
	}
	if _, err := os.Stat(filepath.Join(gotDir, "widget.go")); !os.IsNotExist(err) {
		t.Errorf("widget.go present after a non-remediation prepare (stat err = %v), want a fresh base checkout", err)
	}
	if task.Branch == pushedBranch {
		t.Errorf("task branch = %q, want the branch recomputed for fresh work rather than the persisted PR branch", task.Branch)
	}
}
