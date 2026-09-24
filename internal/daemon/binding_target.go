package daemon

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// bindingTarget is what one binding dispatch starts: the repository the task
// works on (empty for none) and the workflow inputs it carries.
type bindingTarget struct {
	owner, repo string
	inputs      map[string]any
	// declared is whether the workflow declares inputs; such a task's body is
	// provenance only, since its data travels as inputs.
	declared bool
}

// activeWorkflows returns the definitions a dispatch cycle checks bindings
// against, the same set a task is later pinned from.
func (d *Daemon) activeWorkflows(ctx context.Context) (workflow.WorkflowDefinitionCollection, error) {
	if d.WorkflowDefinitions == nil {
		return workflow.ShippedDefinitions(), nil
	}
	collection, _, err := d.WorkflowDefinitions.WorkflowDefinitions(ctx)
	return collection, err
}

// resolveBindingTarget checks a matched binding against the workflow it
// targets and resolves the task's repository and inputs. A non-empty reason
// means the binding does not dispatch and the reason is recorded on it; ok
// false with no reason means resolveBindingRepo already logged why.
func (d *Daemon) resolveBindingTarget(b binding.Binding, values map[string]any, workflows workflow.WorkflowDefinitionCollection) (target bindingTarget, reason string, ok bool) {
	entry, found := workflows.DefinitionByID(b.Workflow)
	if !found {
		return bindingTarget{}, fmt.Sprintf("workflow %q is not defined", b.Workflow), false
	}
	iface, err := workflowtask.ParseWorkflowInterface(entry.YAML)
	if err != nil {
		return bindingTarget{}, fmt.Sprintf("workflow %q: %v", b.Workflow, err), false
	}
	target.inputs = b.ResolveInputs(values)
	target.declared = len(iface.Inputs) > 0
	if err := iface.CheckInputs(target.inputs); err != nil {
		return bindingTarget{}, "inputs do not match the workflow: " + err.Error(), false
	}
	switch mode := iface.RepositoryMode(); {
	case mode == workflowtask.RepositoryNone:
		return target, "", true
	case b.RepoParam != "":
		owner, repo, _, err := b.RepositoryFrom(values)
		if err != nil {
			return bindingTarget{}, err.Error(), false
		}
		if !d.repoConfigured(owner, repo) {
			return bindingTarget{}, fmt.Sprintf("repository %s/%s from the event is not a configured repository", owner, repo), false
		}
		target.owner, target.repo = owner, repo
		return target, "", true
	case mode == workflowtask.RepositoryOptional && b.Owner == "":
		return target, "", true
	}
	owner, repo, resolved := d.resolveBindingRepo(b)
	if !resolved {
		return bindingTarget{}, "", false
	}
	target.owner, target.repo = owner, repo
	return target, "", true
}

// repoConfigured reports whether owner/repo is one of the configured repos. A
// repository named by an event must be one, so a payload cannot point an
// agent at an arbitrary repository.
func (d *Daemon) repoConfigured(owner, repo string) bool {
	for _, r := range d.Cfg.Get().Repos {
		if r.Owner == owner && r.Name == repo {
			return true
		}
	}
	return false
}
