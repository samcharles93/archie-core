package workflow

import (
	"errors"
	"fmt"
	"time"
)

// ExecutionSettings are the limits captured when a workflow execution starts.
type ExecutionSettings struct {
	MaxModelToolSteps          int           `json:"max_model_tool_steps"`
	MaxRuntime                 time.Duration `json:"max_runtime"`
	MaxConsecutiveGateFailures int           `json:"max_consecutive_gate_failures"`
	MaxTaskRuntime             time.Duration `json:"max_task_runtime"`
}

// Validate rejects negative limits. Zero preserves the established meaning of
// disabling that individual limit.
func (s ExecutionSettings) Validate() error {
	var errs []error
	if s.MaxModelToolSteps < 0 {
		errs = append(errs, fmt.Errorf("max model/tool steps must not be negative"))
	}
	if s.MaxRuntime < 0 {
		errs = append(errs, fmt.Errorf("max runtime must not be negative"))
	}
	if s.MaxTaskRuntime < 0 {
		errs = append(errs, fmt.Errorf("max task runtime must not be negative"))
	}
	if s.MaxConsecutiveGateFailures < 0 {
		errs = append(errs, fmt.Errorf("max consecutive gate failures must not be negative"))
	}
	return errors.Join(errs...)
}
