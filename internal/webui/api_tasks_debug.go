package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

// taskDebugView is GET /api/tasks/{id}/debug: the stored task record as it is,
// and every event recorded for the task. It is deliberately not a generic
// store-dump route -- the task list is capped at 100 rows, so a page that wanted
// to show an older task's raw provenance had no row to join against. Nothing
// here is projected, filtered or re-spelled: reading it is the point.
//
// Events are UNFILTERED. A debug view that silently hid the events of other
// attempts would be worse than useless; each event carries its own attempt, so
// the operator sees everything and can attribute it. Attempt reports only which
// attempt the page had selected. No log payload is included: the log tab already
// serves it, and embedding it would duplicate a large body for no new
// information.
type taskDebugView struct {
	TaskID  int64          `json:"task_id"`
	Attempt int            `json:"attempt"`
	Task    task.Task      `json:"task"`
	Events  []events.Event `json:"events"`
}

// handleTaskDebug serves the task's raw record together with its whole event
// list, for whichever attempt the request names.
func (s *Server) handleTaskDebug(w http.ResponseWriter, r *http.Request) {
	t, attempt, ok := s.taskAttemptTarget(w, r)
	if !ok {
		return
	}
	evs, err := s.Store.TaskEvents(r.Context(), t.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if evs == nil {
		evs = []events.Event{}
	}
	writeJSON(w, taskDebugView{TaskID: t.ID, Attempt: attempt, Task: *t, Events: evs})
}
