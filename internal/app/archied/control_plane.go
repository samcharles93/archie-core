package archied

import (
	"context"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// runtimeConfig layers every database-owned setting over a file config and
// validates the result. Boot and the SIGHUP reload both go through it: the
// reload re-resolves the file document alone, so republishing that document
// as it stands reverts each of these layers to its file value until the
// process restarts (archie-core-ju85).
func (b *boot) runtimeConfig(ctx context.Context, base config.Config) (config.Config, error) {
	cfg, err := b.controlPlane.RuntimeConfig(ctx, base)
	if err != nil {
		return config.Config{}, err
	}
	if settings := b.executionSettings.Load(); settings != nil {
		applyExecutionBudgets(&cfg, *settings)
	}
	if err := configuration.Validate(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("validate database settings: %w", err)
	}
	return cfg, nil
}

func (b *boot) loadRuntimeConfig(ctx context.Context) error {
	cfg, err := b.runtimeConfig(ctx, b.cfg)
	if err != nil {
		return err
	}
	b.cfg = cfg
	b.cfgHolder.Set(cfg)
	return nil
}

// reloadConfig publishes a freshly resolved file document as the running
// config. The database layer is re-applied first, and a control plane that
// cannot answer aborts the reload: the running config already holds those
// values, so publishing the file's over them is the worse outcome.
func (b *boot) reloadConfig(ctx context.Context, doc *configuration.Document) error {
	// Boot merges catalog-discovered providers into cfg before the
	// Holders are seeded; publishing the raw reloaded document would
	// drop them from the running config even though the file is
	// unchanged. Re-apply the same merge (idempotent: a catalog that
	// failed to load merges to identity).
	applyModelCatalog(&doc.Config, b.catalog)
	// Bounded: this runs on the signal loop, which handles nothing else
	// while it waits, and a State Store that has stopped answering must
	// surface as a failed reload rather than a SIGHUP that never returns.
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, err := b.runtimeConfig(queryCtx, doc.Config)
	if err != nil {
		return fmt.Errorf("runtime settings unavailable: %w", err)
	}
	doc.Config = cfg
	old := b.cfgHolder.Get()
	b.currentProvenance.Store(&doc.Provenance)
	b.publishConfig(ctx, cfg)
	if fields := changedNonReloadableFields(old, cfg); len(fields) > 0 {
		b.log.Warn("config reloaded; some changes require a restart",
			"fields", fields, "paths", doc.Provenance.Paths())
	} else {
		b.log.Info("config reloaded", "paths", doc.Provenance.Paths())
	}
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

// applyWorkflowExecutionSettings records the settings as well as applying
// them: they arrive on a watch rather than in the file document, so a reload
// has nowhere else to read them back from.
func (b *boot) applyWorkflowExecutionSettings(settings workflow.ExecutionSettings, version int64) {
	b.executionSettings.Store(&settings)
	cfg := b.cfgHolder.Get().Clone()
	applyExecutionBudgets(&cfg, settings)
	b.cfgHolder.Set(cfg)
	b.log.Info("workflow execution settings applied", "version", version)
}

func applyExecutionBudgets(cfg *config.Config, settings workflow.ExecutionSettings) {
	cfg.Budgets = config.Budgets{
		MaxSteps: settings.MaxModelToolSteps, WallClock: config.Duration(settings.MaxRuntime), GateMaxFailures: settings.MaxConsecutiveGateFailures,
	}
	for i := range cfg.Identities {
		cfg.Identities[i].Budgets = cfg.Budgets
	}
}
