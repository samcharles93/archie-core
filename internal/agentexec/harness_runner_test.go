package agentexec

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeHarness is a stand-in coding-agent CLI. FAKE_SCENARIO picks what it
// does to the workspace; every invocation's arguments are appended to
// FAKE_LOG, and it prints a session line and a tool line for the adapter.
const fakeHarness = `#!/bin/sh
printf '%s' "$*" | tr '\n' ' ' >> "$FAKE_LOG"
echo >> "$FAKE_LOG"
case "$FAKE_SCENARIO" in
edit) echo hello > notes.txt ;;
commit) echo x > a.txt; git add a.txt; git -c user.email=a@b.c -c user.name=a commit -qm sneaky ;;
config) git config remote.origin.url https://attacker.invalid/repo.git ;;
protected) echo broken >> widget_test.go ;;
sleep) sleep 30 & echo $! > "$FAKE_LOG.child"; wait ;;
fix-on-resume) if [ -f .attempted ]; then echo ok > fixed.txt; else touch .attempted; fi ;;
esac
echo "session=S123"
echo "tool=edit_file"
`

// lineOutput is a test adapter over fakeHarness's output lines.
type lineOutput struct {
	mu      sync.Mutex
	session string
}

func (o *lineOutput) Line(line []byte, report ToolCallReporter) {
	text := string(line)
	switch {
	case strings.HasPrefix(text, "session="):
		o.mu.Lock()
		o.session = strings.TrimPrefix(text, "session=")
		o.mu.Unlock()
	case strings.HasPrefix(text, "tool=") && report != nil:
		report(ToolCallReport{Tool: strings.TrimPrefix(text, "tool=")})
	}
}

func (o *lineOutput) SessionID() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.session
}

func (o *lineOutput) Usage() Usage { return Usage{} }

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.email=a@b.c", "-c", "user.name=a"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

type fixture struct {
	workspace string
	log       string
	req       Request
}

func newFixture(t *testing.T, scenario string) *fixture {
	t.Helper()
	ws := t.TempDir()
	runGit(t, ws, "init", "-q")
	if err := os.WriteFile(filepath.Join(ws, "widget_test.go"), []byte("package widget\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, ws, "add", ".")
	runGit(t, ws, "commit", "-qm", "base")
	runGit(t, ws, "remote", "add", "origin", "https://forge.invalid/repo.git")

	bin := filepath.Join(t.TempDir(), "fake-harness")
	if err := os.WriteFile(bin, []byte(fakeHarness), 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "invocations")
	t.Setenv("FAKE_SCENARIO", scenario)
	t.Setenv("FAKE_LOG", log)
	return &fixture{workspace: ws, log: log, req: Request{
		Version: ProtocolVersion, TaskID: 1, Attempt: 1, Stage: "implement", Model: "harness",
		Mission: "Write the notes file.",
		Budget:  Budget{WallClock: 20 * time.Second},
		Harness: &HarnessSpec{
			Launch: []string{"/bin/sh", bin},
			Prompt: []string{"-p", "{{.Prompt}}"},
			Resume: []string{"--resume", "{{.SessionID}}"},
		},
	}}
}

func (f *fixture) run(t *testing.T, ctx context.Context) (Result, []ToolCallReport) {
	t.Helper()
	var mu sync.Mutex
	var calls []ToolCallReport
	res, err := NewHarnessRunner(func() HarnessOutput { return &lineOutput{} }).Run(ctx, f.workspace, f.req, func(r ToolCallReport) {
		mu.Lock()
		calls = append(calls, r)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res, calls
}

func (f *fixture) invocations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(f.log)
	if err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func TestHarnessRunsTheMissionAndReportsChanges(t *testing.T) {
	f := newFixture(t, "edit")
	res, calls := f.run(t, t.Context())
	if res.Status != StatusPassed {
		t.Fatalf("status %q (%s), want passed", res.Status, res.StopReason)
	}
	if !slices.Equal(res.Changes, []string{"notes.txt"}) {
		t.Fatalf("changes %v, want [notes.txt]", res.Changes)
	}
	if len(calls) != 1 || calls[0].Tool != "edit_file" {
		t.Fatalf("tool calls %v, want one edit_file", calls)
	}
	inv := f.invocations(t)
	if len(inv) != 1 || !strings.HasPrefix(inv[0], "-p ") || !strings.Contains(inv[0], "Write the notes file.") {
		t.Fatalf("invocations %q, want one headless prompt carrying the mission", inv)
	}
}

func TestHarnessGuaranteesHoldWhateverTheHarnessDoes(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		setup    func(*Request)
		want     string
	}{
		{"a commit fails the step", "commit", nil, "git"},
		{"rewriting the git config fails the step", "config", nil, "git"},
		{"editing a protected path fails the step", "protected", func(r *Request) { r.Protection = Protection{Suffixes: []string{"_test.go"}} }, "widget_test.go"},
		{"writing in a read-only stage fails the step", "edit", func(r *Request) { r.ReadOnly = true }, "notes.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.scenario)
			if tt.setup != nil {
				tt.setup(&f.req)
			}
			res, _ := f.run(t, t.Context())
			if res.Status != StatusParked || !strings.Contains(res.StopReason, StopPolicy) || !strings.Contains(res.Detail, tt.want) {
				t.Fatalf("status %q stop %q detail %q; want parked on policy naming %q", res.Status, res.StopReason, res.Detail, tt.want)
			}
		})
	}
}

func TestHarnessIgnoresChangesThatPredateTheRun(t *testing.T) {
	f := newFixture(t, "edit")
	if err := os.WriteFile(filepath.Join(f.workspace, "earlier.txt"), []byte("from a previous stage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.req.ReadOnly = false
	res, _ := f.run(t, t.Context())
	if !slices.Equal(res.Changes, []string{"notes.txt"}) {
		t.Fatalf("changes %v, want only what this run changed", res.Changes)
	}
}

func TestHarnessBudgetAndCancellation(t *testing.T) {
	t.Run("the wall clock kills the harness", func(t *testing.T) {
		f := newFixture(t, "sleep")
		f.req.Budget.WallClock = 500 * time.Millisecond
		start := time.Now()
		res, _ := f.run(t, t.Context())
		if time.Since(start) > 10*time.Second {
			t.Fatalf("run took %s; the harness outlived its budget", time.Since(start))
		}
		if res.Status != StatusParked || res.StopReason != StopTimedOut {
			t.Fatalf("status %q stop %q, want parked timed_out", res.Status, res.StopReason)
		}
		assertChildDead(t, f.log+".child")
	})
	t.Run("cancelling the run kills the harness", func(t *testing.T) {
		f := newFixture(t, "sleep")
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(300*time.Millisecond, cancel)
		start := time.Now()
		_, err := NewHarnessRunner(func() HarnessOutput { return &lineOutput{} }).Run(ctx, f.workspace, f.req, nil)
		if time.Since(start) > 10*time.Second {
			t.Fatalf("cancel took %s", time.Since(start))
		}
		if err == nil || ctx.Err() == nil {
			t.Fatalf("Run after cancel returned %v, want the cancellation", err)
		}
		assertChildDead(t, f.log+".child")
	})
}

func TestHarnessGateResumesTheSession(t *testing.T) {
	gate := Gate{Commands: []Command{{Name: "fixed", Argv: []string{"test", "-f", "fixed.txt"}}}, MaxConsecutiveFailures: 3}

	t.Run("a failed gate resumes the same session with its output", func(t *testing.T) {
		f := newFixture(t, "fix-on-resume")
		f.req.Gate = gate
		res, _ := f.run(t, t.Context())
		if res.Status != StatusPassed || res.Iterations != 2 {
			t.Fatalf("status %q after %d iterations (%s), want passed after 2", res.Status, res.Iterations, res.StopReason)
		}
		inv := f.invocations(t)
		if len(inv) != 2 || !strings.HasPrefix(inv[1], "--resume S123 -p ") || !strings.Contains(inv[1], "[fixed]") {
			t.Fatalf("invocations %q, want a resume of S123 carrying the gate failure", inv)
		}
	})
	t.Run("a gate that never passes parks after the cap", func(t *testing.T) {
		f := newFixture(t, "edit")
		f.req.Gate = gate
		res, _ := f.run(t, t.Context())
		if res.Status != StatusParked || res.Iterations != 3 {
			t.Fatalf("status %q after %d iterations, want parked after 3", res.Status, res.Iterations)
		}
	})
	t.Run("without a resume verb there is one iteration", func(t *testing.T) {
		f := newFixture(t, "fix-on-resume")
		f.req.Gate = gate
		f.req.Harness.Resume = nil
		res, _ := f.run(t, t.Context())
		if res.Status != StatusParked || res.Iterations != 1 || len(f.invocations(t)) != 1 {
			t.Fatalf("status %q after %d iterations and %d invocations, want parked after 1", res.Status, res.Iterations, len(f.invocations(t)))
		}
	})
}

func TestHarnessRequestValidation(t *testing.T) {
	f := newFixture(t, "edit")
	f.req.Harness.Prompt = []string{"-p"}
	if _, err := NewHarnessRunner(nil).Run(t.Context(), f.workspace, f.req, nil); err == nil {
		t.Fatal("a prompt verb with no {{.Prompt}} placeholder ran; the mission would be silently dropped")
	}
	f.req.Harness = nil
	if _, err := NewHarnessRunner(nil).Run(t.Context(), f.workspace, f.req, nil); err == nil {
		t.Fatal("a request without a harness spec ran on the harness runner")
	}
}

// assertChildDead checks the harness's own child process did not outlive
// it: killing only the CLI would leave its subprocesses running.
func assertChildDead(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the harness recorded no child: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("harness child %d outlived the step", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
