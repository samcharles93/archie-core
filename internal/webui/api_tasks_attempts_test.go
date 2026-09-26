package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// stepWriter is StepRecorder, asserted against srv.Store here rather than
// widened onto storecontract.TaskStore: the dashboard never writes a step
// itself, only reads them (Server.Steps, storecontract.StepReader), and this
// test needs real rows to exercise the read against.
type stepWriter interface {
	StartStep(ctx context.Context, s task.StepStart) (int64, events.Event, error)
	FinishStep(ctx context.Context, s task.StepFinish) (events.Event, error)
}

// railBase is the fixed clock the rail fixtures hang off, so every timestamp
// and duration asserted below is a literal rather than a tolerance.
var railBase = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

// attemptEvent stands in for the events that still drive attempt bounds and
// counts (attemptsFromEvents), independent of the stage rail those events
// used to also back.
func attemptEvent(taskID int64, attempt int, at time.Time) events.Event {
	return events.Event{Kind: events.KindAgentFinish, TaskID: taskID, Attempt: attempt, At: at}
}

func insertTaskEvent(t *testing.T, srv *Server, ev events.Event) {
	t.Helper()
	if _, err := srv.Store.InsertEvent(t.Context(), ev); err != nil {
		t.Fatalf("insert %s event: %v", ev.Kind, err)
	}
}

// stageStep is a fixture StepExecution the pure-function test builds by
// hand -- no store round trip, since stageViewsFromSteps operates on
// task.StepExecution values directly.
func stageStep(name string, attempt int, status taskstate.StepStatus, startedAt, finishedAt time.Time, detail string) task.StepExecution {
	return task.StepExecution{
		Attempt: attempt, Kind: task.StepKindStage, Name: name, Status: status,
		StartedAt: startedAt, FinishedAt: finishedAt, Detail: detail,
	}
}

// getTaskAPI issues one task-detail request through the real mux. A route that
// is not registered falls through to the SPA asset handler and answers 200 with
// an HTML page, so using the mux makes a missing registration fail the shape
// assertions below rather than passing quietly.
func getTaskAPI(t *testing.T, srv *Server, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func taskRoute(id int64, suffix string) string {
	return "/api/tasks/" + strconv.FormatInt(id, 10) + suffix
}

// twoAttemptTask drives one task through the lifecycle the rail has to read:
// claim (attempt 1), park, retry, claim (attempt 2), leaving the task running
// on its second attempt. No test writes an attempt number directly, so the
// attempt under test is the one the real lifecycle produced.
func twoAttemptTask(t *testing.T, srv *Server) *workflow.Task {
	t.Helper()
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	first, err := srv.Store.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("ClaimNext returned no task")
	}
	if first.Attempt != 1 {
		t.Fatalf("first claim attempt = %d, want 1", first.Attempt)
	}
	if err := srv.Store.Transition(ctx, first.ID, workflow.StatusRunning, workflow.StatusParked, "stage implement: builder exited 1"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.RetryTask(ctx, first.ID, workflow.StatusParked, ""); err != nil {
		t.Fatal(err)
	}
	second, err := srv.Store.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatal("second claim returned no task")
	}
	if second.Attempt != 2 || second.Status != workflow.StatusRunning {
		t.Fatalf("second claim = attempt %d status %q, want attempt 2 running", second.Attempt, second.Status)
	}
	return second
}

// summarizeAttemptRail renders a folded rail as one line per attempt so a table
// case states the derivation it expects without a struct literal per stage:
// "attempt:status[name:status:duration[#error][@nostart],...] duration".
func durationToken(ms *int64) string {
	if ms == nil {
		return "?"
	}
	return strconv.FormatInt(*ms, 10)
}

// summarizeStages renders a stage list the same way summarizeAttemptRail's
// inner loop does, for a test that exercises stageViewsFromSteps alone.
func summarizeStages(stages []taskStageView) string {
	tokens := make([]string, 0, len(stages))
	for _, s := range stages {
		token := s.Name + ":" + s.Status + ":" + durationToken(s.DurationMS)
		if s.StartedAt == nil {
			token += "@nostart"
		}
		if s.Error != "" {
			token += "#" + s.Error
		}
		tokens = append(tokens, token)
	}
	return strings.Join(tokens, ",")
}

// TestStageViewsFromStepsDerivesEveryRailRule is the table behind R1: every
// status rule the rail depends on, sourced from StepExecutions -- the
// authoritative record (docs/prds/execution-tree-state-machine.md) -- rather
// than a fold over stage_start/stage_finish events.
func TestStageViewsFromStepsDerivesEveryRailRule(t *testing.T) {
	at := railBase.Add

	tests := []struct {
		name     string
		steps    []task.StepExecution
		inFlight bool
		want     string
	}{
		{
			name:  "a succeeded step is ok and reports its measured span",
			steps: []task.StepExecution{stageStep("prepare", 1, taskstate.StepSucceeded, at(0), at(time.Second), "")},
			want:  "prepare:ok:1000",
		},
		{
			name:  "a failed step fails and carries its detail as the error",
			steps: []task.StepExecution{stageStep("implement", 1, taskstate.StepFailed, at(0), at(2*time.Second), "builder exited 1")},
			want:  "implement:failed:2000#builder exited 1",
		},
		{
			name:  "a cancelled step reads interrupted",
			steps: []task.StepExecution{stageStep("implement", 1, taskstate.StepCancelled, at(0), at(time.Second), "stopped by operator")},
			want:  "implement:interrupted:1000",
		},
		{
			name:  "an interrupted step reads interrupted",
			steps: []task.StepExecution{stageStep("implement", 1, taskstate.StepInterrupted, at(0), at(time.Second), "")},
			want:  "implement:interrupted:1000",
		},
		{
			name:     "a running step on the task's current in-flight attempt is running",
			steps:    []task.StepExecution{stageStep("implement", 2, taskstate.StepRunning, at(0), time.Time{}, "")},
			inFlight: true,
			want:     "implement:running:?",
		},
		{
			name:     "a running step past the task's live attempt is stale, read as interrupted",
			steps:    []task.StepExecution{stageStep("implement", 1, taskstate.StepRunning, at(0), time.Time{}, "")},
			inFlight: false,
			want:     "implement:interrupted:?",
		},
		{
			name:  "a pending step (never observed in practice) reads unknown rather than a false verdict",
			steps: []task.StepExecution{stageStep("implement", 1, taskstate.StepPending, time.Time{}, time.Time{}, "")},
			want:  "implement:unknown:?@nostart",
		},
		{
			name: "a repeated stage name yields one rail entry per occurrence",
			steps: []task.StepExecution{
				stageStep("implement", 1, taskstate.StepSucceeded, at(0), at(time.Second), ""),
				{Attempt: 1, Kind: task.StepKindStage, Name: "implement", Status: taskstate.StepRunning, StartedAt: at(2 * time.Second)},
			},
			inFlight: true,
			want:     "implement:ok:1000,implement:running:?",
		},
		{
			name: "an agent or call step is not part of the stage rail",
			steps: []task.StepExecution{
				stageStep("implement", 1, taskstate.StepSucceeded, at(0), at(time.Second), ""),
				{Attempt: 1, Kind: task.StepKindAgent, Name: "implement", Status: taskstate.StepSucceeded, StartedAt: at(0), FinishedAt: at(time.Second)},
				{Attempt: 1, Kind: task.StepKindCall, Name: "callee", Status: taskstate.StepSucceeded, StartedAt: at(0), FinishedAt: at(time.Second)},
			},
			want: "implement:ok:1000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeStages(stageViewsFromSteps(tt.steps, tt.inFlight)); got != tt.want {
				t.Errorf("stages = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAttemptsFromEventsIndexesAttemptsFromEventsAndUnattributedCount pins
// what attemptsFromEvents still owns now that stage derivation moved to
// steps: which attempt numbers exist, and the unattributed-event count --
// both read from the task's events, since an attempt can carry events (agent
// calls, say) with no recorded step at all.
func TestAttemptsFromEventsIndexesAttemptsFromEventsAndUnattributedCount(t *testing.T) {
	at := railBase.Add

	tests := []struct {
		name             string
		taskStatus       string
		currentAttempt   int
		evs              []events.Event
		steps            []task.StepExecution
		wantAttempts     []int
		wantUnattributed int
	}{
		{
			name:       "a later attempt does not absorb the earlier attempt's steps",
			taskStatus: workflow.StatusRunning, currentAttempt: 2,
			evs:          []events.Event{attemptEvent(1, 1, at(0)), attemptEvent(1, 2, at(time.Second))},
			steps:        []task.StepExecution{stageStep("prepare", 1, taskstate.StepSucceeded, at(0), at(time.Second), "")},
			wantAttempts: []int{1, 2},
		},
		{
			name:       "events carrying no attempt are counted, never grouped",
			taskStatus: workflow.StatusQueued, currentAttempt: 0,
			evs:              []events.Event{{Kind: events.KindAgentFinish, TaskID: 1, At: at(0)}, {Kind: events.KindAgentFinish, TaskID: 1, At: at(time.Second)}},
			wantUnattributed: 2,
		},
		{
			name:       "a task with no events has no attempts and nothing unattributed",
			taskStatus: workflow.StatusQueued, currentAttempt: 0,
		},
		{
			name:       "an attempt with events but no recorded step still appears, with no stages",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs:          []events.Event{attemptEvent(1, 1, at(0))},
			wantAttempts: []int{1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts, unattributed := attemptsFromEvents(tt.evs, tt.steps, tt.taskStatus, tt.currentAttempt)
			var got []int
			for _, a := range attempts {
				got = append(got, a.Attempt)
			}
			if len(got) != len(tt.wantAttempts) {
				t.Fatalf("attempts = %v, want %v", got, tt.wantAttempts)
			}
			for i, want := range tt.wantAttempts {
				if got[i] != want {
					t.Errorf("attempts = %v, want %v", got, tt.wantAttempts)
				}
			}
			if unattributed != tt.wantUnattributed {
				t.Errorf("unattributed = %d, want %d", unattributed, tt.wantUnattributed)
			}
		})
	}
}

// TestHandleTaskAttemptsRendersBothAttemptsOfARetry pins the wire shape: an
// attempt's ordered stages with name, start time, duration, terminal status and
// error text, the task's current attempt, and attempt 1's rail excluding
// attempt 2's stages.
func TestHandleTaskAttemptsRendersBothAttemptsOfARetry(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	steps, ok := srv.Store.(stepWriter)
	if !ok {
		t.Fatal("test store does not implement stepWriter")
	}

	// Not twoAttemptTask: StartStep guards on the execution's own current
	// attempt (a step named for a superseded attempt is a stale transition
	// by design -- steps.go, "a caller naming one the execution no longer
	// runs is describing a run that is over"), so attempt 1's steps have to
	// be recorded while the task is actually on attempt 1, before the retry
	// that moves it to attempt 2.
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	claimed1, err := srv.Store.ClaimNext(ctx)
	if err != nil || claimed1 == nil || claimed1.Attempt != 1 {
		t.Fatalf("ClaimNext = (%+v, %v), want attempt 1", claimed1, err)
	}

	prepareID, _, err := steps.StartStep(ctx, task.StepStart{ExecutionID: claimed1.ID, Attempt: 1, Kind: task.StepKindStage, Name: "prepare"})
	if err != nil {
		t.Fatalf("StartStep prepare: %v", err)
	}
	if _, err := steps.FinishStep(ctx, task.StepFinish{
		StepID: prepareID, ExecutionID: claimed1.ID, From: taskstate.StepRunning, To: taskstate.StepSucceeded,
	}); err != nil {
		t.Fatalf("FinishStep prepare: %v", err)
	}
	implementID, _, err := steps.StartStep(ctx, task.StepStart{ExecutionID: claimed1.ID, Attempt: 1, Kind: task.StepKindStage, Name: "implement"})
	if err != nil {
		t.Fatalf("StartStep implement: %v", err)
	}
	if _, err := steps.FinishStep(ctx, task.StepFinish{
		StepID: implementID, ExecutionID: claimed1.ID, From: taskstate.StepRunning, To: taskstate.StepFailed,
		Detail: "stage implement: builder exited 1",
	}); err != nil {
		t.Fatalf("FinishStep implement: %v", err)
	}

	if err := srv.Store.Transition(ctx, claimed1.ID, workflow.StatusRunning, workflow.StatusParked, "stage implement: builder exited 1"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.RetryTask(ctx, claimed1.ID, workflow.StatusParked, ""); err != nil {
		t.Fatal(err)
	}
	current, err := srv.Store.ClaimNext(ctx)
	if err != nil || current == nil || current.Attempt != 2 {
		t.Fatalf("second ClaimNext = (%+v, %v), want attempt 2", current, err)
	}
	if _, _, err := steps.StartStep(ctx, task.StepStart{ExecutionID: current.ID, Attempt: 2, Kind: task.StepKindStage, Name: "implement"}); err != nil {
		t.Fatalf("StartStep attempt 2 implement: %v", err)
	}

	// No hand-inserted events: StartStep/FinishStep already write
	// stage_start/stage_finish events in the same transaction as the step
	// row (steps.go), which is what attemptBounds and event_count read --
	// attempt 1 gets exactly the 4 those two StartStep+FinishStep pairs
	// produce, attempt 2 the 1 its lone StartStep produces. Their
	// timestamps are the store's own clock, so bounds are checked for
	// presence and order below rather than against a literal railBase time.
	w := getTaskAPI(t, srv, taskRoute(current.ID, "/attempts"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	if raw := w.Body.String(); !strings.Contains(raw, `"error":""`) {
		t.Errorf("body = %s, want the ok stage's error field present and empty rather than omitted", raw)
	}

	var body struct {
		TaskID             int64 `json:"task_id"`
		CurrentAttempt     int   `json:"current_attempt"`
		UnattributedEvents int   `json:"unattributed_events"`
		Attempts           []struct {
			Attempt    int        `json:"attempt"`
			Status     string     `json:"status"`
			StartedAt  *time.Time `json:"started_at"`
			FinishedAt *time.Time `json:"finished_at"`
			DurationMS *int64     `json:"duration_ms"`
			EventCount int        `json:"event_count"`
			Stages     []struct {
				Name       string     `json:"name"`
				Seq        int        `json:"seq"`
				Status     string     `json:"status"`
				StartedAt  *time.Time `json:"started_at"`
				DurationMS *int64     `json:"duration_ms"`
				Error      string     `json:"error"`
			} `json:"stages"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body)
	}
	if body.TaskID != current.ID || body.CurrentAttempt != 2 || body.UnattributedEvents != 0 {
		t.Fatalf("envelope = task %d, current attempt %d, unattributed %d; want %d, 2, 0",
			body.TaskID, body.CurrentAttempt, body.UnattributedEvents, current.ID)
	}
	if len(body.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (%s)", len(body.Attempts), w.Body)
	}

	first := body.Attempts[0]
	if first.Attempt != 1 || first.Status != "failed" {
		t.Errorf("attempt 1 = %d/%q, want 1/failed", first.Attempt, first.Status)
	}
	if first.EventCount != 4 {
		t.Errorf("attempt 1 event_count = %d, want 4", first.EventCount)
	}
	// StartStep/FinishStep stamp their events with the store's own clock, so
	// only presence, order and non-negativity are checked here.
	if first.StartedAt == nil || first.FinishedAt == nil || first.FinishedAt.Before(*first.StartedAt) {
		t.Errorf("attempt 1 bounds = started %v finished %v, want finished not before started", first.StartedAt, first.FinishedAt)
	}
	if first.DurationMS == nil || *first.DurationMS < 0 {
		t.Errorf("attempt 1 duration_ms = %v, want a non-negative measured span", first.DurationMS)
	}
	if len(first.Stages) != 2 {
		t.Fatalf("attempt 1 stages = %d, want 2 (%s)", len(first.Stages), w.Body)
	}
	prepare := first.Stages[0]
	if prepare.Name != "prepare" || prepare.Seq != 0 || prepare.Status != "ok" || prepare.Error != "" {
		t.Errorf("stage 0 = %+v, want prepare/0/ok with no error", prepare)
	}
	// StartStep/FinishStep stamp started_at/finished_at with the store's own
	// clock (now()), not a caller-supplied time, so only their presence and
	// non-negativity are checked here -- railBase no longer applies to a step
	// row the way it did to a hand-inserted event.
	if prepare.StartedAt == nil || prepare.DurationMS == nil || *prepare.DurationMS < 0 {
		t.Errorf("stage 0 = %+v, want a recorded start and a non-negative duration", prepare)
	}
	implement := first.Stages[1]
	if implement.Name != "implement" || implement.Seq != 1 || implement.Status != "failed" {
		t.Errorf("stage 1 = %+v, want implement/1/failed", implement)
	}
	if implement.Error != "stage implement: builder exited 1" {
		t.Errorf("stage 1 error = %q, want the recorded error text", implement.Error)
	}
	if implement.DurationMS == nil || *implement.DurationMS < 0 {
		t.Errorf("stage 1 duration_ms = %v, want a non-negative measured duration", implement.DurationMS)
	}

	second := body.Attempts[1]
	if second.Attempt != 2 || second.Status != "running" || second.EventCount != 1 {
		t.Errorf("attempt 2 = %d/%q with %d events, want 2/running with 1", second.Attempt, second.Status, second.EventCount)
	}
	if second.FinishedAt != nil || second.DurationMS != nil {
		t.Errorf("attempt 2 finished_at = %v, duration_ms = %v; both must be omitted for a running attempt",
			second.FinishedAt, second.DurationMS)
	}
	if len(second.Stages) != 1 || second.Stages[0].Name != "implement" || second.Stages[0].Status != "running" {
		t.Fatalf("attempt 2 stages = %+v, want one running implement", second.Stages)
	}
	if second.Stages[0].Error != "" {
		t.Errorf("attempt 2 stage error = %q, want empty", second.Stages[0].Error)
	}
}

// TestHandleTaskAttemptsSeparatesUnattributedEventsFromAFirstRun pins the
// honesty requirement: events that carry no attempt cannot be presented as
// attempt 1, and a task with nothing recorded at all is a different answer from
// a task whose events predate attribution.
func TestHandleTaskAttemptsSeparatesUnattributedEventsFromAFirstRun(t *testing.T) {
	t.Run("a task with no events at all", func(t *testing.T) {
		srv := newTestServer(t)
		if _, err := srv.Store.EnqueueIssue(t.Context(), "acme", "widget", 1, "task", "", "", ""); err != nil {
			t.Fatal(err)
		}
		task, err := srv.Store.TaskByIssue(t.Context(), "acme", "widget", 1)
		if err != nil {
			t.Fatal(err)
		}

		w := getTaskAPI(t, srv, taskRoute(task.ID, "/attempts"))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		raw := w.Body.String()
		if !strings.Contains(raw, `"attempts":[]`) {
			t.Errorf("body = %s, want an empty attempts array rather than null", raw)
		}
		if !strings.Contains(raw, `"unattributed_events":0`) {
			t.Errorf("body = %s, want unattributed_events 0 for a task with nothing recorded", raw)
		}
	})

	t.Run("events that carry no attempt", func(t *testing.T) {
		srv := newTestServer(t)
		if _, err := srv.Store.EnqueueIssue(t.Context(), "acme", "widget", 1, "task", "", "", ""); err != nil {
			t.Fatal(err)
		}
		task, err := srv.Store.TaskByIssue(t.Context(), "acme", "widget", 1)
		if err != nil {
			t.Fatal(err)
		}
		insertTaskEvent(t, srv, attemptEvent(task.ID, 0, railBase))
		insertTaskEvent(t, srv, attemptEvent(task.ID, 0, railBase.Add(time.Second)))

		w := getTaskAPI(t, srv, taskRoute(task.ID, "/attempts"))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		var body struct {
			UnattributedEvents int `json:"unattributed_events"`
			Attempts           []struct {
				Attempt int `json:"attempt"`
			} `json:"attempts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v (%s)", err, w.Body)
		}
		if body.UnattributedEvents != 2 {
			t.Errorf("unattributed_events = %d, want 2", body.UnattributedEvents)
		}
		if len(body.Attempts) != 0 {
			t.Errorf("attempts = %+v, want none: events carrying no attempt must never be presented as a run", body.Attempts)
		}
	})
}

// TestTaskDetailRoutesRejectUnknownTasksAndStoreFailures keeps the three new
// reads on the existing task-route conventions: an unknown task is 404 plain
// text (never 200 with an empty body), a store failure is 500, and a
// non-numeric id is 400.
func TestTaskDetailRoutesRejectUnknownTasksAndStoreFailures(t *testing.T) {
	base := newTestServer(t)
	if _, err := base.Store.EnqueueIssue(t.Context(), "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	existing, err := base.Store.TaskByIssue(t.Context(), "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}

	eventsBroken := &Server{
		Store: &stubStore{TaskStore: base.Store, taskEventsErr: fmt.Errorf("db locked")},
		Log:   base.Log,
	}
	lookupBroken := &Server{
		Store: &stubStore{TaskStore: base.Store, taskByIDErr: fmt.Errorf("db locked")},
		Log:   base.Log,
	}

	tests := []struct {
		name       string
		srv        *Server
		id         int64
		suffix     string
		wantStatus int
	}{
		{"attempts unknown task", base, 999, "/attempts", http.StatusNotFound},
		{"changes unknown task", base, 999, "/changes", http.StatusNotFound},
		{"debug unknown task", base, 999, "/debug", http.StatusNotFound},
		{"attempts events failure", eventsBroken, existing.ID, "/attempts", http.StatusInternalServerError},
		{"changes events failure", eventsBroken, existing.ID, "/changes", http.StatusInternalServerError},
		{"debug events failure", eventsBroken, existing.ID, "/debug", http.StatusInternalServerError},
		{"attempts lookup failure", lookupBroken, existing.ID, "/attempts", http.StatusInternalServerError},
		{"changes lookup failure", lookupBroken, existing.ID, "/changes", http.StatusInternalServerError},
		{"debug lookup failure", lookupBroken, existing.ID, "/debug", http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := getTaskAPI(t, tt.srv, taskRoute(tt.id, tt.suffix))
			if w.Code != tt.wantStatus {
				t.Errorf("GET %s = %d (%s), want %d", tt.suffix, w.Code, w.Body, tt.wantStatus)
			}
		})
	}

	for _, suffix := range []string{"/attempts", "/changes", "/debug"} {
		t.Run("non-numeric id "+suffix, func(t *testing.T) {
			w := getTaskAPI(t, base, "/api/tasks/not-a-number"+suffix)
			if w.Code != http.StatusBadRequest {
				t.Errorf("GET %s = %d (%s), want 400", suffix, w.Code, w.Body)
			}
		})
	}

	// The attempt parameter belongs to the per-attempt reads: /attempts returns
	// every attempt of the task, so it does not consume the parameter at all,
	// while /changes and /debug resolve it and must refuse a value that is not
	// an attempt number rather than silently read the current one.
	t.Run("non-numeric attempt on /attempts is not a parameter of that read", func(t *testing.T) {
		w := getTaskAPI(t, base, taskRoute(existing.ID, "/attempts?attempt=later"))
		if w.Code != http.StatusOK {
			t.Errorf("GET /attempts?attempt=later = %d (%s), want 200", w.Code, w.Body)
		}
	})
	for _, suffix := range []string{"/changes", "/debug"} {
		t.Run("non-numeric attempt "+suffix, func(t *testing.T) {
			w := getTaskAPI(t, base, taskRoute(existing.ID, suffix+"?attempt=later"))
			if w.Code != http.StatusBadRequest {
				t.Errorf("GET %s?attempt=later = %d (%s), want 400", suffix, w.Code, w.Body)
			}
		})
	}
}
