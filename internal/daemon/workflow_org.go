package daemon

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// workflowContext forwards a task's identity so the State Store selects its
// org's definitions. An identity assigned elsewhere cannot supply the pin.
func (d *Daemon) workflowContext(ctx context.Context, task *workflow.Task) (context.Context, error) {
	owner := task.Org
	if owner == "" {
		owner = org.DefaultOrgID
	}
	if d.Principals == nil {
		principal, forwarded := access.PrincipalFromContext(ctx)
		if owner != org.DefaultOrgID || org.OrgFromContext(ctx) != owner || (forwarded && principal.Org != "" && principal.Org != owner) {
			return nil, pinFailure{taskstate.ParkNeedsHuman, fmt.Errorf("workflow org %s: principal unavailable", owner)}
		}
		return ctx, nil
	}
	principal, err := d.Principals.PrincipalFor(ctx, d.publisherIdentity(task.Identity))
	if err != nil {
		return nil, pinFailure{taskstate.ParkTransient, fmt.Errorf("workflow principal: %w", err)}
	}
	if principal.Org == "" {
		principal.Org = org.DefaultOrgID
	}
	if principal.Org != owner {
		return nil, pinFailure{taskstate.ParkNeedsHuman, fmt.Errorf("workflow identity belongs to org %s, task belongs to %s", principal.Org, owner)}
	}
	return org.WithOrg(access.WithPrincipal(ctx, principal), owner), nil
}
