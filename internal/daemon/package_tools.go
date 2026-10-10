package daemon

import (
	"context"
	"slices"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// PackageAuthorities answers what the operator accepted for an installed
// package. A package that is not installed, or not accepted, has none.
type PackageAuthorities interface {
	AcceptedAuthority(ctx context.Context, name string) (storepkg.Authority, error)
}

// toolAllowlist is the tool allowlist a task runs with. nil offers every tool;
// a non-nil list offers exactly its members. A run of a package's workflow is
// bounded by the tools accepted for that package, so a missing, unaccepted or
// unreadable package offers none.
func (d *Daemon) toolAllowlist(ctx context.Context, task *workflow.Task, profileTools []string) []string {
	name := workflow.PackageOf(task.WorkflowDefinitionYAML)
	if name == "" {
		if len(profileTools) == 0 {
			return nil
		}
		return profileTools
	}
	var accepted []string
	if d.PackageAuthorities != nil {
		authority, err := d.PackageAuthorities.AcceptedAuthority(ctx, name)
		if err != nil {
			d.Log.Warn("package authority unavailable; offering no tools", "task", task.ID, "package", name, "err", err)
		} else {
			accepted = authority.Tools
		}
	}
	allowed := []string{}
	for _, tool := range accepted {
		if len(profileTools) == 0 || slices.Contains(profileTools, tool) {
			allowed = append(allowed, tool)
		}
	}
	return allowed
}
