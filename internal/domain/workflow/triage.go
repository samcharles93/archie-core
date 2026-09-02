package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/store"
)

// triageWorkflowNames is the set of workflows triage may hand a task to.
// Unrecognized or missing classifier output falls back to "implement" --
// see triageDecideCaptureTools' schema description and Triage's OnResult.
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
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Triage this %s on the repository %s: decide whether it needs a code "+
							"change at all, and if so which workflow suits it best.\n\n"+
							"%s\n\n"+
							"Read only as much as you need to judge this -- a title/body that is "+
							"purely conversational, administrative, or already resolved needs no code "+
							"change. Then call the decide tool EXACTLY ONCE and afterwards call finish "+
							"with status \"passed\".",
						taskKind(tc.Task), tc.Repo.FullName(), taskPromptBlock(tc.Task),
					)
				},
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
						tc.Outcome = Outcome{Status: store.StatusMerged, Detail: "triaged: no code change needed  --  " + captured.Reasons}
						return nil
					}
					target := captured.Workflow
					if !triageWorkflowNames[target] {
						target = "implement"
					}
					tc.Task.Workflow = target
					tc.Outcome = Outcome{
						Status: store.StatusQueued,
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
			"workflow": {"type": "string", "enum": ["implement", "tdd", "feasibility"], "description": "Which workflow fits best, when needs_code_change is true. Defaults to implement if omitted or unrecognized."},
			"reasons": {"type": "string", "description": "The rationale, written for the human who filed the request."}
		},
		"required": ["needs_code_change", "reasons"]
	}`)
	return []agentexec.CaptureTool{{
		Name: "decide", Description: "Record the triage verdict. Call exactly once, before finish.",
		Parameters: params, RequiredFields: []string{"needs_code_change", "reasons"},
		NonEmptyStrings: []string{"reasons"}, BooleanFields: []string{"needs_code_change"}, MaxCalls: 1,
	}}
}
