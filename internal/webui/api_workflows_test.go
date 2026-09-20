package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/gateway"
)

func workflowControlPlane(t *testing.T, definitions workflow.WorkflowDefinitionCollection) *controlPlaneClientStub {
	t.Helper()
	value, err := json.Marshal(definitions)
	if err != nil {
		t.Fatal(err)
	}
	return &controlPlaneClientStub{query: &controlpb.Resource{Kind: "workflow-definitions", Version: 1, ValueJson: value}}
}

type recordingWorkRequestCreator struct{ request gateway.SpawnRequest }

func (c *recordingWorkRequestCreator) CreateTask(_ context.Context, request gateway.SpawnRequest) (int64, error) {
	c.request = request
	return 42, nil
}

func TestWorkflowsIncludeInstalledZeroRunDefinitions(t *testing.T) {
	srv := newTestServer(t)
	srv.ControlPlane = workflowControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "custom", YAML: "id: custom\nsteps:\n  - type: implement.prepare\n  - type: implement.plan\n"}}})

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
	if len(response.Definitions) != 1 || response.Definitions[0].ID != "custom" || response.Definitions[0].Origin != "database" {
		t.Fatalf("definitions = %#v", response.Definitions)
	}
}

func TestWorkRequestUsesNormalTaskAdmission(t *testing.T) {
	srv := newTestServer(t)
	creator := &recordingWorkRequestCreator{}
	srv.WorkRequests = creator
	srv.ControlPlane = workflowControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "implement", YAML: "id: implement\nsteps:\n  - type: implement.prepare\n"}}})

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
	srv.ControlPlane = workflowControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "custom", YAML: "id: custom\nsteps:\n  - type: implement.prepare\n"}}})
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
