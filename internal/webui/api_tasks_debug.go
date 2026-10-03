package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

// taskDebugView is GET /api/tasks/{id}/debug: the raw task record and all of
// its events.
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
