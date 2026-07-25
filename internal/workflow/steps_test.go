package workflow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
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
		Task:  &store.Task{ID: 1, Owner: "acme", Repo: "todo", IssueNumber: 42},
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
	if err := StageYaegiGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageYaegiGate() = %v, want nil (warn findings don't block)", err)
	}
}

func TestStageRepoStagesNilLoaderIsNoop(t *testing.T) {
	tc := &TaskContext{Log: slog.New(slog.DiscardHandler)}
	if err := StageRepoStages().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageRepoStages() = %v, want nil (no loader wired up)", err)
	}
}

func TestStageRepoStagesRunsAllInOrder(t *testing.T) {
	var ran []string
	tc := &TaskContext{
		Log: slog.New(slog.DiscardHandler),
		CustomStages: func(string) ([]Stage, error) {
			return []Stage{
				{Name: "first", Run: func(context.Context, *TaskContext) error { ran = append(ran, "first"); return nil }},
				{Name: "second", Run: func(context.Context, *TaskContext) error { ran = append(ran, "second"); return nil }},
			}, nil
		},
	}
	if err := StageRepoStages().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageRepoStages() = %v", err)
	}
	if len(ran) != 2 || ran[0] != "first" || ran[1] != "second" {
		t.Fatalf("ran = %v, want [first second]", ran)
	}
}

func TestStageRepoStagesStopsOnOutcome(t *testing.T) {
	var ran []string
	tc := &TaskContext{
		Log: slog.New(slog.DiscardHandler),
		CustomStages: func(string) ([]Stage, error) {
			return []Stage{
				{Name: "first", Run: func(_ context.Context, tc *TaskContext) error {
					ran = append(ran, "first")
					tc.Outcome = Outcome{Status: "parked", Detail: "stop here"}
					return nil
				}},
				{Name: "second", Run: func(context.Context, *TaskContext) error { ran = append(ran, "second"); return nil }},
			}, nil
		},
	}
	if err := StageRepoStages().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageRepoStages() = %v", err)
	}
	if len(ran) != 1 || ran[0] != "first" {
		t.Fatalf("ran = %v, want only [first] (second stage should not run after an outcome is set)", ran)
	}
}

func TestStageRepoStagesPropagatesLoaderError(t *testing.T) {
	tc := &TaskContext{
		Log: slog.New(slog.DiscardHandler),
		CustomStages: func(string) ([]Stage, error) {
			return nil, errors.New("boom")
		},
	}
	if err := StageRepoStages().Run(context.Background(), tc); err == nil {
		t.Fatal("StageRepoStages() = nil, want the loader's error propagated")
	}
}
