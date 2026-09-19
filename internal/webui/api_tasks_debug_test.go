package webui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
)

// TestHandleTaskDebugReturnsTheWholeRecordAndEveryEvent pins R7's read: the
// stored task record verbatim, and EVERY event of the task rather than only the
// selected attempt's -- each event carries its own attempt, so a filtered list
// would hide the very provenance the view exists to show.
func TestHandleTaskDebugReturnsTheWholeRecordAndEveryEvent(t *testing.T) {
	srv := newTestServer(t)
	current := twoAttemptTask(t, srv)

	insertTaskEvent(t, srv, events.Event{
		Kind: events.KindStageStart, TaskID: current.ID, Attempt: 1, Stage: "prepare", At: railBase,
	})
	insertTaskEvent(t, srv, events.Event{
		Kind: events.KindAgentFinish, TaskID: current.ID, Attempt: 0, At: railBase.Add(time.Second),
		Data: map[string]any{"status": "ok"},
	})
	insertTaskEvent(t, srv, events.Event{
		Kind: events.KindStageStart, TaskID: current.ID, Attempt: 2, Stage: "implement",
		At: railBase.Add(2 * time.Second),
	})

	w := getTaskAPI(t, srv, taskRoute(current.ID, "/debug?attempt=1"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	raw := w.Body.String()
	if strings.Contains(raw, `"entries"`) {
		t.Errorf("body carries a log payload: %s", raw)
	}

	var body struct {
		TaskID  int64         `json:"task_id"`
		Attempt int           `json:"attempt"`
		Task    workflow.Task `json:"task"`
		Events  []struct {
			Kind    string `json:"kind"`
			Attempt int    `json:"attempt"`
		} `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body)
	}
	if body.TaskID != current.ID || body.Attempt != 1 {
		t.Errorf("envelope = task %d attempt %d, want task %d attempt 1 (the selected attempt)",
			body.TaskID, body.Attempt, current.ID)
	}
	if body.Task.ID != current.ID || body.Task.Status != workflow.StatusRunning || body.Task.Attempt != 2 {
		t.Errorf("task record = id %d status %q attempt %d, want the stored record (%d, running, 2)",
			body.Task.ID, body.Task.Status, body.Task.Attempt, current.ID)
	}
	if len(body.Events) != 3 {
		t.Fatalf("events = %d, want all 3 including the one recorded against a different attempt (%s)",
			len(body.Events), w.Body)
	}
	wantAttempts := []int{1, 0, 2}
	for i, want := range wantAttempts {
		if body.Events[i].Attempt != want {
			t.Errorf("event %d attempt = %d, want %d (order and attribution preserved)", i, body.Events[i].Attempt, want)
		}
	}
	if !strings.Contains(raw, `"attempt":0`) {
		t.Errorf("body = %s, want attempt carried on every event so an unattributed one stays distinguishable", raw)
	}
}

// TestHandleTaskDebugResolvesTheCurrentAttemptWithoutAParameter pins that the
// envelope reports the attempt the page selected, defaulting to the task's
// current attempt exactly as the log read does.
func TestHandleTaskDebugResolvesTheCurrentAttemptWithoutAParameter(t *testing.T) {
	srv := newTestServer(t)
	current := twoAttemptTask(t, srv)

	w := getTaskAPI(t, srv, taskRoute(current.ID, "/debug"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	var body struct {
		Attempt int `json:"attempt"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body)
	}
	if body.Attempt != current.Attempt {
		t.Errorf("attempt = %d, want the task's current attempt %d", body.Attempt, current.Attempt)
	}
}
