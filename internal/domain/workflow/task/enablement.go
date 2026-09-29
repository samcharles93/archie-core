package task

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/org"
)

// WorkflowEnablement is the workflow-enablement resource value: which
// workflows each org has turned off. A workflow is enabled for an org unless
// that org lists it, so a newly shipped or added workflow is enabled
// everywhere until an org disables it.
type WorkflowEnablement struct {
	Orgs map[org.OrgID]OrgWorkflows `json:"orgs"`
}

// OrgWorkflows is one org's workflow state.
type OrgWorkflows struct {
	Disabled []string `json:"disabled"`
}

// Enabled reports whether owner may run workflow.
func (e WorkflowEnablement) Enabled(owner org.OrgID, workflow string) bool {
	return !slices.Contains(e.Orgs[owner].Disabled, workflow)
}

// SetEnabled returns e with workflow enabled or disabled for owner.
func (e WorkflowEnablement) SetEnabled(owner org.OrgID, workflow string, enabled bool) WorkflowEnablement {
	orgs := make(map[org.OrgID]OrgWorkflows, len(e.Orgs)+1)
	maps.Copy(orgs, e.Orgs)
	disabled := slices.DeleteFunc(slices.Clone(orgs[owner].Disabled), func(id string) bool { return id == workflow })
	if !enabled {
		disabled = append(disabled, workflow)
	}
	orgs[owner] = OrgWorkflows{Disabled: disabled}
	return WorkflowEnablement{Orgs: orgs}
}

// Validate rejects blank org or workflow IDs and a workflow listed twice.
func (e WorkflowEnablement) Validate() error {
	for owner, state := range e.Orgs {
		if strings.TrimSpace(string(owner)) == "" {
			return fmt.Errorf("workflow enablement: org ID is required")
		}
		seen := make(map[string]bool, len(state.Disabled))
		for _, workflow := range state.Disabled {
			if strings.TrimSpace(workflow) == "" {
				return fmt.Errorf("workflow enablement: org %q: workflow ID is required", owner)
			}
			if seen[workflow] {
				return fmt.Errorf("workflow enablement: org %q lists %q twice", owner, workflow)
			}
			seen[workflow] = true
		}
	}
	return nil
}
