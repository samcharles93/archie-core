package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
)

// railBase is the fixed clock the rail fixtures hang off, so every timestamp
// and duration asserted below is a literal rather than a tolerance.
var railBase = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

func stageStartEvent(taskID int64, attempt int, stage string, at time.Time) events.Event {
	return events.Event{Kind: events.KindStageStart, TaskID: taskID, Attempt: attempt, Stage: stage, At: at}
}

func stageFinishEvent(taskID int64, attempt int, stage string, at time.Time, data map[string]any) events.Event {
	return events.Event{Kind: events.KindStageFinish, TaskID: taskID, Attempt: attempt, Stage: stage, At: at, Data: data}
}

func insertTaskEvent(t *testing.T, srv *Server, ev events.Event) {
	t.Helper()
	if _, err := srv.Store.InsertEvent(t.Context(), ev); err != nil {
		t.Fatalf("insert %s event: %v", ev.Kind, err)
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
func summarizeAttemptRail(attempts []taskAttemptView) string {
	lines := make([]string, 0, len(attempts))
	for _, a := range attempts {
		stages := make([]string, 0, len(a.Stages))
		for _, s := range a.Stages {
			stage := s.Name + ":" + s.Status + ":" + durationToken(s.DurationMS)
			if s.StartedAt == nil {
				stage += "@nostart"
			}
			if s.Error != "" {
				stage += "#" + s.Error
			}
			stages = append(stages, stage)
		}
		line := strconv.Itoa(a.Attempt) + ":" + a.Status + "[" + strings.Join(stages, ",") + "] " + durationToken(a.DurationMS)
		lines = append(lines, line)
	}
	return strings.Join(lines, " | ")
}

func durationToken(ms *int64) string {
	if ms == nil {
		return "?"
	}
	return strconv.FormatInt(*ms, 10)
}

// TestAttemptsFromEventsDerivesEveryRailRule is the table behind R1: every
// status rule the attempt rail depends on, one case each, plus the cases that
// would otherwise be answered by inventing data (a finish with no start, an
// event set with no stage information, events carrying no attempt at all).
func TestAttemptsFromEventsDerivesEveryRailRule(t *testing.T) {
	at := railBase.Add
	duration := func(ms int64) map[string]any { return map[string]any{"duration_ms": ms} }

	tests := []struct {
		name             string
		taskStatus       string
		currentAttempt   int
		evs              []events.Event
		want             string
		wantUnattributed int
	}{
		{
			name:       "a finished stage is ok and the attempt reports its measured span",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs: []events.Event{
				stageStartEvent(1, 1, "prepare", at(0)),
				stageFinishEvent(1, 1, "prepare", at(time.Second), duration(1000)),
			},
			want: "1:ok[prepare:ok:1000] 1000",
		},
		{
			name:       "a failed stage fails the attempt and carries the error text",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs: []events.Event{
				stageStartEvent(1, 1, "implement", at(0)),
				stageFinishEvent(1, 1, "implement", at(2*time.Second), map[string]any{
					"duration_ms": int64(2000),
					"error":       "stage implement: builder exited 1",
				}),
			},
			want: "1:failed[implement:failed:2000#stage implement: builder exited 1] 2000",
		},
		{
			name:       "an interrupted stage reads interrupted even though the error text is recorded",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs: []events.Event{
				stageStartEvent(1, 1, "implement", at(0)),
				stageFinishEvent(1, 1, "implement", at(time.Second), map[string]any{
					"duration_ms": int64(1000),
					"error":       "context canceled",
					"interrupted": true,
				}),
			},
			want: "1:interrupted[implement:interrupted:1000#context canceled] 1000",
		},
		{
			name:       "a failed stage outranks an interrupted one",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs: []events.Event{
				stageStartEvent(1, 1, "a", at(0)),
				stageFinishEvent(1, 1, "a", at(time.Second), map[string]any{"duration_ms": int64(1000), "error": "boom"}),
				stageStartEvent(1, 1, "b", at(2*time.Second)),
				stageFinishEvent(1, 1, "b", at(3*time.Second), map[string]any{
					"duration_ms": int64(1000), "error": "canceled", "interrupted": true,
				}),
			},
			want: "1:failed[a:failed:1000#boom,b:interrupted:1000#canceled] 3000",
		},
		{
			name:       "an unpaired start on the task's current in-flight attempt is running",
			taskStatus: workflow.StatusRunning, currentAttempt: 2,
			evs:  []events.Event{stageStartEvent(1, 2, "implement", at(0))},
			want: "2:running[implement:running:?] ?",
		},
		{
			name:       "an unpaired start on a superseded attempt is interrupted",
			taskStatus: workflow.StatusRunning, currentAttempt: 2,
			evs:  []events.Event{stageStartEvent(1, 1, "implement", at(0))},
			want: "1:interrupted[implement:interrupted:?] 0",
		},
		{
			name:       "an unpaired start on a parked task's own attempt is interrupted",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs:  []events.Event{stageStartEvent(1, 1, "implement", at(0))},
			want: "1:interrupted[implement:interrupted:?] 0",
		},
		{
			name:       "a finish with no start yields the stage without an invented start time",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs: []events.Event{
				stageFinishEvent(1, 1, "commit", at(0), duration(4000)),
			},
			want: "1:ok[commit:ok:4000@nostart] 0",
		},
		{
			name:       "a finish without a recorded duration derives it from its own start",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs: []events.Event{
				stageStartEvent(1, 1, "prepare", at(0)),
				stageFinishEvent(1, 1, "prepare", at(10*time.Second), nil),
			},
			want: "1:ok[prepare:ok:10000] 10000",
		},
		{
			name:       "a repeated stage name yields one rail entry per occurrence",
			taskStatus: workflow.StatusRunning, currentAttempt: 1,
			evs: []events.Event{
				stageStartEvent(1, 1, "implement", at(0)),
				stageFinishEvent(1, 1, "implement", at(time.Second), duration(1000)),
				stageStartEvent(1, 1, "implement", at(2*time.Second)),
			},
			want: "1:running[implement:ok:1000,implement:running:?] ?",
		},
		{
			name:       "a later attempt does not absorb the earlier attempt's stages",
			taskStatus: workflow.StatusRunning, currentAttempt: 2,
			evs: []events.Event{
				stageStartEvent(1, 1, "prepare", at(0)),
				stageFinishEvent(1, 1, "prepare", at(time.Second), duration(1000)),
				stageStartEvent(1, 2, "implement", at(2*time.Second)),
			},
			want: "1:ok[prepare:ok:1000] 1000 | 2:running[implement:running:?] ?",
		},
		{
			name:       "events carrying no attempt are counted, never grouped",
			taskStatus: workflow.StatusQueued, currentAttempt: 0,
			evs: []events.Event{
				{Kind: events.KindStageStart, TaskID: 1, Stage: "prepare", At: at(0)},
				{Kind: events.KindStageFinish, TaskID: 1, Stage: "prepare", At: at(time.Second), Data: duration(1000)},
			},
			wantUnattributed: 2,
		},
		{
			name:       "a task with no events has no attempts and nothing unattributed",
			taskStatus: workflow.StatusQueued, currentAttempt: 0,
			want: "",
		},
		{
			name:       "an attempt whose events carry no stage information is unknown",
			taskStatus: workflow.StatusParked, currentAttempt: 1,
			evs: []events.Event{
				{Kind: events.KindAgentFinish, TaskID: 1, Attempt: 1, At: at(0), Data: map[string]any{"status": "ok"}},
			},
			want: "1:unknown[] ?",
		},
		{
			name:       "the task's own record keeps the current attempt running with no stage events",
			taskStatus: workflow.StatusRunning, currentAttempt: 1,
			evs: []events.Event{
				{Kind: events.KindAgentFinish, TaskID: 1, Attempt: 1, At: at(0), Data: map[string]any{"status": "ok"}},
			},
			want: "1:running[] ?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts, unattributed := attemptsFromEvents(tt.evs, tt.taskStatus, tt.currentAttempt)
			if got := summarizeAttemptRail(attempts); got != tt.want {
				t.Errorf("rail = %q, want %q", got, tt.want)
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
	current := twoAttemptTask(t, srv)
	at := railBase.Add

	insertTaskEvent(t, srv, stageStartEvent(current.ID, 1, "prepare", at(0)))
	insertTaskEvent(t, srv, stageFinishEvent(current.ID, 1, "prepare", at(1200*time.Millisecond),
		map[string]any{"duration_ms": int64(1200)}))
	insertTaskEvent(t, srv, stageStartEvent(current.ID, 1, "implement", at(1323*time.Millisecond)))
	insertTaskEvent(t, srv, stageFinishEvent(current.ID, 1, "implement", at(231323*time.Millisecond),
		map[string]any{"duration_ms": int64(230000), "error": "stage implement: builder exited 1"}))
	insertTaskEvent(t, srv, stageStartEvent(current.ID, 2, "implement", at(time.Hour)))

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
	if first.StartedAt == nil || !first.StartedAt.Equal(at(0)) {
		t.Errorf("attempt 1 started_at = %v, want %v", first.StartedAt, at(0))
	}
	if first.FinishedAt == nil || !first.FinishedAt.Equal(at(231323*time.Millisecond)) {
		t.Errorf("attempt 1 finished_at = %v, want %v", first.FinishedAt, at(231323*time.Millisecond))
	}
	if first.DurationMS == nil || *first.DurationMS != 231323 {
		t.Errorf("attempt 1 duration_ms = %v, want 231323", first.DurationMS)
	}
	if len(first.Stages) != 2 {
		t.Fatalf("attempt 1 stages = %d, want 2 (%s)", len(first.Stages), w.Body)
	}
	prepare := first.Stages[0]
	if prepare.Name != "prepare" || prepare.Seq != 0 || prepare.Status != "ok" || prepare.Error != "" {
		t.Errorf("stage 0 = %+v, want prepare/0/ok with no error", prepare)
	}
	if prepare.DurationMS == nil || *prepare.DurationMS != 1200 {
		t.Errorf("stage 0 duration_ms = %v, want 1200", prepare.DurationMS)
	}
	if prepare.StartedAt == nil || !prepare.StartedAt.Equal(at(0)) {
		t.Errorf("stage 0 started_at = %v, want %v", prepare.StartedAt, at(0))
	}
	implement := first.Stages[1]
	if implement.Name != "implement" || implement.Seq != 1 || implement.Status != "failed" {
		t.Errorf("stage 1 = %+v, want implement/1/failed", implement)
	}
	if implement.Error != "stage implement: builder exited 1" {
		t.Errorf("stage 1 error = %q, want the recorded error text", implement.Error)
	}
	if implement.DurationMS == nil || *implement.DurationMS != 230000 {
		t.Errorf("stage 1 duration_ms = %v, want 230000", implement.DurationMS)
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
		insertTaskEvent(t, srv, stageStartEvent(task.ID, 0, "prepare", railBase))
		insertTaskEvent(t, srv, stageFinishEvent(task.ID, 0, "prepare", railBase.Add(time.Second),
			map[string]any{"duration_ms": int64(1000)}))

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
