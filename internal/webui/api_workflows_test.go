package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
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

// workflowOrgControlPlane is the control-plane stub holding the two resources
// the workflows handlers read together -- the definition collection and the
// enablement document -- so a test can read back the org each was asked for.
func workflowOrgControlPlane(t *testing.T, definitions workflow.WorkflowDefinitionCollection, enablement string) *controlPlaneClientStub {
	t.Helper()
	value, err := json.Marshal(definitions)
	if err != nil {
		t.Fatal(err)
	}
	return &controlPlaneClientStub{resources: map[string]*controlpb.Resource{
		workflowDefinitionsKind: {Kind: workflowDefinitionsKind, Version: 1, ValueJson: value},
		workflowEnablementKind:  {Kind: workflowEnablementKind, Version: 1, ValueJson: []byte(enablement)},
	}}
}

// doJSONInOrg issues a request acting in orgID, or in no org at all when orgID
// is empty. The org travels on the request context (org.WithOrg) exactly as
// authorize attaches the verified principal's, so a test reads back the org a
// handler acts in the way production puts it there.
func doJSONInOrg(t *testing.T, srv *Server, orgID org.OrgID, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	ctx := t.Context()
	if orgID != "" {
		ctx = org.WithOrg(ctx, orgID)
	}
	r := httptest.NewRequestWithContext(ctx, method, path, reader)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
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

// TestWorkflowDefinitionsPathActsInTheCallersOrg: the definitions listing and
// the enablement read beside it both act in the caller's org. A handler that
// reads the definitions in the caller's org while reading the enablement in the
// default org reports another org's enablement as the caller's own.
func TestWorkflowDefinitionsPathActsInTheCallersOrg(t *testing.T) {
	// acme has disabled custom; the default org has not.
	const enablement = `{"orgs":{"acme":{"disabled":["custom"]}}}`
	tests := []struct {
		name        string
		orgID       org.OrgID
		want        org.OrgID
		wantEnabled bool
	}{
		{name: "a caller resolved into acme", orgID: "acme", want: "acme"},
		{name: "no org on the context is the default org", want: org.DefaultOrgID, wantEnabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			stub := workflowOrgControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "custom", YAML: "id: custom\nsteps:\n  - type: implement.prepare\n"}}}, enablement)
			srv.ControlPlane = stub

			w := doJSONInOrg(t, srv, tt.orgID, http.MethodGet, "/api/workflows", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			queried := map[string]bool{}
			for _, request := range stub.queries {
				queried[request.GetKind()] = true
				if got := request.GetOrgId(); got != string(tt.want) {
					t.Errorf("%s query carried org %q, want %q", request.GetKind(), got, tt.want)
				}
			}
			if !queried[workflowDefinitionsKind] || !queried[workflowEnablementKind] {
				t.Fatalf("definitions path queried %v, want both %s and %s", queried, workflowDefinitionsKind, workflowEnablementKind)
			}
			var response struct {
				Definitions []workflow.Definition `json:"definitions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Definitions) != 1 || response.Definitions[0].Enabled != tt.wantEnabled {
				t.Fatalf("definitions = %#v, want custom enabled=%v", response.Definitions, tt.wantEnabled)
			}
		})
	}
}

// TestWorkRequestEnablementReadsTheCallersOrg: whether a workflow may run is
// the caller's org's answer, so the org that disabled pr-review cannot run it
// while an org that did not can.
func TestWorkRequestEnablementReadsTheCallersOrg(t *testing.T) {
	const enablement = `{"orgs":{"acme":{"disabled":["pr-review"]}}}`
	request := map[string]any{
		"identity": "archie", "repository": "acme/widget", "workflow": "pr-review",
		"title": "Review 77", "instructions": "Review it.", "inputs": map[string]any{"pr_number": 77},
	}
	tests := []struct {
		name     string
		orgID    org.OrgID
		want     org.OrgID
		wantCode int
	}{
		{name: "a caller in the org that disabled it", orgID: "acme", want: "acme", wantCode: http.StatusConflict},
		{name: "a caller in the org that kept it enabled", want: org.DefaultOrgID, wantCode: http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			creator := &recordingWorkRequestCreator{}
			srv.WorkRequests = creator
			stub := workflowOrgControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "pr-review", YAML: declaredInputsDefinition}}}, enablement)
			srv.ControlPlane = stub

			w := doJSONInOrg(t, srv, tt.orgID, http.MethodPost, "/api/work-requests", request)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, body = %s, want %d", w.Code, w.Body, tt.wantCode)
			}
			for _, query := range stub.queries {
				if got := query.GetOrgId(); got != string(tt.want) {
					t.Errorf("%s query carried org %q, want %q", query.GetKind(), got, tt.want)
				}
			}
			if admitted := creator.request.Title != ""; admitted != (tt.wantCode == http.StatusCreated) {
				t.Errorf("task admitted = %v, want %v", admitted, tt.wantCode == http.StatusCreated)
			}
		})
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
