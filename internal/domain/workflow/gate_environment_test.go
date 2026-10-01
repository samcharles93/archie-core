package workflow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
)

// gateScript writes an executable shell script running `body` and returns
// the argv that invokes it.
func gateScript(t *testing.T, dir, body string) []string {
	t.Helper()
	path := filepath.Join(dir, "gate.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{"sh", path}
}

// TestGateEnvironmentCauseClassifiesOnlyInfrastructureFailures pins the
// classification the baseline stage acts on. It must recognise the shapes a
// missing host resource produces (the command could not start at all, or a
// tool says its dependency is absent) and must not mistake an ordinary code
// failure for one of them -- a false positive parks a task the builder could
// have fixed.
func TestGateEnvironmentCauseClassifiesOnlyInfrastructureFailures(t *testing.T) {
	failingExit := errors.New("exit status 1")
	missingBinary := &exec.Error{Name: "go", Err: exec.ErrNotFound}

	tests := []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{
			name: "pgtest cannot reach the docker daemon",
			out: "pgtest: start postgres:18 (is Docker running?): " +
				"rootless Docker not found, failed to create Docker provider\nFAIL\tinternal/webui",
			err:  failingExit,
			want: true,
		},
		{
			name: "testcontainers cannot connect to the daemon",
			out:  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			err:  failingExit,
			want: true,
		},
		{
			name: "the gate binary is not installed",
			err:  missingBinary,
			want: true,
		},
		{
			name: "an ordinary test failure is a code failure",
			out:  "--- FAIL: TestBroken (0.00s)\n    broken_test.go:5: want 1, got 2\nFAIL\tpkg/x",
			err:  failingExit,
			want: false,
		},
		{
			name: "a compile error is a code failure",
			out:  "# pkg/x\n./x.go:3:2: undefined: missing",
			err:  failingExit,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := gateEnvironmentCause(tt.out, tt.err)
			if got := cause != ""; got != tt.want {
				t.Fatalf("gateEnvironmentCause() = %q, want environmental=%v", cause, tt.want)
			}
		})
	}
}

// TestStageBaselineGateParksEnvironmentFailureWithoutDispatch is acceptance
// (a): a gate that failed because the environment lacks a resource parks with
// that cause and never starts the baseline-fix builder -- nothing in the
// worktree can fix a missing Docker daemon, so dispatching one only burns the
// run budget.
func TestStageBaselineGateParksEnvironmentFailureWithoutDispatch(t *testing.T) {
	tests := []struct {
		name      string
		gate      func(t *testing.T, dir string) [][]string
		wantCause string
	}{
		{
			name: "no docker daemon for the test harness",
			gate: func(t *testing.T, dir string) [][]string {
				argv := gateScript(t, dir,
					"echo 'pgtest: start postgres:18 (is Docker running?): rootless Docker not found, failed to create Docker provider'\n"+
						"echo 'FAIL\tinternal/webui'\n"+
						"exit 1")
				return [][]string{argv}
			},
			wantCause: "Docker",
		},
		{
			name: "the gate command is not installed",
			gate: func(t *testing.T, _ string) [][]string {
				return [][]string{{"no-such-gate-binary-archie"}}
			},
			wantCause: "could not be started",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dispatched := false
			runner := agentRunnerFunc(func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
				dispatched = true
				// Report failure so the red-stub run reaches the dispatch
				// assertion instead of a nil-tree commit after a passed result.
				return agentexec.Result{Version: agentexec.ProtocolVersion, Status: "failed"}, nil
			})
			tc := &TaskContext{
				Task:  &Task{ID: 1, Owner: "o", Repo: "r"},
				Repo:  config.Repo{Owner: "o", Name: "r", Gate: tt.gate(t, dir)},
				Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
				Agent: runner, Trees: &fakeTrees{},
				Dir: dir,
				Log: slog.New(slog.DiscardHandler),
			}

			err := StageBaselineGate().Run(context.Background(), tc)
			if err == nil {
				t.Fatal("environment-caused gate failure returned nil, want a park error")
			}
			if dispatched {
				t.Fatal("an environment-caused gate failure dispatched the baseline-fix builder")
			}
			if !strings.Contains(err.Error(), tt.wantCause) {
				t.Fatalf("park error does not name the cause %q: %v", tt.wantCause, err)
			}
		})
	}
}

// TestStageBaselineGateDispatchesBuilderForCodeFailure is acceptance (b): a
// genuine pre-existing code failure still reaches the repair agent.
func TestStageBaselineGateDispatchesBuilderForCodeFailure(t *testing.T) {
	dir := t.TempDir()
	argv := gateScript(t, dir, "echo '--- FAIL: TestBroken (0.00s)'; echo 'FAIL\tpkg/x'; exit 1")

	dispatched := false
	runner := agentRunnerFunc(func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
		dispatched = true
		return agentexec.Result{Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed}, nil
	})
	tc := &TaskContext{
		Task:  &Task{ID: 1, Owner: "o", Repo: "r"},
		Repo:  config.Repo{Owner: "o", Name: "r", Gate: [][]string{argv}},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner, Trees: &fakeTrees{},
		Dir: dir,
		Log: slog.New(slog.DiscardHandler),
	}

	if err := StageBaselineGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("StageBaselineGate.Run() = %v, want nil", err)
	}
	if !dispatched {
		t.Fatal("a code failure did not dispatch the baseline-fix builder")
	}
}

// TestStageBaselineGateProviderFailureStillReportsGateOutput is acceptance
// (c): a provider error during the fix run must not replace the gate cause --
// the park reason carries the provider failure and the gate output together.
func TestStageBaselineGateProviderFailureStillReportsGateOutput(t *testing.T) {
	dir := t.TempDir()
	argv := gateScript(t, dir,
		"echo '--- FAIL: TestBroken (0.00s)'\n"+
			"echo '    broken_test.go:5: pre-existing failure'\n"+
			"exit 1")

	runner := agentRunnerFunc(func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{}, errors.New("provider: 402 Payment Required")
	})
	tc := &TaskContext{
		Task:  &Task{ID: 1, Owner: "o", Repo: "r"},
		Repo:  config.Repo{Owner: "o", Name: "r", Gate: [][]string{argv}},
		Cfg:   config.Config{Models: map[string]string{"builder": "provider/model"}},
		Agent: runner,
		Dir:   dir,
		Log:   slog.New(slog.DiscardHandler),
	}

	err := StageBaselineGate().Run(context.Background(), tc)
	if err == nil {
		t.Fatal("provider failure during the fix run returned nil, want a park error")
	}
	if !strings.Contains(err.Error(), "402") {
		t.Fatalf("park error dropped the provider failure: %v", err)
	}
	if !strings.Contains(err.Error(), "TestBroken") {
		t.Fatalf("provider failure masked the gate output: %v", err)
	}
}
