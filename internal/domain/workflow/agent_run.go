package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
	Mission string `yaml:"mission" doc:"What the agent is asked to do. May reference {{ task.* }}, {{ inputs.* }} and {{ steps.<id>.* }}."`
	// Role selects the model (cfg.Models[Role]); empty means builder.
	Role string `yaml:"role" doc:"Which configured model role runs the agent, e.g. builder or planner. Empty means builder."`
	// ReadOnly restricts the agent to read-only tools.
	ReadOnly bool `yaml:"read_only" title:"Read only" doc:"Restrict the agent to read-only tools."`
	// Gate holds the agent to the repository's gate before it may finish:
	// "repository", or "test-failure" where the test command must fail
	// (writing a failing reproduction). Empty means ungated.
	Gate string `yaml:"gate" enum:"repository,test-failure" doc:"Hold the agent to the repository gate before it may finish; test-failure requires the tests to fail."`
	// ProtectTests write-blocks the repository's test files.
	ProtectTests bool `yaml:"protect_tests" title:"Protect tests" doc:"Write-block the repository test files."`
	// ExtraRules is appended to the agent's system prompt.
	ExtraRules string `yaml:"extra_rules" title:"Extra rules" doc:"Appended to the agent system prompt."`
	// Result is a JSON Schema object. When set, the agent must return a value
	// matching it, which later steps read as steps.<id>.result.
	Result map[string]any `yaml:"result" doc:"A JSON Schema object the agent must return; later steps read it as steps.<id>.result."`
}

// AgentRunStepType contributes the agent.run step type.
func AgentRunStepType() StepType {
	return StepType{Name: AgentRunStepName, Factory: newAgentRunStage, Settings: agentRunSettings{}}
}

func newAgentRunStage(settings yaml.Node) (Stage, error) {
	var s agentRunSettings
	if settings.Kind == 0 {
		return Stage{}, fmt.Errorf("%s: settings.mission is required", AgentRunStepName)
	}
	if err := settings.Decode(&s); err != nil {
		return Stage{}, fmt.Errorf("%s: %w", AgentRunStepName, err)
	}
	if err := s.check(); err != nil {
		return Stage{}, fmt.Errorf("%s: %w", AgentRunStepName, err)
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
		Name:       AgentRunStepName,
		Role:       role,
		ReadOnly:   s.ReadOnly,
		Mission:    func(*TaskContext) string { return s.Mission },
		ExtraRules: s.ExtraRules,
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
	applyRepositoryControls(&agent, s)
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

func (s agentRunSettings) check() error {
	if strings.TrimSpace(s.Mission) == "" {
		return fmt.Errorf("settings.mission is required")
	}
	if s.Gate != "" && s.Gate != "repository" && s.Gate != gateExpectTestFailure {
		return fmt.Errorf("settings.gate is repository, test-failure or empty, not %q", s.Gate)
	}
	return nil
}

// applyRepositoryControls sets the agent's gate and write protection.
func applyRepositoryControls(agent *AgentStage, s agentRunSettings) {
	switch s.Gate {
	case "repository":
		agent.Gate = func(tc *TaskContext) agentrun.Gate { return GateFromRepo(tc.Repo, tc.Cfg.Budgets) }
	case gateExpectTestFailure:
		agent.Gate = func(tc *TaskContext) agentrun.Gate { return tddReproGate(tc.Repo, tc.Cfg.Budgets) }
	}
	if s.ProtectTests {
		agent.ProtectGlobs = func(tc *TaskContext) []string {
			if glob := tc.Repo.ResolvedTestGlob(); glob != "" {
				return []string{glob}
			}
			return nil
		}
	}
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
	tool := &agentrun.CaptureTool{
		Name: resultToolName, Description: "Return this step's result. Call exactly once, before finish.",
		Parameters: params, RequiredFields: required, MaxCalls: 1,
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(properties)) {
		property, _ := properties[name].(map[string]any)
		switch property["type"] {
		case "boolean":
			tool.BooleanFields = append(tool.BooleanFields, name)
		case "string":
			if slices.Contains(required, name) {
				tool.NonEmptyStrings = append(tool.NonEmptyStrings, name)
			}
		}
	}
	return tool, nil
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
