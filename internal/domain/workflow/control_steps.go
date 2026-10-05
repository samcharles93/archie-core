package workflow

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// Control step types: they decide where the task goes next.
const (
	HandoffStepName    = "workflow.handoff"
	ApproveStepName    = "human.approve"
	CloseIssueStepName = "forge.close-issue"
	CommentStepName    = "forge.comment"
)

// ControlStepTypes contributes the control and forge step types.
func ControlStepTypes() []StepType {
	return []StepType{
		{Name: HandoffStepName, Factory: newHandoffStage, Settings: handoffSettings{}},
		{Name: ApproveStepName, Factory: newApproveStage, Settings: approveSettings{}},
		{Name: CloseIssueStepName, Factory: newCloseIssueStage},
		{Name: CommentStepName, Factory: newCommentStage, Settings: commentSettings{}},
	}
}

type handoffSettings struct {
	Workflow string `yaml:"workflow" doc:"The workflow the task is requeued under."`
	Detail   string `yaml:"detail" doc:"Why the task was handed off."`
}

// newHandoffStage requeues the task under another workflow: the same task,
// not a child run, so its history stays in one place.
func newHandoffStage(settings yaml.Node) (Stage, error) {
	var s handoffSettings
	if err := decodeSettings(HandoffStepName, settings, &s); err != nil {
		return Stage{}, err
	}
	if strings.TrimSpace(s.Workflow) == "" {
		return Stage{}, fmt.Errorf("%s: settings.workflow is required", HandoffStepName)
	}
	return Stage{Name: HandoffStepName, Run: func(_ context.Context, tc *TaskContext) error {
		tc.Task.Workflow = strings.TrimSpace(s.Workflow)
		tc.Outcome = Outcome{Status: StatusQueued, Detail: s.Detail}
		return nil
	}}, nil
}

type approveSettings struct {
	// Plan is what the human decides on. It is kept on the task, and the
	// workflow named by Then reads it as task.plan.
	Plan string `yaml:"plan" doc:"What the human decides on; the next workflow reads it as task.plan."`
	// Then is the workflow the task is requeued under once approved.
	Then string `yaml:"then" doc:"The workflow the task runs once approved."`
}

// newApproveStage delivers the plan to the decision surfaces and waits: the
// task parks in waiting_human until an operator approves or rejects it.
func newApproveStage(settings yaml.Node) (Stage, error) {
	var s approveSettings
	if err := decodeSettings(ApproveStepName, settings, &s); err != nil {
		return Stage{}, err
	}
	if strings.TrimSpace(s.Plan) == "" || strings.TrimSpace(s.Then) == "" {
		return Stage{}, fmt.Errorf("%s: settings.plan and settings.then are required", ApproveStepName)
	}
	return Stage{Name: ApproveStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		tc.Task.Plan = s.Plan
		notify(ctx, tc, "approval")
		tc.Task.Workflow = s.Then
		tc.Outcome = Outcome{Status: StatusWaitingHuman, Detail: "plan delivered, awaiting go/no-go"}
		return nil
	}}, nil
}

func newCloseIssueStage(yaml.Node) (Stage, error) {
	return Stage{Name: CloseIssueStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		if !tc.Task.IsForgeBacked() {
			return nil
		}
		return tc.Forge.CloseIssue(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, "")
	}}, nil
}

type commentSettings struct {
	Body string `yaml:"body" doc:"The comment text."`
	// On is "issue" (the default) or "pr".
	On string `yaml:"on" enum:"issue,pr" doc:"Comment on the issue or the pull request. Empty means issue."`
}

func newCommentStage(settings yaml.Node) (Stage, error) {
	var s commentSettings
	if err := decodeSettings(CommentStepName, settings, &s); err != nil {
		return Stage{}, err
	}
	if strings.TrimSpace(s.Body) == "" {
		return Stage{}, fmt.Errorf("%s: settings.body is required", CommentStepName)
	}
	if s.On != "" && s.On != "issue" && s.On != "pr" {
		return Stage{}, fmt.Errorf("%s: settings.on is issue or pr, not %q", CommentStepName, s.On)
	}
	return Stage{Name: CommentStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		number := tc.Task.IssueNumber
		if s.On == "pr" {
			number = tc.Task.EffectivePRNumber()
		}
		if !tc.Task.IsForgeBacked() || number == 0 {
			return nil
		}
		_, err := tc.Forge.Comment(ctx, tc.Task.Owner, tc.Task.Repo, number, s.Body)
		return err
	}}, nil
}

// condition is a step's when: a reference that must be truthy, optionally
// negated with !, or compared with == or != to a literal.
type condition struct {
	path     string
	negate   bool
	operator string
	operand  string
}

func parseCondition(source string) (condition, error) {
	source = strings.TrimSpace(source)
	for _, operator := range []string{"==", "!="} {
		if left, right, ok := strings.Cut(source, operator); ok {
			return condition{path: strings.TrimSpace(left), operator: operator, operand: strings.Trim(strings.TrimSpace(right), `"'`)}, nil
		}
	}
	c := condition{path: source}
	if rest, ok := strings.CutPrefix(source, "!"); ok {
		c = condition{path: strings.TrimSpace(rest), negate: true}
	}
	if c.path == "" {
		return condition{}, fmt.Errorf("when names nothing")
	}
	return c, nil
}

func (c condition) holds(tc *TaskContext) bool {
	value := lookup(referenceScope(tc), strings.Split(c.path, "."))
	switch c.operator {
	case "==":
		return stringify(value) == c.operand
	case "!=":
		return stringify(value) != c.operand
	}
	return truthy(value) != c.negate
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != "" && v != "false"
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return true
}

// validateControlTargets checks every handoff and approve step's target. A
// literal target must name a workflow the task can follow; a target templated
// from an agent result must resolve to one of the values the referenced enum
// allows. A templated target with no enum has no fixed set to check.
func validateControlTargets(parsed map[string]YAMLDefinition) error {
	for id, d := range parsed {
		err := walkSteps(d.Steps, fmt.Sprintf("workflow %q", id), func(where string, step StepRecord) error {
			target, ok, err := controlTarget(step)
			if err != nil || !ok {
				if err != nil {
					return fmt.Errorf("%s: %w", where, err)
				}
				return nil
			}
			if !referencePattern.MatchString(target) {
				return checkControlTarget(where, d.WorkflowInterface, parsed, target)
			}
			for _, name := range targetEnum(d, target) {
				if err := checkControlTarget(where, d.WorkflowInterface, parsed, name); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// controlTarget is the workflow a handoff or approve step routes to, and
// whether the step is one of those.
func controlTarget(step StepRecord) (string, bool, error) {
	var target string
	switch step.Type {
	case HandoffStepName:
		var s handoffSettings
		if err := decodeSettings(HandoffStepName, step.Settings, &s); err != nil {
			return "", false, err
		}
		target = strings.TrimSpace(s.Workflow)
	case ApproveStepName:
		var s approveSettings
		if err := decodeSettings(ApproveStepName, step.Settings, &s); err != nil {
			return "", false, err
		}
		target = strings.TrimSpace(s.Then)
	default:
		return "", false, nil
	}
	return target, true, nil
}

// checkControlTarget refuses a target workflow the task cannot follow: one
// that is not defined, one that needs a repository the task does not have, or
// one whose declared inputs the task's own inputs cannot satisfy.
func checkControlTarget(where string, source task.WorkflowInterface, parsed map[string]YAMLDefinition, target string) error {
	wf, ok := parsed[target]
	if !ok {
		return fmt.Errorf("%s targets %q, which is not defined", where, target)
	}
	if wf.RepositoryMode() == task.RepositoryRequired && source.RepositoryMode() != task.RepositoryRequired {
		return fmt.Errorf("%s targets %q, which needs a repository the task does not have", where, target)
	}
	for _, name := range slices.Sorted(maps.Keys(source.Inputs)) {
		if _, declared := wf.Inputs[name]; !declared {
			return fmt.Errorf("%s targets %q, which does not declare input %q the task carries", where, target, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(wf.Inputs)) {
		if wf.Inputs[name].Required && !source.Inputs[name].Required {
			return fmt.Errorf("%s targets %q, which requires input %q the task may not carry", where, target, name)
		}
	}
	return nil
}

// targetEnum returns the values a templated target can resolve to: the enum of
// the agent result field a "{{ steps.<id>.result.<field> }}" reference reads. A
// target that is not exactly such a reference, or reads a field without an
// enum, has no fixed set to check and returns none.
func targetEnum(d YAMLDefinition, target string) []string {
	match := referencePattern.FindStringSubmatch(target)
	if match == nil || strings.TrimSpace(target) != match[0] {
		return nil
	}
	parts := strings.Split(match[1], ".")
	if len(parts) != 4 || parts[0] != "steps" || parts[2] != "result" {
		return nil
	}
	step, ok := findStep(d.Steps, parts[1])
	if !ok || step.Type != AgentRunStepName {
		return nil
	}
	var s agentRunSettings
	if err := step.Settings.Decode(&s); err != nil {
		return nil
	}
	properties, _ := s.Result["properties"].(map[string]any)
	field, _ := properties[parts[3]].(map[string]any)
	values, _ := field["enum"].([]any)
	names := make([]string, 0, len(values))
	for _, value := range values {
		name, ok := value.(string)
		if !ok {
			return nil
		}
		names = append(names, name)
	}
	return names
}

// findStep resolves a step by id anywhere in a definition, including a step
// inside a parallel branch, whose result stays readable after the parallel
// step.
func findStep(steps []StepRecord, id string) (StepRecord, bool) {
	for _, step := range steps {
		if step.ID == id {
			return step, true
		}
		for _, branch := range step.Parallel {
			if found, ok := findStep(branch, id); ok {
				return found, true
			}
		}
	}
	return StepRecord{}, false
}
