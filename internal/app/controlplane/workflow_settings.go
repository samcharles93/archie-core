package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/store"
)

const WorkflowExecutionSettingsKind = "workflow-execution-settings"

type executionSettingsDocument struct {
	MaxModelToolSteps          int   `json:"max_model_tool_steps"`
	MaxRuntimeSeconds          int64 `json:"max_runtime_seconds"`
	MaxConsecutiveGateFailures int   `json:"max_consecutive_gate_failures"`
}

// ImportWorkflowExecutionSettings preserves the focused bootstrap API for callers
// that do not own the complete legacy configuration document.
func (s *Server) ImportWorkflowExecutionSettings(ctx context.Context, settings workflow.ExecutionSettings) (int64, error) {
	resource, err := s.store.Resource(ctx, WorkflowExecutionSettingsKind)
	if err == nil {
		return resource.Version, nil
	}
	if !errors.Is(err, store.ErrResourceNotFound) {
		return 0, err
	}
	value, err := encodeSettings(settings)
	if err != nil {
		return 0, err
	}
	value, err = workflowDefinition().Decode(value)
	if err != nil {
		return 0, err
	}
	resource, err = s.store.PutResource(ctx, store.ResourceWrite{Kind: WorkflowExecutionSettingsKind, Value: value, Actor: "system:migration", Source: "legacy-config", RequestID: "import:" + WorkflowExecutionSettingsKind, ExpectedVersion: 0, At: time.Now().UTC()})
	return resource.Version, err
}

func workflowDefinition() Definition {
	return Definition{
		Kind: WorkflowExecutionSettingsKind, Title: "Workflow execution settings", ApplyMode: "live", Document: executionSettingsDocument{},
		Seed: func(cfg config.Config) any {
			return executionSettingsDocument{cfg.Budgets.MaxSteps, int64(time.Duration(cfg.Budgets.WallClock) / time.Second), cfg.Budgets.GateMaxFailures}
		},
		Validate: func(input []byte) error { _, err := decodeSettings(input); return err },
	}
}

func encodeSettings(s workflow.ExecutionSettings) ([]byte, error) {
	return json.Marshal(executionSettingsDocument{s.MaxModelToolSteps, int64(s.MaxRuntime / time.Second), s.MaxConsecutiveGateFailures})
}

func decodeSettings(value []byte) (workflow.ExecutionSettings, error) {
	var document executionSettingsDocument
	if err := json.Unmarshal(value, &document); err != nil {
		return workflow.ExecutionSettings{}, fmt.Errorf("%w: invalid JSON: %w", ErrValidation, err)
	}
	settings := workflow.ExecutionSettings{MaxModelToolSteps: document.MaxModelToolSteps, MaxRuntime: time.Duration(document.MaxRuntimeSeconds) * time.Second, MaxConsecutiveGateFailures: document.MaxConsecutiveGateFailures}
	if err := settings.Validate(); err != nil {
		return workflow.ExecutionSettings{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return settings, nil
}
