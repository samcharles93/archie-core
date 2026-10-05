package webui

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Stage/attempt statuses the attempt rail reports. They are one vocabulary
// because the rail renders one status per stage and one for the attempt it
// belongs to; "unknown" exists only for an attempt whose events carry no stage
// information at all, so a rail never claims a verdict it did not observe.
const (
	attemptStatusOK          = "ok"
	attemptStatusFailed      = "failed"
	attemptStatusInterrupted = "interrupted"
	attemptStatusRunning     = "running"
	attemptStatusSkipped     = "skipped"
	attemptStatusUnknown     = "unknown"
)

// taskAttemptsView is GET /api/tasks/{id}/attempts: every attempt of one task
// with its stages inline, so the rail and the attempt selector cannot disagree
// and the page makes no per-attempt calls of its own.
type taskAttemptsView struct {
	TaskID int64 `json:"task_id"`
	// CurrentAttempt is the task's own attempt, echoed so the page can show
	// which run the daemon is on even when that attempt has no events yet.
	CurrentAttempt int `json:"current_attempt"`
	// UnattributedEvents counts events whose attempt is 0. It exists so the
	// page can say honestly that older activity cannot be attributed instead
	// of presenting it as a first run.
	UnattributedEvents int               `json:"unattributed_events"`
	Attempts           []taskAttemptView `json:"attempts"`
}

// taskAttemptView is one attempt, bounded by its first and last event.
// Unknown fields are omitted.
type taskAttemptView struct {
	Attempt    int             `json:"attempt"`
	Status     string          `json:"status"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	DurationMS *int64          `json:"duration_ms,omitempty"`
	EventCount int             `json:"event_count"`
	Stages     []taskStageView `json:"stages"`
}

// taskStageView is one stage occurrence within an attempt. Error is always
// present (empty when the stage recorded none) so a consumer can tell "no error
// text" from a field it did not receive.
type taskStageView struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Seq        int             `json:"seq"`
	Status     string          `json:"status"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	DurationMS *int64          `json:"duration_ms,omitempty"`
	Error      string          `json:"error"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Agents     []taskAgentView `json:"agents,omitempty"`
}

type taskAgentView struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Detail     string     `json:"detail"`
	TokensUsed int64      `json:"tokens_used"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// handleTaskAttempts returns a task's attempts from its events, each with the
// stages recorded as step executions.
func (s *Server) handleTaskAttempts(w http.ResponseWriter, r *http.Request) {
	t, ok := s.taskByPathID(w, r)
	if !ok {
		return
	}
	evs, err := s.Store.TaskEvents(r.Context(), t.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var steps []task.StepExecution
	if s.Steps != nil {
		steps, err = s.Steps.ListSteps(r.Context(), t.ID, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	attempts, unattributed := attemptsFromEvents(evs, steps, t.Status, t.Attempt)
	writeJSON(w, taskAttemptsView{
		TaskID:             t.ID,
		CurrentAttempt:     t.Attempt,
		UnattributedEvents: unattributed,
		Attempts:           attempts,
	})
}

// taskByPathID resolves a task route's {id} to the task it names, answering the
// request itself and reporting false when it cannot. An unknown task is 404
// plain text -- TaskByID says "no such row" the same way it says "here is the
// row", so the distinction is drawn here rather than leaked to the page.
func (s *Server) taskByPathID(w http.ResponseWriter, r *http.Request) (*task.Task, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return nil, false
	}
	t, err := s.Store.TaskByID(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return nil, false
	}
	return t, true
}

// taskAttemptTarget resolves the task in {id} and the requested attempt,
// defaulting to the current one.
func (s *Server) taskAttemptTarget(w http.ResponseWriter, r *http.Request) (*task.Task, int, bool) {
	t, ok := s.taskByPathID(w, r)
	if !ok {
		return nil, 0, false
	}
	attempt := t.Attempt
	raw := strings.TrimSpace(r.URL.Query().Get("attempt"))
	if raw == "" {
		return t, attempt, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		http.Error(w, "bad attempt", http.StatusBadRequest)
		return nil, 0, false
	}
	if parsed != 0 {
		attempt = parsed
	}
	return t, attempt, true
}

// attemptsFromEvents groups a task's events by attempt and counts events with
// no attempt.
func attemptsFromEvents(evs []events.Event, steps []task.StepExecution, taskStatus string, currentAttempt int) ([]taskAttemptView, int) {
	grouped := make(map[int][]events.Event)
	numbers := make([]int, 0, 4)
	unattributed := 0
	for _, e := range evs {
		if e.Attempt == 0 {
			unattributed++
			continue
		}
		if _, seen := grouped[e.Attempt]; !seen {
			numbers = append(numbers, e.Attempt)
		}
		grouped[e.Attempt] = append(grouped[e.Attempt], e)
	}
	sort.Ints(numbers)

	attempts := make([]taskAttemptView, 0, len(numbers))
	for _, number := range numbers {
		attempts = append(attempts, attemptRail(number, grouped[number], stepsForAttempt(steps, number), taskStatus, currentAttempt))
	}
	return attempts, unattributed
}

// stepsForAttempt filters ListSteps' full-execution result to one attempt's
// steps, in the order the query already returns them (attempt, id).
func stepsForAttempt(steps []task.StepExecution, attempt int) []task.StepExecution {
	var out []task.StepExecution
	for _, s := range steps {
		if s.Attempt == attempt {
			out = append(out, s)
		}
	}
	return out
}

// attemptRail derives one attempt's status and bounds from its events, and
// its stages from its recorded StepExecutions.
func attemptRail(number int, evs []events.Event, steps []task.StepExecution, taskStatus string, currentAttempt int) taskAttemptView {
	// The task's own record is the only honest source for "is this still
	// happening": a step still running on any other attempt ended without
	// finishing -- parked, retried, crashed or restarted -- so it is reported
	// as interrupted rather than left looking permanently in flight.
	inFlight := number == currentAttempt && taskStatus == task.StatusRunning

	first, last := attemptBounds(evs)
	stages := stageViewsFromSteps(steps, inFlight)
	attempt := taskAttemptView{
		Attempt:    number,
		Status:     attemptStatus(stages, inFlight),
		EventCount: len(evs),
		Stages:     stages,
	}
	if !first.IsZero() {
		attempt.StartedAt = &first
	}
	// A finished_at is only claimed for an attempt that has an end: a running
	// one has none yet, and an unknown one never had stage information to time.
	if !last.IsZero() && attempt.Status != attemptStatusRunning && attempt.Status != attemptStatusUnknown {
		attempt.FinishedAt = &last
		if !first.IsZero() {
			span := last.Sub(first).Milliseconds()
			attempt.DurationMS = &span
		}
	}
	return attempt
}

// attemptBounds is the span of an attempt's own recorded events: the earliest
// and latest timestamp any of them carries. A zero time contributes nothing -- a
// record whose timestamp did not parse is not evidence of when it happened.
func attemptBounds(evs []events.Event) (first, last time.Time) {
	for _, e := range evs {
		if e.At.IsZero() {
			continue
		}
		if first.IsZero() || e.At.Before(first) {
			first = e.At
		}
		if last.IsZero() || e.At.After(last) {
			last = e.At
		}
	}
	return first, last
}

// stageViewsFromSteps returns an attempt's stage steps in order.
func stageViewsFromSteps(steps []task.StepExecution, inFlight bool) []taskStageView {
	stages := []taskStageView{}
	for _, s := range steps {
		if s.Kind != task.StepKindStage {
			continue
		}
		view := taskStageView{ID: s.ID, Name: s.Name, Seq: len(stages), Status: mapStepStatus(s.Status, inFlight)}
		if s.Status == taskstate.StepFailed {
			view.Error = s.Detail
		}
		if !s.StartedAt.IsZero() {
			started := s.StartedAt
			view.StartedAt = &started
		}
		if !s.FinishedAt.IsZero() {
			finished := s.FinishedAt
			view.FinishedAt = &finished
			duration := s.FinishedAt.Sub(s.StartedAt).Milliseconds()
			view.DurationMS = &duration
		}
		for _, child := range steps {
			if child.ParentID != s.ID || child.Kind != task.StepKindAgent {
				continue
			}
			agent := taskAgentView{ID: child.ID, Name: child.Name, Status: mapStepStatus(child.Status, inFlight), Detail: child.Detail, TokensUsed: child.TokensUsed, StartedAt: child.StartedAt}
			if !child.FinishedAt.IsZero() {
				finished := child.FinishedAt
				agent.FinishedAt = &finished
			}
			view.Agents = append(view.Agents, agent)
		}
		stages = append(stages, view)
	}
	return stages
}

// mapStepStatus maps a step status; a step still running after its attempt
// ended reads interrupted.
func mapStepStatus(status taskstate.StepStatus, inFlight bool) string {
	switch status {
	case taskstate.StepSucceeded:
		return attemptStatusOK
	case taskstate.StepFailed:
		return attemptStatusFailed
	case taskstate.StepCancelled, taskstate.StepInterrupted:
		return attemptStatusInterrupted
	case taskstate.StepSkipped:
		return attemptStatusSkipped
	case taskstate.StepRunning:
		if inFlight {
			return attemptStatusRunning
		}
		return attemptStatusInterrupted
	default:
		return attemptStatusUnknown
	}
}

// attemptStatus derives an attempt's status from its stages and whether it is
// running now.
func attemptStatus(stages []taskStageView, inFlight bool) string {
	if inFlight {
		return attemptStatusRunning
	}
	switch {
	case anyStageStatus(stages, attemptStatusFailed):
		return attemptStatusFailed
	case anyStageStatus(stages, attemptStatusInterrupted):
		return attemptStatusInterrupted
	case len(stages) == 0:
		// The attempt exists only because events attributed to it do; if none
		// of them recorded a stage there is no verdict to report.
		return attemptStatusUnknown
	default:
		return attemptStatusOK
	}
}

func anyStageStatus(stages []taskStageView, status string) bool {
	for _, s := range stages {
		if s.Status == status {
			return true
		}
	}
	return false
}
