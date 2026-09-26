package task

import "github.com/samcharles93/archie-core/internal/taskstate"

// The step-execution vocabulary of docs/prds/execution-tree-state-machine.md:
// one stage run, or one agent or workflow.call step an execution's tree
// records, started and finished through the State Store under the run's own
// credential.

// Step kinds. `stage` is what workflow.Run records for every stage it runs;
// `agent` is what the agent runtime records for one agent call; `call` is the
// workflow.call step in a caller's tree, which names its callee's execution.
// A new kind needs a named consumer before it is added.
const (
	StepKindStage = "stage"
	StepKindAgent = "agent"
	StepKindCall  = "call"
)

// ValidStepKind reports whether kind is one the store records.
func ValidStepKind(kind string) bool {
	switch kind {
	case StepKindStage, StepKindAgent, StepKindCall:
		return true
	default:
		return false
	}
}

// StepStart names the step a caller is starting. ExecutionID is the
// WorkflowExecution (the tasks row); Attempt is the run of it the step
// belongs to; ParentID is 0 for a stage and names the enclosing StepExecution
// otherwise, from which the store derives depth.
type StepStart struct {
	ExecutionID int64
	Attempt     int
	// ParentID is 0 when the step is a stage at the tree's root.
	ParentID int64
	Kind     string
	Name     string
}

// StepFinish moves one step from From to its outcome. ExecutionID names the
// run the step belongs to: the task-scoped grant check verifies it, and the
// store verifies the step really is that execution's, so a caller cannot move
// another run's step by naming it.
type StepFinish struct {
	StepID      int64
	ExecutionID int64
	// From and To are the step transition table's statuses (taskstate).
	From taskstate.StepStatus
	To   taskstate.StepStatus
	// Detail is the step's outcome line: an error's text for a failed step, a
	// summary otherwise. Clipped store-side like every detail.
	Detail string
	// TokensUsed is the usage the step itself accounted. Stages record none;
	// the agent runtime records its own calls' usage.
	TokensUsed int64
}
