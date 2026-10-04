package daemon

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// adoptWorkflowIdentity pins the task's workflow and, when the workflow names
// an identity, makes the run act as it. The identity that dispatched the run
// (a binding's, a poll's, or a calling workflow's) needs run on the workflow.
// It runs before anything that depends on the identity, and reports whether
// processing may continue; a refusal parks the task.
func (d *Daemon) adoptWorkflowIdentity(ctx context.Context, task *workflow.Task) bool {
	if err := d.pinWorkflowDefinition(ctx, task); err != nil {
		d.Log.Error("pin workflow definition failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "pin workflow definition: "+err.Error(), pinParkClass(err))
		return false
	}
	iface, err := workflowtask.ParseWorkflowInterface(task.WorkflowDefinitionYAML)
	if err != nil || iface.Identity == "" || iface.Identity == task.Identity {
		// A malformed definition is pinTaskProfile's to report.
		return true
	}
	runner := d.identityFor(&workflow.Task{Identity: iface.Identity})
	if runner == nil {
		d.parkRunningTask(ctx, task.ID, "workflow "+task.Workflow+" runs as identity "+iface.Identity+", which is not configured", taskstate.ParkNeedsHuman)
		return false
	}
	if !d.mayRunWorkflow(ctx, task) {
		d.parkRunningTask(ctx, task.ID, "the dispatching identity may not run workflow "+task.Workflow, taskstate.ParkNeedsHuman)
		return false
	}
	task.Identity = runner.Name
	if err := d.Store.Update(ctx, task); err != nil {
		d.parkRunningTask(ctx, task.ID, "persist workflow identity: "+err.Error(), taskstate.ParkTransient)
		return false
	}
	return true
}

// mayRunWorkflow asks the policy chain whether the task's current identity
// may run its workflow, recording a denial. No chain allows it, as every
// other check on an install that has not built one.
func (d *Daemon) mayRunWorkflow(ctx context.Context, task *workflow.Task) bool {
	if d.Access == nil || d.Principals == nil {
		return true
	}
	principal, err := d.Principals.PrincipalFor(ctx, d.dispatcherID(task))
	if err != nil {
		d.Log.Warn("workflow identity: principal unavailable", "task", task.ID, "err", err)
		return false
	}
	resource := access.Resource{Kind: access.KindWorkflow, ID: task.Workflow, Org: principal.Org}
	decision := d.Access.Authorize(principal, access.ActionRun, resource, access.Context{})
	if decision.Allowed {
		return true
	}
	if d.Denials != nil {
		denial := access.Denial{
			Principal: principal.IdentityID, Org: principal.Org, Action: access.ActionRun,
			Kind: resource.Kind, ResourceID: resource.ID, Level: decision.Level, Policies: decision.Policies,
		}
		if err := d.Denials.RecordDenial(ctx, denial); err != nil {
			d.Log.Warn("workflow identity: record denial", "task", task.ID, "err", err)
		}
	}
	return false
}

// dispatcherID is the identity ID the task currently acts as: its configured
// identity's, else the root's.
func (d *Daemon) dispatcherID(task *workflow.Task) identity.IdentityID {
	if runner := d.identityFor(task); runner != nil {
		return runner.ID
	}
	return d.RootIdentityID
}
