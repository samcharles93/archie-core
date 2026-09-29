package controlplane

import (
	"context"
	"encoding/json"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

const WorkflowEnablementKind = "workflow-enablement"

func workflowEnablementDefinition() Definition {
	return Definition{
		Kind:      WorkflowEnablementKind,
		Title:     "Workflow enablement",
		Document:  task.WorkflowEnablement{},
		ApplyMode: "live",
		Seed:      func(config.Config) any { return task.WorkflowEnablement{Orgs: map[org.OrgID]task.OrgWorkflows{}} },
		Validate: func(input []byte) error {
			return validateAs(input, func(enablement task.WorkflowEnablement) error { return enablement.Validate() })
		},
	}
}

// WorkflowEnablement returns which workflows each org has disabled.
func (c *Client) WorkflowEnablement(ctx context.Context) (task.WorkflowEnablement, error) {
	response, err := c.rpc.Query(ctx, controlplanerpc.QueryRequest(ctx, WorkflowEnablementKind))
	if err != nil {
		return task.WorkflowEnablement{}, controlplanerpc.ClientError(err)
	}
	var enablement task.WorkflowEnablement
	err = json.Unmarshal(response.Resource.ValueJson, &enablement)
	return enablement, err
}
