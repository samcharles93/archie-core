package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/gateway"
)

type recordingWorkRequestCreator struct{ request gateway.SpawnRequest }

func (c *recordingWorkRequestCreator) CreateTask(_ context.Context, request gateway.SpawnRequest) (int64, error) {
	c.request = request
	return 42, nil
}

func TestWorkflowsIncludeInstalledZeroRunDefinitions(t *testing.T) {
	srv := newTestServer(t)
	srv.Workflows = []workflow.Definition{{ID: "implement", Name: "Implement", Origin: "builtin", Enabled: true, Stages: []string{"prepare", "plan"}}}

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/workflows", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var response struct {
		Definitions []workflow.Definition `json:"definitions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Definitions) != 1 || response.Definitions[0].ID != "implement" || len(response.Definitions[0].Stages) != 2 {
		t.Fatalf("definitions = %#v", response.Definitions)
	}
}

func TestWorkRequestUsesNormalTaskAdmission(t *testing.T) {
	srv := newTestServer(t)
	creator := &recordingWorkRequestCreator{}
	srv.WorkRequests = creator
	srv.Workflows = []workflow.Definition{{ID: "implement", Name: "Implement", Enabled: true}}

	body := `{"identity":"archie","repository":"acme/widget","workflow":"implement","title":"Fix login","instructions":"Reproduce and fix it."}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/work-requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if got := creator.request; got.Identity != "archie" || got.Repo != "acme/widget" || got.Workflow != "implement" || got.Title != "Fix login" || got.Body != "Reproduce and fix it." {
		t.Fatalf("request = %#v", got)
	}
}

func TestWorkRequestRejectsDisabledWorkflow(t *testing.T) {
	srv := newTestServer(t)
	creator := &recordingWorkRequestCreator{}
	srv.WorkRequests = creator
	srv.Workflows = []workflow.Definition{{ID: "implement", Enabled: false}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/work-requests", strings.NewReader(`{"identity":"archie","repository":"acme/widget","workflow":"implement","title":"Fix","instructions":"Do it"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if creator.request != (gateway.SpawnRequest{}) {
		t.Fatalf("creator called with %#v", creator.request)
	}
}
