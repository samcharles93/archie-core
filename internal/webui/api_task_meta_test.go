package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
}
