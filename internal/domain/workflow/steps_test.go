package workflow

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx := t.Context()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRepoWithOriginRef creates a repo with a base commit, then labels
// that commit "origin/<base>" (a plain local branch standing in for a
// remote-tracking ref) so Manager.Diff/ChangedFiles  --  which always diff
// against "origin/<base>"  --  resolve without a real clone/push round trip.
func gitRepoWithOriginRef(t *testing.T, base string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", base)
	runGit(t, dir, "config", "user.name", "archie-bot")
	runGit(t, dir, "config", "user.email", "archie-bot@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "seed")
	runGit(t, dir, "branch", "origin/"+base, base)
	return dir
}

func newYaegiGateTaskContext(t *testing.T, dir string) *TaskContext {
	t.Helper()
	return &TaskContext{
		Task:  &Task{ID: 1, Owner: "acme", Repo: "todo", IssueNumber: 42},
		Repo:  config.Repo{Owner: "acme", Name: "todo", Base: "main"},
		Trees: &worktree.Manager{WorkDir: t.TempDir()},
		Dir:   dir,
		Log:   slog.New(slog.DiscardHandler),
	}
}

func writeGateGo(t *testing.T, dir, src string) {
	t.Helper()
	archieDir := filepath.Join(dir, ".archie")
	if err := os.MkdirAll(archieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archieDir, "gate.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStageYaegiGateNoScriptIsNoop(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := gitRepoWithOriginRef(t, "main")
	tc := newYaegiGateTaskContext(t, dir)

	if err := StageYaegiGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageYaegiGate() = %v, want nil (no .archie/gate.go)", err)
	}
}

func TestStageYaegiGateBlocksOnErrorFinding(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := gitRepoWithOriginRef(t, "main")
	writeGateGo(t, dir, `package gate

import (
	"strings"

	"github.com/samcharles93/archie-core/internal/gate"
)

func Check(ctx gate.GateContext) []gate.Finding {
	var findings []gate.Finding
	for _, line := range strings.Split(ctx.Diff, "\n") {
		if strings.HasPrefix(line, "+") && strings.Contains(line, "panic(") {
			findings = append(findings, gate.Finding{Level: "error", Message: "new panic() call"})
		}
	}
	return findings
}
`)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { panic(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "add panic")

	tc := newYaegiGateTaskContext(t, dir)
	err := StageYaegiGate().Run(context.Background(), tc)
	if err == nil {
		t.Fatal("StageYaegiGate() = nil, want an error for the blocking finding")
	}
}

func TestStageYaegiGateAllowsWarnFinding(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := gitRepoWithOriginRef(t, "main")
	writeGateGo(t, dir, `package gate

import "github.com/samcharles93/archie-core/internal/gate"

func Check(ctx gate.GateContext) []gate.Finding {
	return []gate.Finding{{Level: "warn", Message: "advisory only"}}
}
`)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "add notes")

	tc := newYaegiGateTaskContext(t, dir)
	if err := StageYaegiGate().Run(context.Background(), tc); err == nil {
		t.Fatal("StageYaegiGate() = nil, want legacy gate rejection")
	}
}

func TestStageYaegiGateRejectsUnknownLevel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := gitRepoWithOriginRef(t, "main")
	writeGateGo(t, dir, `package gate

import "github.com/samcharles93/archie-core/internal/gate"

func Check(ctx gate.GateContext) []gate.Finding {
	return []gate.Finding{{Level: "fatal", Message: "mis-typed level"}}
}
`)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "add notes")

	tc := newYaegiGateTaskContext(t, dir)
	if err := StageYaegiGate().Run(context.Background(), tc); err == nil {
		t.Fatal("StageYaegiGate() = nil, want an error for an unrecognized finding level (fail closed)")
	}
}

func TestStageRepoStagesNilLoaderIsNoop(t *testing.T) {
	tc := &TaskContext{Log: slog.New(slog.DiscardHandler)}
	if err := StageRepoStages().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageRepoStages() = %v, want nil (no loader wired up)", err)
	}
}

func TestStageRepoStagesRunsAllInOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".archie", "stages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".archie", "stages", "first.go"), []byte("package stages"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := &TaskContext{Dir: dir, Log: slog.New(slog.DiscardHandler)}
	if err := StageRepoStages().Run(context.Background(), tc); err == nil {
		t.Fatal("StageRepoStages() = nil, want legacy stage rejection")
	}
}

func TestStageRepoStagesStopsOnOutcome(t *testing.T) {
	t.Skip("repository-authored stages are no longer executable")
}

func TestStageRepoStagesPropagatesLoaderError(t *testing.T) {
	t.Skip("repository-authored stage loaders are no longer called")
}

// TestStageDiffCapUnlimitedWhenCapIsZero pins that an explicit 0 switches the
// cap off. The dashboard schema documents 0 that way, and this is the step that
// has to honour it -- for a long time it could not, because the config loader
// rewrote an explicit 0 to the default before the step ever saw it.
func TestStageDiffCapUnlimitedWhenCapIsZero(t *testing.T) {
	tc := &TaskContext{
		Cfg:   config.Config{DiffCapLines: new(0)},
		Trees: &fakeTrees{changedLines: 10_000},
		Repo:  config.Repo{Owner: "acme", Name: "app"},
	}

	if err := StageDiffCap().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageDiffCap: %v", err)
	}
	if tc.Outcome.Status == StatusParked {
		t.Fatalf("task parked with the cap switched off: %q", tc.Outcome.Detail)
	}
}

// TestStageDiffCapParksWithAnActionableReason is the regression case for a park
// reason that told the operator to do something impossible. A parked task's
// actions are retry, abandon and reject (internal/taskstate), so "approve
// manually" sent them looking for a button that does not exist -- which is
// exactly what happened on task 11.
func TestStageDiffCapParksWithAnActionableReason(t *testing.T) {
	tc := &TaskContext{
		Cfg:   config.Config{DiffCapLines: new(400)},
		Trees: &fakeTrees{changedLines: 789},
		Repo:  config.Repo{Owner: "acme", Name: "app"},
	}

	if err := StageDiffCap().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageDiffCap: %v", err)
	}
	if tc.Outcome.Status != StatusParked {
		t.Fatalf("Outcome.Status = %q, want parked at 789 lines over a 400 cap", tc.Outcome.Status)
	}
	if strings.Contains(tc.Outcome.Detail, "approve") {
		t.Errorf("park reason offers approve, which is not an action a parked task has: %q", tc.Outcome.Detail)
	}
	for _, want := range []string{"789", "400", "diff_cap_lines"} {
		if !strings.Contains(tc.Outcome.Detail, want) {
			t.Errorf("park reason %q is missing %q", tc.Outcome.Detail, want)
		}
	}
}
