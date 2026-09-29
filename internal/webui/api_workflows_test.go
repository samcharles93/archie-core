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

// declaredInputsDefinition is a pr-review-shaped definition: it declares the
// inputs the handler checks a work request against, one of them required, so a
// request that does not name a pull request number cannot be admitted.
const declaredInputsDefinition = `id: pr-review
inputs:
  pr_number:
    type: number
    required: true
  depth:
    type: string
steps:
  - type: implement.prepare
`

func postWorkRequest(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/work-requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// TestWorkRequestCarriesDeclaredInputs: a dashboard request names the workflow's
// declared inputs and the admitted task carries them, so a pr-review request
// reaches its workflow with the pull request number stagePRIntake reads
// (archie-core-06nq).
func TestWorkRequestCarriesDeclaredInputs(t *testing.T) {
	srv := newTestServer(t)
	creator := &recordingWorkRequestCreator{}
	srv.WorkRequests = creator
	srv.ControlPlane = workflowControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "pr-review", YAML: declaredInputsDefinition}}})

	w := postWorkRequest(t, srv, `{"identity":"archie","repository":"acme/widget","workflow":"pr-review","title":"Review 77","instructions":"Review it.","inputs":{"pr_number":77,"depth":"deep"}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if got := creator.request.Inputs["pr_number"]; got != float64(77) {
		t.Errorf("pr_number input = %#v, want the number the request sent", got)
	}
	if got := creator.request.Inputs["depth"]; got != "deep" {
		t.Errorf("depth input = %#v, want \"deep\"", got)
	}
	// EffectivePRNumber is how the daemon and stagePRIntake read the input, so
	// the assignment is proven against the consumer rather than against the
	// map's shape alone.
	if got := (workflow.Task{Inputs: creator.request.Inputs}).EffectivePRNumber(); got != 77 {
		t.Errorf("EffectivePRNumber() = %d, want 77", got)
	}
}

// TestWorkRequestRejectsUnmetDeclaredInputs: the request is refused at the
// producer, naming the input, instead of being admitted and then parked or
// failed at the workflow's first stage.
func TestWorkRequestRejectsUnmetDeclaredInputs(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing a required input",
			body: `{"identity":"archie","repository":"acme/widget","workflow":"pr-review","title":"Review","instructions":"Review it."}`,
			want: `input "pr_number" is required`,
		},
		{
			name: "naming an input the workflow does not declare",
			body: `{"identity":"archie","repository":"acme/widget","workflow":"pr-review","title":"Review","instructions":"Review it.","inputs":{"pr_number":77,"pull_request":77}}`,
			want: `input "pull_request" is not declared by the workflow`,
		},
		{
			name: "a value of the wrong type",
			body: `{"identity":"archie","repository":"acme/widget","workflow":"pr-review","title":"Review","instructions":"Review it.","inputs":{"pr_number":"seventy-seven"}}`,
			want: `input "pr_number" is string, want number`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			creator := &recordingWorkRequestCreator{}
			srv.WorkRequests = creator
			srv.ControlPlane = workflowControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "pr-review", YAML: declaredInputsDefinition}}})

			w := postWorkRequest(t, srv, tt.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			if !strings.Contains(w.Body.String(), tt.want) {
				t.Errorf("body = %q, want it to name the failure %q", w.Body, tt.want)
			}
			if got := creator.request; got.Title != "" || got.Body != "" || len(got.Inputs) != 0 {
				t.Errorf("creator called with %#v", got)
			}
		})
	}
}

func TestWorkRequestUsesNormalTaskAdmission(t *testing.T) {
	srv := newTestServer(t)
	creator := &recordingWorkRequestCreator{}
	srv.WorkRequests = creator
	srv.ControlPlane = workflowControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "implement", YAML: "id: implement\nsteps:\n  - type: implement.prepare\n"}}})

	// An inputless workflow keeps taking an inputless request.
	body := `{"identity":"archie","repository":"acme/widget","workflow":"implement","title":"Fix login","instructions":"Reproduce and fix it."}`
	w := postWorkRequest(t, srv, body)

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
	w := postWorkRequest(t, srv, `{"identity":"archie","repository":"acme/widget","workflow":"implement","title":"Fix","instructions":"Do it"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if got := creator.request; got.Title != "" || got.Body != "" || got.Repo != "" || got.Workflow != "" || got.Identity != "" || len(got.Inputs) != 0 {
		t.Fatalf("creator called with %#v", got)
	}
}
