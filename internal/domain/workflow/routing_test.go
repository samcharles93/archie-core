package workflow

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/store"
)

func TestRouteDoesNotSilentlyReplaceAnAdmittedWorkflow(t *testing.T) {
	task := &store.Task{Workflow: "skill-review"}
	wf := Route(task, Registry{"implement": Implement()})
	if wf.Name != "none" || len(wf.Stages) != 1 || wf.Stages[0].Name != "fail" {
		t.Fatalf("Route() = %#v, want explicit failure workflow", wf)
	}
}
