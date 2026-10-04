package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// AgentRunStepName is the step type that runs one agent mission. It needs no
// repository: the agent works in whatever workspace the task has, a worktree
// or a scratch directory.
const AgentRunStepName = "agent.run"

// resultToolName is the capture tool through which an agent.run step with a
// result schema returns its structured result.
const resultToolName = "result"

// agentRunSettings are the agent.run step's settings.
type agentRunSettings struct {
	// Mission is what the agent is asked to do. The workflow's inputs are
	// appended to it as structured data.
	Mission string `yaml:"mission"`
	// Role selects the model (cfg.Models[Role]); empty means builder.
	Role string `yaml:"role"`
	// ReadOnly restricts the agent to read-only tools.
	ReadOnly bool `yaml:"read_only"`
	// Gate is "repository" to hold the agent to the repository's gate
	// commands before it may finish; empty means ungated.
	Gate string `yaml:"gate"`
	// Result is a JSON Schema object. When set, the agent must return a value
	// matching it, which later steps read as steps.<id>.result.
	Result map[string]any `yaml:"result"`
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
	if s.Gate != "" && s.Gate != "repository" {
		return Stage{}, fmt.Errorf("%s: settings.gate is repository or empty, not %q", AgentRunStepName, s.Gate)
	}
	resultTool, err := agentResultTool(s.Result)
	if err != nil {
		return Stage{}, fmt.Errorf("%s: settings.result: %w", AgentRunStepName, err)
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
		OnResult: func(tc *TaskContext, res agentrun.Result) error {
			tc.stepResult.Summary = res.Summary
			if resultTool == nil {
				return nil
			}
			calls := res.Captures[resultToolName]
			if len(calls) != 1 {
				return fmt.Errorf("%s returned its result %d times (want exactly once)", AgentRunStepName, len(calls))
			}
			return json.Unmarshal(calls[0], &tc.stepResult.Result)
		},
	}
	if s.Gate == "repository" {
		agent.Gate = func(tc *TaskContext) agentrun.Gate { return GateFromRepo(tc.Repo, tc.Cfg.Budgets) }
	}
	if resultTool != nil {
		agent.CaptureTools = func(*TaskContext) []agentrun.CaptureTool { return []agentrun.CaptureTool{*resultTool} }
	}
	stage := agent.Stage()
	return Stage{Name: AgentRunStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.Dir == "" {
			dir, branch, err := tc.Trees.Prepare(ctx, tc.Task.Owner, tc.Task.Repo, tc.Repo.BaseBranch(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Labels, PrepareFresh)
			if err != nil {
				return fmt.Errorf("%s: workspace: %w", AgentRunStepName, err)
			}
			tc.Dir, tc.Branch = dir, branch
		}
		return stage.Run(ctx, tc)
	}}, nil
}

// agentResultTool is the capture tool for a result schema, or nil for none.
func agentResultTool(schema map[string]any) (*agentrun.CaptureTool, error) {
	if schema == nil {
		return nil, nil
	}
	if schema["type"] != "object" {
		return nil, fmt.Errorf("must be a JSON Schema with type: object")
	}
	params, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var required []string
	if list, ok := schema["required"].([]any); ok {
		for _, field := range list {
			name, ok := field.(string)
			if !ok {
				return nil, fmt.Errorf("required lists field names")
			}
			required = append(required, name)
		}
	}
	return &agentrun.CaptureTool{
		Name: resultToolName, Description: "Return this step's result. Call exactly once, before finish.",
		Parameters: params, RequiredFields: required, MaxCalls: 1,
	}, nil
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
