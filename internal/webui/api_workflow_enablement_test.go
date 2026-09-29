package webui

import (
	"encoding/json"
	"net/http"
	"testing"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

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
