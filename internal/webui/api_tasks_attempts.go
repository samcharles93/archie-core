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

// taskAttemptView is one attempt's rail entry. The bounds and duration are
// measured from the attempt's own events: the span between its first and last
// recorded event, which therefore includes any waiting-for-a-human gap rather
// than understating wall time (design AMENDMENTS 0.1, F3). Every optional
// field is omitted when it is not known rather than sent as a zero.
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
	Name       string     `json:"name"`
	Seq        int        `json:"seq"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	DurationMS *int64     `json:"duration_ms,omitempty"`
	Error      string     `json:"error"`
}

// handleTaskAttempts serves the stage rail: one task's attempts, each with the
// stages it recorded. Attempt bounds, event counts and which attempt numbers
// exist still come from the task's events (an attempt can carry events with
// no step, e.g. agent calls, so events remain the complete attempt index);
// the stages themselves are step_executions, the authoritative record,
// not a fold over
// stage_start/stage_finish events.
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

// taskAttemptTarget resolves a per-attempt read: the task its {id} names and the
// attempt it asks for. An absent or zero "attempt" parameter selects the task's
// current attempt, which is what a human means by "why did this park?" -- the
// most recent run, not an arbitrary earlier retry. Zero is not an attempt: it is
// the value that means "unattributed" on an event, so reading "attempt zero"
// would read a bucket rather than a run.
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

// attemptsFromEvents folds a task's timeline into its rail: one entry per
// attempt the events attribute to, ordered by attempt, plus the number of events
// that carry no attempt at all.
//
// Events with attempt 0 are counted and then dropped, never grouped. Zero means
// unattributed -- every row written before the column existed and every
// deliberately task-agnostic producer carries it -- so grouping them would
// present unrelated activity as attempt zero's rail.
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

// stageViewsFromSteps builds an attempt's ordered stage occurrences directly
// from its recorded StepExecutions -- the authoritative record,
// not a fold over
// stage_start/stage_finish events. Only kind "stage" steps are shown: agent
// and call steps are the tree's own detail, not this rail's.
func stageViewsFromSteps(steps []task.StepExecution, inFlight bool) []taskStageView {
	stages := []taskStageView{}
	for _, s := range steps {
		if s.Kind != task.StepKindStage {
			continue
		}
		view := taskStageView{Name: s.Name, Seq: len(stages), Status: mapStepStatus(s.Status, inFlight)}
		if s.Status == taskstate.StepFailed {
			view.Error = s.Detail
		}
		if !s.StartedAt.IsZero() {
			started := s.StartedAt
			view.StartedAt = &started
		}
		if !s.FinishedAt.IsZero() {
			duration := s.FinishedAt.Sub(s.StartedAt).Milliseconds()
			view.DurationMS = &duration
		}
		stages = append(stages, view)
	}
	return stages
}

// mapStepStatus reads the rail's status vocabulary off a step's own recorded
// status. A step still recorded running past this attempt -- the daemon
// crashed or restarted without marking it -- reads interrupted rather than
// running: RecoverStale is what corrects the row itself, and until it runs
// this is the honest read of a stale one.
func mapStepStatus(status taskstate.StepStatus, inFlight bool) string {
	switch status {
	case taskstate.StepSucceeded:
		return attemptStatusOK
	case taskstate.StepFailed:
		return attemptStatusFailed
	case taskstate.StepCancelled, taskstate.StepInterrupted:
		return attemptStatusInterrupted
	case taskstate.StepRunning:
		if inFlight {
			return attemptStatusRunning
		}
		return attemptStatusInterrupted
	default:
		return attemptStatusUnknown
	}
}

// attemptStatus reads one status off an attempt's stages. The task's own record
// outranks the events: an attempt the daemon is executing right now is running
// whatever its events have recorded so far -- including the ones that have
// recorded no stages at all. Otherwise no stage is left RUNNING to read: every
// stage still open when the daemon is not executing this attempt is folded as
// interrupted, which is what the next case reports.
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
