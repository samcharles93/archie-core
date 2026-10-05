package workflow

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

func TestValidateControlTargets(t *testing.T) {
	registry := StepRegistry{
		AgentRunStepName:     newAgentRunStage,
		FinishStepName:       newFinishStage,
		HandoffStepName:      newHandoffStage,
		ApproveStepName:      newApproveStage,
		WorkflowCallStepName: newWorkflowCallStage,
	}
	defined := `id: defined
repository: none
steps:
  - type: workflow.finish
`
	tests := []struct {
		name    string
		defs    map[string]string
		wantErr string
	}{
		{
			name: "a literal handoff to a defined workflow is accepted",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - type: workflow.handoff
    settings: {workflow: defined}
`,
				"defined": defined,
			},
		},
		{
			name: "an approve target to a defined workflow is accepted",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - type: human.approve
    settings: {plan: p, then: defined}
`,
				"defined": defined,
			},
		},
		{
			name: "an unknown literal handoff is refused naming the step",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - type: workflow.finish
  - type: workflow.handoff
    settings: {workflow: missing}
`,
			},
			wantErr: `workflow "source" step 2 targets "missing", which is not defined`,
		},
		{
			name: "an unknown approve target is refused naming the step",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - type: human.approve
    settings: {plan: p, then: missing}
`,
			},
			wantErr: `workflow "source" step 1 targets "missing", which is not defined`,
		},
		{
			name: "a target needing a repository the task does not have is refused",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - type: workflow.handoff
    settings: {workflow: defined}
`,
				"defined": `id: defined
steps:
  - type: workflow.finish
`,
			},
			wantErr: `which needs a repository the task does not have`,
		},
		{
			name: "a target requiring an input the task may not carry is refused",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - type: workflow.handoff
    settings: {workflow: defined}
`,
				"defined": `id: defined
repository: none
inputs:
  pr_number: {type: number, required: true}
steps:
  - type: workflow.finish
`,
			},
			wantErr: `which requires input "pr_number" the task may not carry`,
		},
		{
			name: "a target that drops an input the task carries is refused",
			defs: map[string]string{
				"source": `id: source
repository: none
inputs:
  plan: {type: string, required: true}
steps:
  - type: workflow.handoff
    settings: {workflow: defined}
`,
				"defined": defined,
			},
			wantErr: `which does not declare input "plan" the task carries`,
		},
		{
			name: "an enum target whose values are all defined is accepted",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - id: classify
    type: agent.run
    settings:
      mission: m
      read_only: true
      result:
        type: object
        properties:
          workflow: {type: string, enum: [alpha, beta]}
  - type: workflow.handoff
    settings: {workflow: "{{ steps.classify.result.workflow }}"}
`,
				"alpha": strings.Replace(defined, "id: defined", "id: alpha", 1),
				"beta":  strings.Replace(defined, "id: defined", "id: beta", 1),
			},
		},
		{
			name: "an enum target with an undefined value is refused",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - id: classify
    type: agent.run
    settings:
      mission: m
      read_only: true
      result:
        type: object
        properties:
          workflow: {type: string, enum: [alpha, missing]}
  - type: workflow.handoff
    settings: {workflow: "{{ steps.classify.result.workflow }}"}
`,
				"alpha": strings.Replace(defined, "id: defined", "id: alpha", 1),
			},
			wantErr: `targets "missing", which is not defined`,
		},
		{
			name: "a templated target without an enum is not checked",
			defs: map[string]string{
				"source": `id: source
repository: none
steps:
  - id: classify
    type: agent.run
    settings:
      mission: m
      read_only: true
      result:
        type: object
        properties:
          workflow: {type: string}
  - type: workflow.handoff
    settings: {workflow: "{{ steps.classify.result.workflow }}"}
`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collection := task.WorkflowDefinitionCollection{}
			for _, id := range slices.Sorted(maps.Keys(tt.defs)) {
				collection.Definitions = append(collection.Definitions, task.WorkflowDefinitionEntry{ID: id, YAML: tt.defs[id]})
			}
			err := ValidateDefinitionCollection(collection, registry)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
