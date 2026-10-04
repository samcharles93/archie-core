package workflow

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
)

// FinishStepName is the step type that ends the workflow with a chosen outcome.
const FinishStepName = "workflow.finish"

// finishStatuses are the outcomes a workflow.finish step may choose.
var finishStatuses = map[string]string{
	"completed":     StatusCompleted,
	"parked":        StatusParked,
	"waiting_human": StatusWaitingHuman,
	"declined":      StatusClosedWontDo,
}

type finishSettings struct {
	Status string `yaml:"status"`
	Detail string `yaml:"detail"`
}

// FinishStepType contributes the workflow.finish step type.
func FinishStepType() StepType {
	return StepType{Name: FinishStepName, Factory: newFinishStage}
}

func newFinishStage(settings yaml.Node) (Stage, error) {
	var s finishSettings
	if settings.Kind != 0 {
		if err := settings.Decode(&s); err != nil {
			return Stage{}, fmt.Errorf("%s: %w", FinishStepName, err)
		}
	}
	if s.Status == "" {
		s.Status = "completed"
	}
	status, ok := finishStatuses[s.Status]
	if !ok {
		return Stage{}, fmt.Errorf("%s: settings.status is completed, parked, waiting_human or declined, not %q", FinishStepName, s.Status)
	}
	return Stage{Name: FinishStepName, Run: func(_ context.Context, tc *TaskContext) error {
		tc.Outcome = Outcome{Status: status, Detail: s.Detail}
		return nil
	}}, nil
}
