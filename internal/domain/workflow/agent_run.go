package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// AgentRunStepName is the step type that runs one agent mission and finishes
// the workflow with its summary. It needs no repository: the agent works in
// whatever workspace the task has, a worktree or a scratch directory.
const AgentRunStepName = "agent.run"

// agentRunSettings are the agent.run step's settings.
type agentRunSettings struct {
	// Mission is what the agent is asked to do. The workflow's inputs are
	// appended to it as structured data.
	Mission string `yaml:"mission"`
	// Role selects the model (cfg.Models[Role]); empty means builder.
	Role string `yaml:"role"`
	// ReadOnly restricts the agent to read-only tools.
	ReadOnly bool `yaml:"read_only"`
}

// AgentRunStepType contributes the agent.run step type.
func AgentRunStepType() StepType {
	return StepType{Name: AgentRunStepName, Factory: newAgentRunStage}
}

func newAgentRunStage(settings yaml.Node) (Stage, error) {
	var s agentRunSettings
	if settings.Kind == 0 {
		return Stage{}, fmt.Errorf("%s: settings.mission is required", AgentRunStepName)
	}
	if err := settings.Decode(&s); err != nil {
		return Stage{}, fmt.Errorf("%s: %w", AgentRunStepName, err)
	}
	if strings.TrimSpace(s.Mission) == "" {
		return Stage{}, fmt.Errorf("%s: settings.mission is required", AgentRunStepName)
	}
	role := s.Role
	if role == "" {
		role = "builder"
	}
	agent := AgentStage{
		Name:     AgentRunStepName,
		Role:     role,
		ReadOnly: s.ReadOnly,
		Mission:  func(*TaskContext) string { return s.Mission },
		OnResult: func(tc *TaskContext, res agentexec.Result) error {
			tc.Outcome = Outcome{Status: StatusCompleted, Detail: res.Summary}
			return nil
		},
	}.Stage()
	return Stage{Name: AgentRunStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.Dir == "" {
			dir, branch, err := tc.Trees.Prepare(ctx, tc.Task.Owner, tc.Task.Repo, tc.Repo.BaseBranch(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Labels)
			if err != nil {
				return fmt.Errorf("%s: workspace: %w", AgentRunStepName, err)
			}
			tc.Dir, tc.Branch = dir, branch
		}
		return agent.Run(ctx, tc)
	}}, nil
}

// missionWithInputs appends the task's workflow inputs to a mission as a JSON
// block, so the agent reads them as structured data rather than prose.
func missionWithInputs(t *Task, mission string) string {
	if t == nil || len(t.Inputs) == 0 {
		return mission
	}
	data, err := json.MarshalIndent(t.Inputs, "", "  ")
	if err != nil {
		return mission
	}
	return mission + "\n\nWorkflow inputs (JSON):\n```json\n" + string(data) + "\n```"
}
