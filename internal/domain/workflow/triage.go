package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// triageWorkflowNames is the set of workflows triage may hand a task to.
// A name outside it is refused, not defaulted -- see Triage's OnResult.
var triageWorkflowNames = map[string]bool{
	"implement":   true,
	"tdd":         true,
	"feasibility": true,
}

// Triage is the cheap-classification workflow: one read-only agent turn
// decides whether a task needs a code change at all and, if so, which
// workflow suits it best. See docs/prds/dynamic-workflow-triage.md.
//
// It exists because Route (workflow.go) previously had no content-aware
// fallback: every task with no explicit workflow and no label match ran
// the full "implement" pipeline regardless of what it actually asked for.
// A chat-spawned administrative request ("this is just a test, close it")
// cost 669,421 tokens finding that out the expensive way (baseline, plan,
// and build all ran before concluding nothing needed to change).
//
// StagePrepareWorktree still runs first: AgentStage mounts tc.Dir into the
// agent container (agent.go's handleResult passes it straight through),
// and no cheaper directory-less classification primitive exists in this
// codebase. The savings are in what triage skips, not in avoiding a
// worktree checkout.
func Triage() Workflow {
	return Workflow{
		Name: "triage",
		Stages: []Stage{
			StagePrepareWorktree(),

			AgentStage{
				Name:         "classify",
				Role:         "planner",
				ReadOnly:     true,
				CaptureTools: triageDecideCaptureTools,
				Mission:      triageMission,
				OnResult: func(tc *TaskContext, res agentexec.Result) error {
					calls := res.Captures["decide"]
					if len(calls) != 1 {
						return fmt.Errorf("triage classify stage called the decide tool %d times (want exactly once)", len(calls))
					}
					var captured struct {
						NeedsCodeChange *bool  `json:"needs_code_change"`
						Workflow        string `json:"workflow"`
						Reasons         string `json:"reasons"`
					}
					if err := json.Unmarshal(calls[0], &captured); err != nil {
						return fmt.Errorf("decode triage decision: %w", err)
					}
					if captured.NeedsCodeChange == nil {
						return fmt.Errorf("triage classify stage decision has no boolean needs_code_change value")
					}
					if !*captured.NeedsCodeChange {
						// OnResult has no ctx of its own (matches
						// feasibility.go's identical constraint); Forge
						// calls build their own timeout from what they're
						// given, same as feasibility.go's CloseIssue call.
						if tc.Task.IsForgeBacked() {
							if err := tc.Forge.CloseIssue(context.Background(), tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, ""); err != nil {
								return err
							}
						}
						tc.Outcome = Outcome{Status: StatusCompleted, Detail: "triaged: no code change needed  --  " + captured.Reasons}
						return nil
					}
					// No fallback. The decide tool already refuses a call that
					// needs a code change and names no workflow, so reaching
					// here with an unrecognised one means the model ignored
					// its own enum; defaulting that to implement is how an
					// unsettled capability used to reach the builder.
					target := captured.Workflow
					if !triageWorkflowNames[target] {
						return fmt.Errorf("triage chose workflow %q, which is not one of implement, tdd or feasibility", target)
					}
					tc.Task.Workflow = target
					tc.Outcome = Outcome{
						Status: StatusQueued,
						Detail: fmt.Sprintf("triaged to %s: %s", target, captured.Reasons),
					}
					return nil
				},
			}.Stage(),
		},
	}
}

// triageDecideCaptureTools gives the classify agent a structured verdict
// tool, mirroring feasibility.go's decideCaptureTools.
func triageDecideCaptureTools(*TaskContext) []agentexec.CaptureTool {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"needs_code_change": {"type": "boolean", "description": "false: nothing to build -- close/no-op. true: route to a workflow that builds something."},
			"workflow": {"type": "string", "enum": ["implement", "tdd", "feasibility"], "description": "Which workflow fits best, when needs_code_change is true. feasibility: a new capability whose design is not settled, or a request too broad to scope -- it produces a design document for a human to approve before any code is written. tdd: a defect with an observable wrong behaviour that a test can reproduce first. implement: the change is well understood and its shape is already clear. Choose feasibility when no approved design exists and the request is not a defect; do not choose implement merely because nothing else obviously fits. Defaults to implement if omitted or unrecognized."},
			"reasons": {"type": "string", "description": "The rationale, written for the human who filed the request."}
		},
		"required": ["needs_code_change", "reasons"]
	}`)
	return []agentexec.CaptureTool{{
		Name: "decide", Description: "Record the triage verdict. Call exactly once, before finish.",
		Parameters: params, RequiredFields: []string{"needs_code_change", "reasons"},
		NonEmptyStrings: []string{"reasons"}, BooleanFields: []string{"needs_code_change"},
		// Conditional, not flat: a task needing no code change has no
		// workflow to name, and forcing one would be a meaningless answer.
		RequiredWhenTrue: map[string][]string{"needs_code_change": {"workflow"}},
		MaxCalls:         1,
	}}
}

// triageMission is the classify stage's prompt. It is a named function so a
// test can assert the selection criteria are actually stated to the model,
// which is the whole of this stage's behaviour.
func triageMission(tc *TaskContext) string {
	return fmt.Sprintf(
		"Triage this %s on the repository %s: decide whether it needs a code "+
			"change at all, and if so which workflow suits it best.\n\n"+
			"%s\n\n"+
			"Read only as much as you need to judge this -- a title/body that is "+
			"purely conversational, administrative, or already resolved needs no code "+
			"change.\n\n"+
			"If it does need a change, the workflow is a real decision, not a "+
			"formality. Check whether the repository already has a settled design "+
			"for what is being asked (a design document, an approved plan, an "+
			"existing implementation to extend). If it does not, and the request "+
			"is not a defect, choose feasibility: it writes a design document for "+
			"a human to approve, which is cheaper than an implementation built on "+
			"a guess. Choose tdd when there is an observable wrong behaviour a "+
			"test can reproduce first. Choose implement only when the change is "+
			"well understood and its shape is already clear.\n\n"+
			"Then call the decide tool EXACTLY ONCE and afterwards call finish "+
			"with status \"passed\".",
		taskKind(tc.Task), tc.Repo.FullName(), taskPromptBlock(tc.Task),
	)
}
