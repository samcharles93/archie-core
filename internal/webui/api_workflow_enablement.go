package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

const workflowEnablementKind = "workflow-enablement"

// workflowEnablement returns which workflows each org has disabled, and the
// resource version a replacement must name.
func (s *Server) workflowEnablement(ctx context.Context) (task.WorkflowEnablement, int64, error) {
	if s.ControlPlane == nil {
		return task.WorkflowEnablement{}, 0, nil
	}
	response, err := s.ControlPlane.Query(ctx, &controlpb.QueryRequest{Kind: workflowEnablementKind})
	if err != nil {
		return task.WorkflowEnablement{}, 0, err
	}
	if response.Resource == nil {
		return task.WorkflowEnablement{}, 0, errors.New("workflow enablement missing")
	}
	var enablement task.WorkflowEnablement
	if err := json.Unmarshal(response.Resource.ValueJson, &enablement); err != nil {
		return task.WorkflowEnablement{}, 0, err
	}
	return enablement, response.Resource.Version, nil
}

type workflowEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// handleWorkflowEnabled enables or disables a workflow for the caller's org
// (org.OrgFromContext).
func (s *Server) handleWorkflowEnabled(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.ControlPlane == nil {
		http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	request, ok := decodeBody[workflowEnabledRequest](w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	found, err := s.hasWorkflowDefinition(r.Context(), id)
	if err != nil {
		http.Error(w, "workflow definitions unavailable", http.StatusServiceUnavailable)
		return
	}
	if !found {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}
	enablement, version, err := s.workflowEnablement(r.Context())
	if err != nil {
		http.Error(w, "workflow enablement unavailable", http.StatusServiceUnavailable)
		return
	}
	value, err := json.Marshal(enablement.SetEnabled(org.OrgFromContext(r.Context()), id, request.Enabled))
	if err != nil {
		http.Error(w, "encode workflow enablement", http.StatusInternalServerError)
		return
	}
	audit, err := webAudit(r.Context())
	if err != nil {
		http.Error(w, "cannot create request ID", http.StatusInternalServerError)
		return
	}
	if _, err := s.ControlPlane.Command(r.Context(), &controlpb.CommandRequest{
		Kind: workflowEnablementKind, Command: "replace", ValueJson: value,
		ExpectedVersion: version, Actor: string(audit.ActorID),
		Source: audit.Source, RequestId: audit.RequestID,
	}); err != nil {
		writeControlPlaneError(w, err)
		return
	}
	writeJSON(w, map[string]any{"id": id, "enabled": request.Enabled})
}
