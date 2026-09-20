package archied

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

func (b *boot) loadRuntimeConfig(ctx context.Context) error {
	cfg, err := b.controlPlane.RuntimeConfig(ctx, b.cfg)
	if err != nil {
		return err
	}
	if err := configuration.Validate(&cfg); err != nil {
		return fmt.Errorf("validate database settings: %w", err)
	}
	b.cfg = cfg
	b.cfgHolder.Set(cfg)
	return nil
}

func (b *boot) startWorkflowExecutionSettings(ctx context.Context) error {
	settings, version, err := b.controlPlane.WorkflowExecutionSettings(ctx)
	if err != nil {
		return err
	}
	b.applyWorkflowExecutionSettings(settings, version)
	updates, err := b.controlPlane.WatchWorkflowExecutionSettings(ctx, version)
	if err != nil {
		return err
	}
	go func() {
		for update := range updates {
			if update.Err != nil {
				b.log.Error("workflow execution settings watch failed", "err", update.Err)
				return
			}
			b.applyWorkflowExecutionSettings(update.Settings, update.Version)
		}
	}()
	return nil
}

func (b *boot) applyWorkflowExecutionSettings(settings workflow.ExecutionSettings, version int64) {
	cfg := b.cfgHolder.Get().Clone()
	cfg.Budgets = executionBudgets(settings)
	for i := range cfg.Identities {
		cfg.Identities[i].Budgets = cfg.Budgets
	}
	b.cfgHolder.Set(cfg)
	b.log.Info("workflow execution settings applied", "version", version)
}

func executionBudgets(settings workflow.ExecutionSettings) config.Budgets {
	return config.Budgets{
		MaxSteps: settings.MaxModelToolSteps, WallClock: config.Duration(settings.MaxRuntime), GateMaxFailures: settings.MaxConsecutiveGateFailures,
	}
}
