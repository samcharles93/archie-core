package webui

import (
	"encoding/json"
	"net/http"
	"testing"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// TestWorkflowEnabledWritesTheCallersOrg: enabling a workflow writes the
// caller's org's enablement document, and leaves the other org's exactly as it
// found it -- otherwise a caller in one org toggles another org's workflows.
func TestWorkflowEnabledWritesTheCallersOrg(t *testing.T) {
	// Both orgs have the workflow disabled, so the write has to name the org it
	// turns on rather than the state it happens to leave behind.
	const enablement = `{"orgs":{"default":{"disabled":["implement"]},"acme":{"disabled":["implement"]}}}`
	tests := []struct {
		name  string
		orgID org.OrgID
		want  org.OrgID
		other org.OrgID
	}{
		{name: "a caller resolved into acme", orgID: "acme", want: "acme", other: org.DefaultOrgID},
		{name: "no org on the context is the default org", want: org.DefaultOrgID, other: "acme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			stub := workflowOrgControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "implement", YAML: "id: implement\nsteps:\n  - type: implement.prepare\n"}}}, enablement)
			srv.ControlPlane = stub

			w := doJSONInOrg(t, srv, tt.orgID, http.MethodPut, "/api/workflows/implement/enabled", map[string]any{"enabled": true})
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			if stub.command == nil {
				t.Fatal("the handler wrote no command")
			}
			if got := stub.command.GetOrgId(); got != string(tt.want) {
				t.Errorf("command carried org %q, want %q", got, tt.want)
			}
			var written task.WorkflowEnablement
			if err := json.Unmarshal(stub.command.GetValueJson(), &written); err != nil {
				t.Fatalf("decode written enablement: %v", err)
			}
			if !written.Enabled(tt.want, "implement") {
				t.Errorf("the write left implement disabled for %s", tt.want)
			}
			if written.Enabled(tt.other, "implement") {
				t.Errorf("the write enabled implement for %s, want only the caller's org", tt.other)
			}
		})
	}
}

// Disabling a workflow flags the bindings that target it and refuses new
// ones; enabling it again lifts both.
func TestWorkflowDisableFlagsAndRefusesBindings(t *testing.T) {
	srv := bindingTestServer(t)
	stub, ok := srv.ControlPlane.(*controlPlaneClientStub)
	if !ok {
		t.Fatal("binding test server has no control-plane stub")
	}
	stub.resources = map[string]*controlpb.Resource{
		workflowEnablementKind: {Kind: workflowEnablementKind, Version: 1, ValueJson: []byte(`{"orgs":{}}`)},
	}
	mappingID := seedMapping(t, srv, "m")
	if w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID)); w.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body = %s", w.Code, w.Body)
	}

	setEnabled := func(enabled bool) {
		t.Helper()
		if w := doJSON(t, srv, http.MethodPut, "/api/workflows/implement/enabled", map[string]any{"enabled": enabled}); w.Code != http.StatusOK {
			t.Fatalf("set enabled=%v status = %d; body = %s", enabled, w.Code, w.Body)
		}
	}
	flagged := func() bool {
		t.Helper()
		w := doJSON(t, srv, http.MethodGet, "/api/bindings", nil)
		var body struct {
			Bindings []bindingView `json:"bindings"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Bindings) != 1 {
			t.Fatalf("list bindings: %v; body = %s", err, w.Body)
		}
		return body.Bindings[0].WorkflowDisabled
	}

	setEnabled(false)
	if !flagged() {
		t.Fatal("binding targeting a disabled workflow is not flagged")
	}
	if w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("b", "sentry", mappingID)); w.Code != http.StatusConflict {
		t.Fatalf("create against disabled workflow status = %d, want %d", w.Code, http.StatusConflict)
	}

	setEnabled(true)
	if flagged() {
		t.Fatal("binding still flagged after the workflow was enabled")
	}
	if w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("b", "sentry", mappingID)); w.Code != http.StatusCreated {
		t.Fatalf("create after enabling status = %d; body = %s", w.Code, w.Body)
	}
}

func TestWorkflowEnabledRefusesUnknownWorkflow(t *testing.T) {
	srv := bindingTestServer(t)
	if w := doJSON(t, srv, http.MethodPut, "/api/workflows/missing/enabled", map[string]any{"enabled": false}); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
