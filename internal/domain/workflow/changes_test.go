package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/logging"
)

// recordingStore is the narrow Store the engine calls mid-run, keeping the
// events it was asked to persist. A capture is durable provenance, so the
// assertions below read this and not the lossy bus.
type recordingStore struct {
	events      []events.Event
	transitions []transition
	insertEr    error
}

// transition is one status change the engine asked the store for. Parking is
// observable only through these, which is what makes "a reporting failure
// never parks the task" a test rather than a reading of the code.
type transition struct{ from, to, detail string }

func (s *recordingStore) Update(context.Context, *Task) error { return nil }

func (s *recordingStore) Transition(_ context.Context, _ int64, from, to, detail string) error {
	s.transitions = append(s.transitions, transition{from: from, to: to, detail: detail})
	return nil
}

func (s *recordingStore) InsertEvent(_ context.Context, e events.Event) (int64, error) {
	if s.insertEr != nil {
		return 0, s.insertEr
	}
	s.events = append(s.events, e)
	return int64(len(s.events)), nil
}

func (s *recordingStore) ofKind(kind string) []events.Event {
	var out []events.Event
	for _, e := range s.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// capturingTrees is a Trees implementation that can also report what changed.
// fakeTrees deliberately stays without the capability, so every existing test
// that drives a stage through it exercises the degrade path.
type capturingTrees struct {
	fakeTrees
	stats task.ChangeStats
	err   error
	calls int
	dir   string
	base  string
}

func (c *capturingTrees) ChangedFileStats(_ context.Context, dir, base string) (task.ChangeStats, error) {
	c.calls++
	c.dir, c.base = dir, base
	return c.stats, c.err
}

var (
	_ Trees             = (*fakeTrees)(nil)
	_ Trees             = (*capturingTrees)(nil)
	_ changeStatsReader = (*capturingTrees)(nil)
	_ task.Store        = (*recordingStore)(nil)
)

// capturePayload mirrors the frozen persisted shape of a changes_captured
// event. Decoding the event's data through it is what proves the field names
// a reader depends on.
type capturePayload struct {
	Schema        string            `json:"schema"`
	Owner         string            `json:"owner"`
	Repo          string            `json:"repo"`
	Base          string            `json:"base"`
	Branch        string            `json:"branch"`
	HeadSHA       string            `json:"head_sha"`
	BaseSHA       string            `json:"base_sha"`
	PRNumber      int               `json:"pr_number"`
	CapturedAfter string            `json:"captured_after"`
	Files         []task.FileChange `json:"files"`
	Totals        task.ChangeTotals `json:"totals"`
	Truncated     bool              `json:"truncated"`
}

func decodeCapture(t *testing.T, e events.Event) capturePayload {
	t.Helper()
	raw, err := json.Marshal(e.Data)
	if err != nil {
		t.Fatalf("marshal capture data: %v", err)
	}
	var payload capturePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode capture data: %v", err)
	}
	return payload
}

func sampleStats() task.ChangeStats {
	return task.ChangeStats{
		BaseSHA: strings.Repeat("a", 40),
		HeadSHA: strings.Repeat("b", 40),
		Files: []task.FileChange{
			{Path: "internal/x.go", Status: task.ChangeModified, Additions: 12, Deletions: 3},
			{Path: "docs/y.md", OldPath: "docs/z.md", Status: task.ChangeRenamed},
		},
		Totals: task.ChangeTotals{Files: 2, Additions: 12, Deletions: 3},
	}
}

func captureTaskContext(t *testing.T, trees Trees, store Store) *TaskContext {
	t.Helper()
	return &TaskContext{
		Task: &Task{
			ID: 11, Attempt: 2, Owner: "acme", Repo: "widget", IssueNumber: 7,
			Stage: "commit-push", Branch: "feat/7-widget",
		},
		Repo:   config.Repo{Owner: "acme", Name: "widget", Base: "main"},
		Trees:  trees,
		Store:  store,
		Dir:    "/worktree",
		Branch: "feat/7-widget",
		Log:    slog.New(slog.DiscardHandler),
	}
}

// TestRunStampsTheAttemptOnEveryEventItEmits is R2's producer half. The
// attempt is read from the task record the run holds, so an event
// is attributable to one attempt without segmenting the stream by stage order,
// and nothing here guesses an attempt it does not have.
func TestRunStampsTheAttemptOnEveryEventItEmits(t *testing.T) {
	bus := events.NewBus()
	sub := bus.Subscribe(32)
	t.Cleanup(sub.Close)

	tc := &TaskContext{
		Task:  &Task{ID: 4, Attempt: 3, Owner: "o", Repo: "r", IssueNumber: 1, Status: StatusRunning},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Store: &recordingStore{},
		Bus:   bus,
		Log:   slog.New(slog.DiscardHandler),
	}
	Run(context.Background(), Workflow{Name: "w", Stages: []Stage{{Name: "build", Run: func(context.Context, *TaskContext) error {
		return fmt.Errorf("boom")
	}}}}, tc)

	var emitted []events.Event
	for {
		select {
		case e := <-sub.C:
			emitted = append(emitted, e)
		default:
			if len(emitted) == 0 {
				t.Fatal("run emitted no events at all; the assertion below would be vacuous")
			}
			for _, e := range emitted {
				if e.Attempt != 3 {
					t.Errorf("event %s attempt = %d, want 3", e.Kind, e.Attempt)
				}
			}
			return
		}
	}
}

// TestRunBindsTheStageOntoTheTaskLoggerOnlyForTheStage is R5's producer half:
// a line a stage's own code writes must be selectable by stage, and a line
// written outside any stage must not be attributed to one.
//
// It asserts against the logging entry the reader actually filters on
// (internal/logging.Query.Stage matches Entry.Fields["stage"]) rather than
// against a rendered line, so a change in the handler cannot make this pass
// while the filter stays broken.
func TestRunBindsTheStageOntoTheTaskLoggerOnlyForTheStage(t *testing.T) {
	feed := logging.NewFeed(32)
	outer := slog.New(logging.NewFeedHandler(slog.NewJSONHandler(discardSink{}, nil), feed))
	tc := &TaskContext{
		Task:  &Task{ID: 4, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1, Status: StatusRunning},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Store: &recordingStore{},
		Log:   outer,
	}
	Run(context.Background(), Workflow{Name: "w", Stages: []Stage{{Name: "build", Run: func(_ context.Context, tc *TaskContext) error {
		tc.Log.Info("inside the stage")
		return nil
	}}}}, tc)

	stages := map[string]string{}
	for _, entry := range feed.Snapshot() {
		stage, _ := entry.Fields["stage"].(string)
		stages[entry.Message] = stage
	}
	starting, ok := stages["stage starting"]
	if !ok {
		t.Fatalf("no stage-start line was logged at all: %v", stages)
	}
	if starting != "build" {
		t.Errorf("stage starting carries stage %q, want build", starting)
	}
	if got := stages["inside the stage"]; got != "build" {
		t.Errorf("a line logged by the stage's own code carries stage %q, want build", got)
	}

	// Restored afterwards: a line the engine writes outside a stage is not
	// attributable to one, and claiming it were would make a stage filter lie.
	if tc.Log != outer {
		t.Error("tc.Log was not restored after the stage; the next stage's lines would inherit a stale stage")
	}
	outer.Info("after the run")
	entries := feed.Snapshot()
	last := entries[len(entries)-1]
	if last.Message != "after the run" {
		t.Fatalf("last entry = %q, want the line logged after the run", last.Message)
	}
	if stage, ok := last.Fields["stage"]; ok {
		t.Errorf("a line logged after the run carries stage %v, want none", stage)
	}
}

// discardSink absorbs what the wrapped JSON handler renders, so this test's
// assertions read the feed and never a terminal. It cannot be io.Discard or
// slog.DiscardHandler: FeedHandler.Enabled delegates to the wrapped handler,
// and slog.DiscardHandler reports every record as disabled, which leaves the
// feed empty and the assertions below vacuous.
type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return len(p), nil }

func TestStageCommitCapturesTheChangedFiles(t *testing.T) {
	trees := &capturingTrees{commitAllChanged: true, stats: sampleStats()}
	store := &recordingStore{}
	tc := captureTaskContext(t, trees, store)
	tc.Task.Stage = "commit-repro"

	if err := StageCommit("commit-repro", func(*TaskContext) string { return "msg" }).Run(context.Background(), tc); err != nil {
		t.Fatalf("StageCommit.Run() = %v, want nil", err)
	}

	captures := store.ofKind(events.KindChangesCaptured)
	if len(captures) != 1 {
		t.Fatalf("changes_captured events = %d, want exactly 1", len(captures))
	}
	got := captures[0]
	if got.Attempt != 2 {
		t.Errorf("capture attempt = %d, want the task's own attempt 2", got.Attempt)
	}
	if got.Stage != "commit-repro" {
		t.Errorf("capture stage = %q, want the capturing stage", got.Stage)
	}
	if got.TaskID != tc.Task.ID || got.Repo != "acme/widget" || got.Workflow != tc.Task.Workflow {
		t.Errorf("capture identity = (%d, %q, %q), want the task's", got.TaskID, got.Repo, got.Workflow)
	}
	if trees.dir != tc.Dir || trees.base != "main" {
		t.Errorf("diffstat read (%q, %q), want the worktree and the repo's base branch", trees.dir, trees.base)
	}

	payload := decodeCapture(t, got)
	if payload.Schema != events.ChangesCapturedSchema {
		t.Errorf("payload schema = %q, want %q: a reader refuses a capture it cannot name",
			payload.Schema, events.ChangesCapturedSchema)
	}
	if payload.Owner != "acme" || payload.Repo != "widget" || payload.Base != "main" || payload.Branch != "feat/7-widget" {
		t.Errorf("payload coordinates = %+v", payload)
	}
	if payload.CapturedAfter != capturedAfterCommit {
		t.Errorf("captured_after = %q, want %q", payload.CapturedAfter, capturedAfterCommit)
	}
	if payload.HeadSHA != strings.Repeat("b", 40) || payload.BaseSHA != strings.Repeat("a", 40) {
		t.Errorf("payload SHAs = (%q, %q), want the measured pair", payload.BaseSHA, payload.HeadSHA)
	}
	if len(payload.Files) != 2 || payload.Files[0].Path != "internal/x.go" || payload.Files[1].OldPath != "docs/z.md" {
		t.Errorf("payload files = %+v", payload.Files)
	}
	if payload.Totals != (task.ChangeTotals{Files: 2, Additions: 12, Deletions: 3}) {
		t.Errorf("payload totals = %+v", payload.Totals)
	}
}

// TestStageCommitPushCapturesOnlyAfterThePush pins the second capture point:
// a push that failed is not a change that landed, and a capture claiming one
// would be worse than no capture.
func TestStageCommitPushCapturesOnlyAfterThePush(t *testing.T) {
	tests := []struct {
		name       string
		pushShares bool
	}{
		{name: "after a successful push", pushShares: true},
		{name: "not after a failed push", pushShares: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trees := &capturingTrees{commitAllChanged: true, stats: sampleStats()}
			if !tt.pushShares {
				trees.pushErr = fmt.Errorf("push refused")
			}
			store := &recordingStore{}
			tc := captureTaskContext(t, trees, store)

			err := StageCommitPush(func(*TaskContext) string { return "msg" }).Run(context.Background(), tc)
			captures := store.ofKind(events.KindChangesCaptured)
			if !tt.pushShares {
				if err == nil {
					t.Fatal("StageCommitPush.Run() = nil on a failed push, want the push error")
				}
				if len(captures) != 0 {
					t.Fatalf("captures = %d after a failed push, want none", len(captures))
				}
				return
			}
			if err != nil {
				t.Fatalf("StageCommitPush.Run() = %v, want nil", err)
			}
			if len(captures) != 1 {
				t.Fatalf("changes_captured events = %d, want exactly 1", len(captures))
			}
			if got := decodeCapture(t, captures[0]).CapturedAfter; got != capturedAfterCommitPush {
				t.Errorf("captured_after = %q, want %q", got, capturedAfterCommitPush)
			}
		})
	}
}

// TestChangeCaptureDegradesWithoutTheCapability: the capture is an optional
// capability, so a Trees implementation that cannot report a diffstat must
// lose the record and nothing else. fakeTrees is exactly that implementation.
func TestChangeCaptureDegradesWithoutTheCapability(t *testing.T) {
	trees := &fakeTrees{commitAllChanged: true}
	store := &recordingStore{}
	tc := captureTaskContext(t, trees, store)

	if err := StageCommit("commit", func(*TaskContext) string { return "msg" }).Run(context.Background(), tc); err != nil {
		t.Fatalf("StageCommit.Run() = %v, want the stage to succeed without a capture", err)
	}
	if got := len(store.ofKind(events.KindChangesCaptured)); got != 0 {
		t.Errorf("changes_captured events = %d, want none", got)
	}
}

// TestChangeCaptureFailureNeverFailsTheStage: reporting is not the work. A
// worktree that cannot be measured, and a store that rejects the write, must
// both leave the stage's own result untouched.
func TestChangeCaptureFailureNeverFailsTheStage(t *testing.T) {
	tests := []struct {
		name   string
		trees  Trees
		store  Store
		reason string
	}{
		{
			name:   "the diffstat could not be read",
			trees:  &capturingTrees{commitAllChanged: true, err: fmt.Errorf("worktree vanished")},
			store:  &recordingStore{},
			reason: "a worktree the daemon already deleted",
		},
		{
			name:   "the capture could not be persisted",
			trees:  &capturingTrees{commitAllChanged: true, stats: sampleStats()},
			store:  &recordingStore{insertEr: fmt.Errorf("store unavailable")},
			reason: "a store that refused the row",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := captureTaskContext(t, tt.trees, tt.store)
			if err := StageCommit("commit", func(*TaskContext) string { return "msg" }).Run(context.Background(), tc); err != nil {
				t.Fatalf("StageCommit.Run() = %v, want nil despite %s", err, tt.reason)
			}
		})
	}
}

// TestOpenPRCapturesThePullRequestNumber is R3's link requirement at the
// producer. The captures taken at commit and push time run before the PR
// exists, so their pr_number is necessarily 0; OpenPR captures once more as
// soon as the number is recorded, which is what lets the changed-files view
// link the pull request.
func TestOpenPRCapturesThePullRequestNumber(t *testing.T) {
	trees := &capturingTrees{stats: sampleStats()}
	store := &recordingStore{}
	forgeClient := &fakeForge{}
	tc := captureTaskContext(t, trees, store)
	tc.Forge = forgeClient
	tc.Task.Stage = "open-pr"
	tc.Task.Title = "widget fails"

	if err := OpenPR(context.Background(), tc, "body"); err != nil {
		t.Fatalf("OpenPR() = %v, want nil", err)
	}
	if tc.Task.PRNumber == 0 {
		t.Fatal("OpenPR recorded no PR number; the assertion below would be vacuous")
	}

	captures := store.ofKind(events.KindChangesCaptured)
	if len(captures) != 1 {
		t.Fatalf("changes_captured events = %d, want the capture OpenPR records once the PR number exists", len(captures))
	}
	payload := decodeCapture(t, captures[0])
	if payload.CapturedAfter != capturedAfterOpenPR {
		t.Errorf("captured_after = %q, want %q", payload.CapturedAfter, capturedAfterOpenPR)
	}
	if payload.PRNumber != tc.Task.PRNumber {
		t.Errorf("pr_number = %d, want the number OpenPR recorded (%d)", payload.PRNumber, tc.Task.PRNumber)
	}
	if captures[0].Stage != "open-pr" {
		t.Errorf("capture stage = %q, want the capturing stage", captures[0].Stage)
	}
}

// TestChangeCaptureTruncatesTheFileListButNotTheTotals: one large refactor
// must not write an unbounded payload, and a capture that dropped entries must
// still report how much the attempt actually changed.
func TestChangeCaptureTruncatesTheFileListButNotTheTotals(t *testing.T) {
	files := make([]task.FileChange, 0, task.MaxCapturedFiles+5)
	for i := range task.MaxCapturedFiles + 5 {
		files = append(files, task.FileChange{
			Path: fmt.Sprintf("file-%03d.go", i), Status: task.ChangeAdded, Additions: 1,
		})
	}
	stats := task.ChangeStats{
		Files:  files,
		Totals: task.ChangeTotals{Files: len(files), Additions: len(files)},
	}
	trees := &capturingTrees{commitAllChanged: true, stats: stats}
	store := &recordingStore{}
	tc := captureTaskContext(t, trees, store)

	if err := StageCommit("commit", func(*TaskContext) string { return "msg" }).Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	captures := store.ofKind(events.KindChangesCaptured)
	if len(captures) != 1 {
		t.Fatalf("changes_captured events = %d, want 1", len(captures))
	}
	payload := decodeCapture(t, captures[0])
	if len(payload.Files) != task.MaxCapturedFiles {
		t.Errorf("payload carries %d files, want the cap of %d", len(payload.Files), task.MaxCapturedFiles)
	}
	if !payload.Truncated {
		t.Error("truncated = false, want true when entries were dropped")
	}
	if payload.Totals != (task.ChangeTotals{Files: len(files), Additions: len(files)}) {
		t.Errorf("totals = %+v, want the full set %d", payload.Totals, len(files))
	}
	if payload.Totals.Files == len(payload.Files) {
		t.Error("totals were recomputed over the truncated list; a reader would understate the change")
	}
}

// TestBaselineFixCapturesItsOwnCommit pins the third capture point: the
// baseline repair is a real, gate-verified commit of its own, and it is the
// only record of that intermediate state once the later push capture folds it
// into one bigger change.
func TestBaselineFixCapturesItsOwnCommit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gate.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := agentRunnerFunc(func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed}, nil
	})

	tests := []struct {
		name    string
		changed bool
		want    int
	}{
		{name: "a real fix is captured", changed: true, want: 1},
		{name: "a commit that produced nothing is not", changed: false, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trees := &capturingTrees{commitAllChanged: tt.changed, stats: sampleStats()}
			store := &recordingStore{}
			tc := &TaskContext{
				Task:   &Task{ID: 9, Attempt: 1, Owner: "o", Repo: "r", Status: StatusRunning},
				Repo:   config.Repo{Owner: "o", Name: "r", Gate: [][]string{{"sh", script}}},
				Cfg:    config.Config{Models: map[string]string{"builder": "provider/model"}},
				Agent:  runner,
				Trees:  trees,
				Store:  store,
				Dir:    dir,
				Branch: "feat/9-x",
				Log:    slog.New(slog.DiscardHandler),
			}
			if err := StageBaselineGate().Run(context.Background(), tc); err != nil {
				t.Fatalf("StageBaselineGate.Run() = %v", err)
			}
			captures := store.ofKind(events.KindChangesCaptured)
			if len(captures) != tt.want {
				t.Fatalf("changes_captured events = %d, want %d", len(captures), tt.want)
			}
			if tt.want == 0 {
				return
			}
			if got := decodeCapture(t, captures[0]).CapturedAfter; got != capturedAfterBaselineFix {
				t.Errorf("captured_after = %q, want %q", got, capturedAfterBaselineFix)
			}
			if !tc.BaselineFixed {
				t.Error("BaselineFixed = false after a captured baseline commit")
			}
		})
	}
}

// TestCaptureIsNeverCallableFromTrees makes the constraint explicit in code,
// next to the type that must not grow past it: the capture is an optional,
// unexported capability, and Trees -- the surface interpreted stage code is
// handed -- must not name it. wfextract's reachability test proves the same
// thing from the interpreted side.
func TestCaptureIsNeverCallableFromTrees(t *testing.T) {
	if _, ok := any((*fakeTrees)(nil)).(changeStatsReader); ok {
		t.Error("fakeTrees satisfies the capture capability; the degrade test above would be vacuous")
	}
	if _, ok := any(Trees(nil)).(changeStatsReader); ok {
		t.Error("Trees names the capture capability; interpreted stage code could call it")
	}
}

// TestAFailedCaptureNeverParksTheTask runs the whole engine, not just the
// step: the only way a capture failure could reach the operator as a parked
// task is through Run's own error path, so the assertion has to be made
// against Run's transitions.
func TestAFailedCaptureNeverParksTheTask(t *testing.T) {
	tests := []struct {
		name  string
		trees Trees
	}{
		{
			name:  "the diffstat could not be read",
			trees: &capturingTrees{commitAllChanged: true, err: fmt.Errorf("worktree vanished")},
		},
		{
			name:  "the capture could not be persisted",
			trees: &capturingTrees{commitAllChanged: true, stats: sampleStats()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &recordingStore{}
			if tt.name == "the capture could not be persisted" {
				// Only the capture's own insert fails; every other write the
				// engine makes still has to succeed for the run to finish.
				store = &recordingStore{insertEr: fmt.Errorf("store unavailable")}
			}
			tc := captureTaskContext(t, tt.trees, store)
			tc.Task.Status = StatusRunning

			Run(context.Background(), Workflow{Name: "implement", Stages: []Stage{
				StageCommitPush(func(*TaskContext) string { return "msg" }),
				{Name: "open-pr", Run: func(_ context.Context, tc *TaskContext) error {
					tc.Outcome = Outcome{Status: StatusPROpen, Detail: "PR #1"}
					return nil
				}},
			}}, tc)

			for _, tr := range store.transitions {
				if tr.to == StatusParked {
					t.Fatalf("task parked (%s -> %s: %s); a change capture must never park a run that succeeded",
						tr.from, tr.to, tr.detail)
				}
			}
			if tc.Outcome.Status != StatusPROpen {
				t.Errorf("outcome = %q, want the stage's own %q", tc.Outcome.Status, StatusPROpen)
			}
		})
	}
}
