package workflow

import (
	"bytes"
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
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/worktree"
)

func TestStageBaselineGateAccountsForRepairAgentUsage(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	script := filepath.Join(dir, "gate.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := agentRunnerFunc(func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{
			Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed,
			TokensUsed: 120, Iterations: 3,
			Usage: agentexec.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CachedTokens: 80},
		}, nil
	})
	task := &store.Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r"}
	tc := &TaskContext{
		Task: task, Repo: config.Repo{Owner: "o", Name: "r", Gate: [][]string{{"sh", script}}},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner, Trees: &worktree.Manager{BotUser: "archie", BotEmail: "archie@example.com"},
		Dir: dir, Log: slog.New(slog.DiscardHandler),
	}
	if err := StageBaselineGate().Run(t.Context(), tc); err != nil {
		t.Fatal(err)
	}
	if task.TokensUsed != 120 || task.Iterations != 3 {
		t.Fatalf("baseline usage = %d tokens/%d iterations, want 120/3", task.TokensUsed, task.Iterations)
	}
	if tc.RunUsage.PromptTokens != 100 || tc.RunUsage.CompletionTokens != 20 || tc.RunUsage.CachedTokens != 80 {
		t.Fatalf("RunUsage = %+v, want prompt=100 completion=20 cached=80", tc.RunUsage)
	}
}

// TestImplementPRBodyShowsFreshVsCachedTokens pins problem 1: a PR body
// built from a heavily-cached run must surface how much of the reported
// prompt-token sum was a cache hit, not read as the full-price total.
func TestImplementPRBodyShowsFreshVsCachedTokens(t *testing.T) {
	tc := &TaskContext{
		Task:         &store.Task{Iterations: 14, TokensUsed: 405004},
		BuildSummary: "did the thing",
		RunUsage: agentexec.Usage{
			PromptTokens: 400021, CompletionTokens: 4983, CachedTokens: 360018,
		},
	}
	body := implementPRBody(tc)
	if !strings.Contains(body, "405004 tokens") {
		t.Fatalf("PR body missing the raw total: %s", body)
	}
	if !strings.Contains(body, "cached") {
		t.Fatalf("PR body does not disclose cache hits: %s", body)
	}
	// fresh = prompt - cached + completion = 400021 - 360018 + 4983 = 44986
	if !strings.Contains(body, "44986 fresh") {
		t.Fatalf("PR body missing the fresh/net figure: %s", body)
	}
}

// TestImplementPRBodyFallsBackWithoutUsageBreakdown guards the degrade
// path: a run with no populated Usage (e.g. no agent stage ran) must still
// produce a sane PR body rather than a "0 fresh + 0 cached" line implying
// billed usage was known and zero.
func TestImplementPRBodyFallsBackWithoutUsageBreakdown(t *testing.T) {
	tc := &TaskContext{
		Task:         &store.Task{Iterations: 1, TokensUsed: 500},
		BuildSummary: "did the thing",
	}
	body := implementPRBody(tc)
	if !strings.Contains(body, "500 tokens") {
		t.Fatalf("PR body missing the total: %s", body)
	}
	if strings.Contains(body, "fresh") || strings.Contains(body, "cached") {
		t.Fatalf("PR body fabricated a breakdown with no usage data: %s", body)
	}
}

// TestExtractFailingGateOutputDropsPassingPackages pins problem 2: the
// baseline-fix mission must not re-inject "ok" lines for packages that
// already pass -- those dominate a large repo's `go test ./...` output and
// squeeze the actual failure detail out of clip's size bound on later
// retries.
func TestExtractFailingGateOutputDropsPassingPackages(t *testing.T) {
	out := "ok  \tgithub.com/example/pkg/a\t0.004s\n" +
		"ok  \tgithub.com/example/pkg/b\t0.012s\n" +
		"--- FAIL: TestSomething (0.00s)\n" +
		"    something_test.go:12: expected 1, got 2\n" +
		"FAIL\tgithub.com/example/pkg/c\t0.010s\n" +
		"ok  \tgithub.com/example/pkg/d\t0.002s\n"

	got := extractFailingGateOutput(out)

	if strings.Contains(got, "pkg/a") || strings.Contains(got, "pkg/b") || strings.Contains(got, "pkg/d") {
		t.Fatalf("passing-package lines survived extraction: %s", got)
	}
	if !strings.Contains(got, "--- FAIL: TestSomething") || !strings.Contains(got, "pkg/c") {
		t.Fatalf("failure detail was dropped: %s", got)
	}
	if len(got) >= len(out) {
		t.Fatalf("extraction did not reduce output size: got %d bytes, want < %d", len(got), len(out))
	}
}

// TestExtractFailingGateOutputDegradesGracefullyForUnrecognizedFormat
// guards against silently dropping real failure information for a gate
// command whose output isn't shaped like `go test` output (a non-Go gate,
// or an unexpected format) -- the full output must be preserved.
func TestExtractFailingGateOutputDegradesGracefullyForUnrecognizedFormat(t *testing.T) {
	out := "lint: 3 problems found\nfile.go:10: unused variable\nfile.go:22: missing return\n"
	if got := extractFailingGateOutput(out); got != out {
		t.Fatalf("extraction altered unrecognized output:\ngot:  %q\nwant: %q", got, out)
	}
}

// TestStageBaselineGateMissionOmitsPassingPackageOutput is the integration
// pin: a real StageBaselineGate run with go-test-shaped failure output must
// build a mission that omits the passing-package noise.
func TestStageBaselineGateMissionOmitsPassingPackageOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gate.sh")
	body := "#!/bin/sh\n" +
		"echo 'ok  \tgithub.com/example/pkg/quietpassingpkg\t0.004s'\n" +
		"echo '--- FAIL: TestBroken (0.00s)'\n" +
		"echo '    broken_test.go:5: boom'\n" +
		"echo 'FAIL\tgithub.com/example/pkg/broken\t0.010s'\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	var gotMission string
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		gotMission = req.Mission
		return agentexec.Result{Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed}, nil
	})
	tc := &TaskContext{
		Task:  &store.Task{ID: 1, Owner: "o", Repo: "r"},
		Repo:  config.Repo{Owner: "o", Name: "r", Gate: [][]string{{"sh", script}}},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner, Trees: &worktree.Manager{BotUser: "archie", BotEmail: "archie@example.com"},
		Dir: dir, Log: slog.New(slog.DiscardHandler),
	}
	if err := StageBaselineGate().Run(t.Context(), tc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotMission, "quietpassingpkg") {
		t.Fatalf("mission still contains a passing package's output: %s", gotMission)
	}
	if !strings.Contains(gotMission, "TestBroken") || !strings.Contains(gotMission, "pkg/broken") {
		t.Fatalf("mission lost the actual failure detail: %s", gotMission)
	}
}

// TestStageBaselineGateSetsBaselineFixedWhenItCommits is the companion
// regression case for archie-core-95dj: when the repair agent's changes
// are actually committed, tc.BaselineFixed must be set so a later
// no-op build stage cannot cause StageCommitPush to discard the fix.
func TestStageBaselineGateSetsBaselineFixedWhenItCommits(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	// Real clones get commit.gpgsign=false via Manager.setIdentity; a bare
	// `git init` here does not, so it inherits the host's global config --
	// see worktree_test.go's disableSigning for the same fixture gap.
	runGit(t, dir, "config", "commit.gpgsign", "false")
	script := filepath.Join(dir, "gate.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := agentRunnerFunc(func(_ context.Context, runDir string, _ agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		if err := os.WriteFile(filepath.Join(runDir, "fixed.txt"), []byte("fix\n"), 0o644); err != nil {
			return agentexec.Result{}, err
		}
		return agentexec.Result{Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed}, nil
	})
	task := &store.Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r"}
	tc := &TaskContext{
		Task: task, Repo: config.Repo{Owner: "o", Name: "r", Gate: [][]string{{"sh", script}}},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner, Trees: &worktree.Manager{BotUser: "archie", BotEmail: "archie@example.com"},
		Dir: dir, Log: slog.New(slog.DiscardHandler),
	}
	if err := StageBaselineGate().Run(t.Context(), tc); err != nil {
		t.Fatal(err)
	}
	if !tc.BaselineFixed {
		t.Fatal("BaselineFixed = false, want true after a real committed fix")
	}
}

func TestStageBaselineGateWarningIncludesFailureTail(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gate.sh")
	// Pad past baselineWarnLogBytes (2048) so clipTail short-circuit does
	// NOT save head-clipping from being detected. The marker near the END
	// would otherwise be inside the window either way; a marker past the
	// window asserts the direction.
	body := "echo headmarker; head -c 5000 /dev/zero | tr '\\0' x; echo; echo tailmarker; exit 1\n"
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	tc := &TaskContext{
		Task:  &store.Task{ID: 1, Owner: "o", Repo: "r"},
		Repo:  config.Repo{Gate: [][]string{{"sh", script}}},
		Agent: alwaysFailingRunner,
		Dir:   dir,
		Log:   slog.New(slog.NewJSONHandler(&logs, nil)),
	}

	_ = StageBaselineGate().Run(t.Context(), tc)
	logsStr := logs.String()
	if !strings.Contains(logsStr, "tailmarker") {
		t.Fatalf("baseline warning omitted the useful failure tail: %s", logsStr)
	}
	if strings.Contains(logsStr, "headmarker") {
		t.Fatalf("baseline warning included the head marker -- clip is retaining the head instead of the tail")
	}
}

// TestStageBaselineGateAgentRunErrorSkipsAgentFinish pins the ordering
// inside StageBaselineGate: when the baseline-fix agent returns an error,
// the stage must surface that error WITHOUT first emitting an
// agent_finish event whose status/stop_reason/tokens are all zero. AgentStage
// (internal/domain/workflow/agent.go) bails on every error path before its
// own agent_finish emit, so the baseline-fix path must match.
func TestStageBaselineGateAgentRunErrorSkipsAgentFinish(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gate.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	sub := bus.Subscribe(8)
	t.Cleanup(sub.Close)
	runner := agentRunnerFunc(func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{}, errors.New("simulated transport failure")
	})
	task := &store.Task{ID: 1, Owner: "o", Repo: "r"}
	tc := &TaskContext{
		Task:  task,
		Repo:  config.Repo{Owner: "o", Name: "r", Gate: [][]string{{"sh", script}}},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner,
		Bus:   bus,
		Dir:   dir,
		Log:   slog.New(slog.DiscardHandler),
	}
	if err := StageBaselineGate().Run(t.Context(), tc); err == nil {
		t.Fatal("expected StageBaselineGate to surface the agent error")
	}
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == "agent_finish" {
				t.Fatalf("baseline-fix errored but agent_finish was still published: %+v", ev)
			}
		default:
			return
		}
	}
}

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

// fakeTrees implements Trees, tracking calls without touching a real
// worktree. commitAllChanged controls what CommitAll reports.
type fakeTrees struct {
	commitAllChanged bool
	pushed           bool
	pushBranch       string
}

func (f *fakeTrees) Prepare(context.Context, string, string, string, int, string, string, string) (string, string, error) {
	return "", "", nil
}

func (f *fakeTrees) CommitAll(context.Context, string, string) (bool, error) {
	return f.commitAllChanged, nil
}

func (f *fakeTrees) Push(_ context.Context, _, branch string) error {
	f.pushed = true
	f.pushBranch = branch
	return nil
}

func (f *fakeTrees) Diff(context.Context, string, string) (string, error) { return "", nil }

func (f *fakeTrees) ChangedFiles(context.Context, string, string) ([]string, error) { return nil, nil }

func (f *fakeTrees) ChangedLines(context.Context, string, string) (int, error) { return 0, nil }

func (f *fakeTrees) Snapshot(context.Context, string, string) error { return nil }

// TestStageCommitPushPushesBaselineFixEvenWithNothingNewToCommit is the
// regression case for archie-core-95dj: StageBaselineGate already
// committed a real, gate-verified fix for a pre-existing failure, and the
// build stage found nothing further to do. The worktree has no *new*
// uncommitted changes (CommitAll correctly reports changed=false), but
// the baseline-fix commit already sitting on the branch must still reach
// a PR -- not be silently discarded as "no changes required".
func TestStageCommitPushPushesBaselineFixEvenWithNothingNewToCommit(t *testing.T) {
	trees := &fakeTrees{commitAllChanged: false}
	stage := StageCommitPush(func(*TaskContext) string { return "commit msg" })
	tc := &TaskContext{
		Trees:         trees,
		BaselineFixed: true,
		Dir:           "/tmp/test",
		Branch:        "archie/issue-1",
		Task:          &store.Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 1},
	}

	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("StageCommitPush.Run() = %v, want nil", err)
	}
	if !trees.pushed {
		t.Fatal("Push was not called; the baseline-fix commit would be discarded")
	}
	if trees.pushBranch != "archie/issue-1" {
		t.Errorf("pushed branch = %q, want %q", trees.pushBranch, "archie/issue-1")
	}
}

// TestStageCommitPushStillErrorsOnEmptyTreeWithoutBaselineFix confirms the
// existing "nothing to commit" guard survives: only a real baseline-fix
// commit excuses an empty tree, not a plain no-op build.
func TestStageCommitPushStillErrorsOnEmptyTreeWithoutBaselineFix(t *testing.T) {
	trees := &fakeTrees{commitAllChanged: false}
	stage := StageCommitPush(func(*TaskContext) string { return "commit msg" })
	tc := &TaskContext{
		Trees:  trees,
		Dir:    "/tmp/test",
		Branch: "archie/issue-1",
		Task:   &store.Task{ID: 1, Owner: "o", Repo: "r", IssueNumber: 1},
	}

	if err := stage.Run(context.Background(), tc); err == nil {
		t.Fatal("StageCommitPush.Run() = nil, want an error for an empty tree with no baseline fix")
	}
	if trees.pushed {
		t.Fatal("Push was called despite nothing to commit and no baseline fix")
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
