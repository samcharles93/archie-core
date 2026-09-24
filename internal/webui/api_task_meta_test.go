package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// The dashboard derives everything it can present from /api/task-meta. If the
// catalog is empty or malformed the frontend falls back to its own defaults and
// the whole point of the endpoint -- one server-held vocabulary -- is lost.
func TestTaskMetaCatalog(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/task-meta", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}

	var got struct {
		Statuses []struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			Kind     string `json:"kind"`
			NeedsYou bool   `json:"needs_you"`
		} `json:"statuses"`
		Actions []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Kind    string `json:"kind"`
			Confirm string `json:"confirm"`
		} `json:"actions"`
		ChangeStatuses []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"change_statuses"`
		ConfigSchema string `json:"config_schema"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Statuses) == 0 {
		t.Error("statuses empty")
	}
	if len(got.Actions) == 0 {
		t.Error("actions empty")
	}
	if len(got.ChangeStatuses) == 0 {
		t.Error("change_statuses empty; the dashboard renders raw status codes without it")
	}
	if got.ConfigSchema == "" {
		t.Error("config_schema empty; the config panel would call every capture an unknown schema")
	}
	for _, s := range got.Statuses {
		if s.ID == "" || s.Label == "" {
			t.Errorf("status missing id/label: %+v", s)
		}
	}
	for _, a := range got.Actions {
		if a.ID == "" || a.Label == "" {
			t.Errorf("action missing id/label: %+v", a)
		}
	}
	for _, cs := range got.ChangeStatuses {
		if cs.ID == "" || cs.Label == "" {
			t.Errorf("change status missing id/label: %+v", cs)
		}
	}
}

// TestTaskMetaChangeStatusesAreDeliberate pins the catalog's change-status IDs
// to the producer's constants, in order, each with a distinct label. It is the
// same guard config_schema_test.go applies to the config catalog: a sixth
// Change* constant added without a label fails here rather than reaching the
// dashboard as a raw status code.
func TestTaskMetaChangeStatusesAreDeliberate(t *testing.T) {
	want := []string{
		task.ChangeAdded,
		task.ChangeModified,
		task.ChangeDeleted,
		task.ChangeRenamed,
		task.ChangeTypeChanged,
	}
	got := buildTaskMeta().ChangeStatuses
	if len(got) != len(want) {
		t.Fatalf("change_statuses has %d entries, want %d (one per Change* constant)", len(got), len(want))
	}
	labels := make(map[string]string, len(got))
	for i, cs := range got {
		if cs.ID != want[i] {
			t.Errorf("change_statuses[%d].id = %q, want %q", i, cs.ID, want[i])
		}
		if cs.Label == "" {
			t.Errorf("change status %q has no label", cs.ID)
			continue
		}
		if prev, dup := labels[cs.Label]; dup {
			t.Errorf("change statuses %q and %q share the label %q", prev, cs.ID, cs.Label)
		}
		labels[cs.Label] = cs.ID
	}
}
