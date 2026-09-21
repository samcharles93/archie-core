package archied

import (
	"context"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
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
	cfg, versions, err := b.controlPlane.RuntimeConfig(ctx, base)
	if err != nil {
		return config.Config{}, err
	}
	if settings := b.executionSettings.Load(); settings != nil {
		applyExecutionBudgets(&cfg, *settings)
	}
	if err := configuration.Validate(&cfg); err != nil {
		wrapped := fmt.Errorf("validate database settings: %w", err)
		b.reportApplied(ctx, versions, wrapped)
		return config.Config{}, wrapped
	}
	b.reportApplied(ctx, versions, nil)
	return cfg, nil
}

// reportApplied publishes the version of each kind this process just layered
// in. On a validation failure every kind is reported with the error, because
// the layering is all-or-nothing: none of the versions read took effect.
func (b *boot) reportApplied(ctx context.Context, versions map[string]int64, applyErr error) {
	for kind, version := range versions {
		b.applyStatus.Report(ctx, kind, version, applyErr)
	}
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
	if err := b.applyWorkflowExecutionSettings(ctx, settings, version); err != nil {
		return fmt.Errorf("apply workflow execution settings: %w", err)
	}
	updates, err := b.controlPlane.WatchWorkflowExecutionSettings(ctx, version)
	if err != nil {
		return err
	}
	go func() {
		for update := range updates {
			if update.Err != nil {
				// A refused update arrives here, not through
				// applyWorkflowExecutionSettings: controlplane.Client decodes every
				// document it is handed, so a stored value this process cannot run
				// never reaches the apply call. Report it against the version it
				// came from -- the reporter keeps the version it last applied --
				// so the settings page shows the refusal instead of a version this
				// process never ran (docs/prds/control-plane-apply-status.md).
				//
				// The stream is not re-established after this point; that gap is
				// archie-core-yrmr, not this apply path.
				b.applyStatus.Report(ctx, controlplane.WorkflowExecutionSettingsKind, update.Version, update.Err)
				b.log.Error("workflow execution settings watch failed", "err", update.Err)
				return
			}
			_ = b.applyWorkflowExecutionSettings(ctx, update.Settings, update.Version)
		}
	}()
	return nil
}

// executionSettingsCandidate is a live workflow-execution-settings update that
// has been built and checked but not yet switched in: the limits themselves,
// and the configuration snapshot every new task would be built from.
type executionSettingsCandidate struct {
	settings workflow.ExecutionSettings
	cfg      config.Config
}

// buildExecutionSettingsCandidate stages a live update without touching
// anything running: it builds the configuration snapshot the new limits would
// be published as and runs this process's own runnability check on it -- the
// same configuration.Validate that runtimeConfig applies before publishing a
// layered config, and that the readiness probe applies to the published one. A
// snapshot this process cannot run is refused before it becomes the live one
// (docs/prds/runtime-control-plane.md, "API": Archie starts and checks the new
// one before switching).
//
// The kind's schema is deliberately not re-checked here. The store validates
// every document before it is written (Definition.Decode) and
// controlplane.Client validates it again as it decodes it, so re-running
// workflow.ExecutionSettings.Validate at this point would be a guard no
// producer can trip.
func (b *boot) buildExecutionSettingsCandidate(settings workflow.ExecutionSettings) (executionSettingsCandidate, error) {
	cfg := b.cfgHolder.Get().Clone()
	applyExecutionBudgets(&cfg, settings)
	if err := configuration.Validate(&cfg); err != nil {
		return executionSettingsCandidate{}, fmt.Errorf("workflow execution settings: %w", err)
	}
	return executionSettingsCandidate{settings: settings, cfg: cfg}, nil
}

// applyWorkflowExecutionSettings builds a candidate and switches it in.
//
// Both halves of the running component move together and only after the check
// passes: on a refusal the previous settings keep running, the configuration
// snapshot new tasks are built from is left as it is, and the failure is
// reported for the version that was refused. The apply-status reporter writes
// that record against the version it last applied, so once this process has
// applied anything the settings page shows a rejected edit rather than a
// version this process never ran.
//
// The settings are recorded as well as applied: they arrive on a watch rather
// than in the file document, so a reload has nowhere else to read them back
// from.
//
// The returned error is propagated by boot, the caller that cannot continue on
// limits it did not apply. Boot reaches this function only with settings the
// control plane already decoded and over a snapshot loadRuntimeConfig already
// validated, so that propagation is the fail-closed direction of the rule
// above rather than a branch the daemon takes today.
func (b *boot) applyWorkflowExecutionSettings(ctx context.Context, settings workflow.ExecutionSettings, version int64) error {
	candidate, err := b.buildExecutionSettingsCandidate(settings)
	if err != nil {
		b.applyStatus.Report(ctx, controlplane.WorkflowExecutionSettingsKind, version, err)
		b.log.Error("workflow execution settings rejected; the running settings stay", "version", version, "err", err)
		return err
	}
	b.executionSettings.Store(&candidate.settings)
	b.cfgHolder.Set(candidate.cfg)
	b.applyStatus.Report(ctx, controlplane.WorkflowExecutionSettingsKind, version, nil)
	b.log.Info("workflow execution settings applied", "version", version)
	return nil
}

func applyExecutionBudgets(cfg *config.Config, settings workflow.ExecutionSettings) {
	cfg.Budgets = config.Budgets{
		MaxSteps: settings.MaxModelToolSteps, WallClock: config.Duration(settings.MaxRuntime), GateMaxFailures: settings.MaxConsecutiveGateFailures,
	}
	for i := range cfg.Identities {
		cfg.Identities[i].Budgets = cfg.Budgets
	}
}
