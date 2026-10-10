package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// startTriggers names how a task was started, in the vocabulary a package's
// accepted authority lists: "source:<source>" for where the task came from,
// "call" for a workflow.call, "binding:<id>" for a binding dispatch and
// "label:<name>" for each issue label.
func startTriggers(task *workflow.Task) []string {
	source := task.Source
	if source == "" {
		source = workflow.SourceForge
	}
	triggers := []string{"source:" + source}
	if task.CallParentTaskID != 0 {
		triggers = append(triggers, "call")
	}
	if task.BindingID != "" {
		triggers = append(triggers, "binding:"+task.BindingID)
	}
	for label := range strings.SplitSeq(task.Labels, ",") {
		if label = strings.TrimSpace(label); label != "" {
			triggers = append(triggers, "label:"+label)
		}
	}
	return triggers
}

// checkPackageTrigger refuses to run a package's workflow unless one of the
// ways the task started is a trigger accepted for that package. A missing,
// unaccepted or unreadable package accepts none. Operator workflows are not
// bounded here.
func (d *Daemon) checkPackageTrigger(ctx context.Context, task *workflow.Task) error {
	name := workflow.PackageOf(task.WorkflowDefinitionYAML)
	if name == "" {
		return nil
	}
	if d.PackageAuthorities == nil {
		return fmt.Errorf("package %s: no accepted authority is available", name)
	}
	authority, err := d.PackageAuthorities.AcceptedAuthority(ctx, name)
	if err != nil {
		return fmt.Errorf("package %s: accepted authority unavailable: %w", name, err)
	}
	started := startTriggers(task)
	for _, accepted := range authority.Triggers {
		if slices.Contains(started, accepted) {
			return nil
		}
	}
	return fmt.Errorf("package %s: none of the triggers that started this task (%s) is accepted", name, strings.Join(started, ", "))
}
