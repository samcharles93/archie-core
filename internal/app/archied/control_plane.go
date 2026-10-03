package archied

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// runtimeConfig layers every database-owned setting over a file config and
// validates the result. Boot and the SIGHUP reload both go through it: the
// reload re-resolves the file document alone, so republishing that document
// as it stands reverts each of these layers to its file value until the
// process restarts.
func (b *boot) runtimeConfig(ctx context.Context, base config.Config) (config.Config, map[string]int64, error) {
	cfg, versions, err := b.controlPlane.RuntimeConfig(ctx, base)
	if err != nil {
		return config.Config{}, nil, err
	}
	if settings := b.executionSettings.Load(); settings != nil {
		applyExecutionBudgets(&cfg, *settings)
	}
	if err := configuration.Validate(&cfg); err != nil {
		wrapped := fmt.Errorf("validate database settings: %w", err)
		b.reportApplied(ctx, versions, wrapped)
		return config.Config{}, nil, wrapped
	}
	// The State Store persists source references, while openStores resolved
	// only the file snapshot. Layering provider-settings restores the source
	// references, so resolve the effective map before any runtime consumes it.
	if err := resolveProviderSecrets(&cfg, b.secrets, b.log); err != nil {
		b.reportApplied(ctx, versions, err)
		return config.Config{}, nil, err
	}
	b.reportApplied(ctx, versions, nil)
	return cfg, versions, nil
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
	cfg, versions, err := b.runtimeConfig(ctx, b.cfg)
	if err != nil {
		return err
	}
	b.cfg = cfg
	b.cfgHolder.Set(cfg)
	// The versions boot layered in are the resume points the runtime-resource
	// watches start from: a watch re-established after a version it already
	// applied neither replays a document this process handled nor skips one
	// it has not.
	b.runtimeVersions = versions
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
	catalog, _ := b.catalogState()
	applyModelCatalog(&doc.Config, catalog)
	// Bounded: this runs on the signal loop, which handles nothing else
	// while it waits, and a State Store that has stopped answering must
	// surface as a failed reload rather than a SIGHUP that never returns.
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, _, err := b.runtimeConfig(queryCtx, doc.Config)
	if err != nil {
		return fmt.Errorf("runtime settings unavailable: %w", err)
	}
	doc.Config = cfg
	old := b.cfgHolder.Get()
	b.currentProvenance.Store(&doc.Provenance)
	b.publishConfig(ctx, cfg)
	// The container pool reads the published config on every acquire, but the
	// dispatcher holds its limit; a reloaded containers.max_concurrency must
	// reach it the same way a control-plane update does.
	b.resizeTaskDispatcher(cfg.Containers.MaxConcurrency)
	if fields := changedNonReloadableFields(old, cfg); len(fields) > 0 {
		b.log.Warn("config reloaded; some changes require a restart",
			"fields", fields, "paths", doc.Provenance.Paths())
	} else {
		b.log.Info("config reloaded", "paths", doc.Provenance.Paths())
	}
	return nil
}

// startWorkflowExecutionSettings loads the stored limits, applies them and
// keeps the stream that delivers later ones established. The first read and
// the first stream are synchronous, so a control plane that cannot be reached
// fails the boot that asked for it; after that the watch does not return, it
// reconnects (see keepWatch).
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
	var lastApplyErr, streamErr error
	open := func(ctx context.Context, after int64) (<-chan controlplane.AppliedSettings, error) {
		updates, err := b.controlPlane.WatchWorkflowExecutionSettings(ctx, after)
		if err == nil {
			b.applyStatus.Report(ctx, controlplane.WorkflowExecutionSettingsKind, after, lastApplyErr)
		}
		return updates, err
	}
	go keepWatch(ctx, b.log, controlplane.WorkflowExecutionSettingsKind, version, updates,
		open,
		waitFor,
		func(update controlplane.AppliedSettings) int64 { return update.Version },
		func(update controlplane.AppliedSettings) {
			if update.Err != nil {
				// A refused document carries its version, while a stream failure
				// does not. Keep a refusal visible across a transport outage; when
				// there is no outstanding refusal, the end callback records the
				// outage until a watch is re-established.
				if update.Version > 0 {
					lastApplyErr = update.Err
					b.applyStatus.Report(ctx, controlplane.WorkflowExecutionSettingsKind, update.Version, update.Err)
				} else {
					streamErr = update.Err
				}
				b.log.Error("workflow execution settings watch failed", "err", update.Err)
				return
			}
			lastApplyErr = b.applyWorkflowExecutionSettings(ctx, update.Settings, update.Version)
		},
		func() {
			if lastApplyErr != nil {
				return
			}
			if streamErr == nil {
				streamErr = fmt.Errorf("control-plane watch stream ended")
			}
			b.applyStatus.Report(ctx, controlplane.WorkflowExecutionSettingsKind, 0,
				fmt.Errorf("workflow execution settings watch unavailable: %w", streamErr))
			streamErr = nil
		})
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
// snapshot this process cannot run is refused before it becomes the live one.
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
	b.publishConfig(ctx, candidate.cfg)
	b.applyStatus.Report(ctx, controlplane.WorkflowExecutionSettingsKind, version, nil)
	b.log.Info("workflow execution settings applied", "version", version)
	if limit, uptime := settings.MaxTaskRuntime, candidate.cfg.Containers.MaxUptime.Std(); uptime > 0 && (limit <= 0 || limit > uptime) {
		b.log.Warn("containers.max_uptime is shorter than the task time limit; the container is killed first", "max_uptime", uptime, "max_task_runtime", limit)
	}
	return nil
}

func applyExecutionBudgets(cfg *config.Config, settings workflow.ExecutionSettings) {
	cfg.Budgets = config.Budgets{
		MaxSteps: settings.MaxModelToolSteps, WallClock: config.Duration(settings.MaxRuntime), GateMaxFailures: settings.MaxConsecutiveGateFailures,
		TaskWallClock: config.Duration(settings.MaxTaskRuntime),
	}
	for i := range cfg.Identities {
		cfg.Identities[i].Budgets = cfg.Budgets
	}
}

// startLiveSettings starts everything a live control-plane update needs
// established before Run continues: the workflow-execution-settings watch,
// whose limits the layering applies, and one watch per runtime-resource kind
// (control_plane.go, runtimeResourceKinds). Both first reads are synchronous,
// so a control plane that cannot be watched at all fails the boot that asked
// for it rather than leaving the process running settings it can no longer
// update.
func (b *boot) startLiveSettings(ctx context.Context) error {
	if err := b.startWorkflowExecutionSettings(ctx); err != nil {
		return fmt.Errorf("workflow execution settings: %w", err)
	}
	if err := b.startRuntimeResourceWatches(ctx, b.runtimeVersions); err != nil {
		return fmt.Errorf("runtime resource watches: %w", err)
	}
	return nil
}

// runtimeResourceKinds are the control-plane kinds this process re-layers
// live: on a new stored version the watch re-runs the layering a reload runs
// (b.runtimeConfig, which applies the execution budgets the settings watch
// published) and republishes through config.Holder, so the change takes
// effect without a restart. Kinds stay out of this list while a
// startup-built component still holds their value; none does today, so the
// list is every control-plane kind. A kind joins it only with a consumer that
// re-reads it. ToolSettingsKind is on it because applyRuntimeResourceUpdate
// reconciles the MCP provider set and rebuilds the two config-derived tool
// entries (boot.reconcileToolSettings, its consumer); a removed MCP server
// really disconnects, because it is a child process with a Stop, not a Yaegi
// interpreter. ContainerRuntimePoliciesKind is on it because both consumers
// re-read the published config: the container pool takes its image, pull
// policy, network, max-uptime and concurrency cap per acquire, and the daemon's
// dispatcher is resized after the publish (applyRuntimeResourceUpdate below).
// PluginSettingsKind is on the list because its only consumer is the directory
// reconciliation, which reads the running config each tick; a removed plugin
// or engine still cannot unload, so that kind reports the removal rather than
// requiring a restart. ChannelSettingsKind is on the list for this process's
// own re-layer; the Messaging Service, which owns the channel transports,
// reconciles them separately and restarts only the channel whose settings
// changed. Each kind is watched in its own goroutine, exactly the shape the
// workflow-execution-settings watch established.
var runtimeResourceKinds = []string{
	controlplane.ProviderSettingsKind,
	controlplane.ModelRoleAssignmentsKind,
	controlplane.RepositoryPoliciesKind,
	controlplane.SchedulingPolicyKind,
	controlplane.ToolSettingsKind,
	controlplane.PluginSettingsKind,
	controlplane.ReviewSettingsKind,
	controlplane.ChannelSettingsKind,
	controlplane.ContainerRuntimePoliciesKind,
}

// startRuntimeResourceWatches keeps a watch per live kind established for the
// life of the process. versions carries the resume point each kind starts
// from, the versions boot's layering recorded. The first stream per kind is
// opened synchronously, so a control plane that cannot be watched at all
// fails the boot that asked for it; after that the watch does not return, it
// reconnects (see keepWatch, whose backoff rules these watches ride).
func (b *boot) startRuntimeResourceWatches(ctx context.Context, versions map[string]int64) error {
	for _, kind := range runtimeResourceKinds {
		updates, err := b.controlPlane.WatchResource(ctx, kind, versions[kind])
		if err != nil {
			return fmt.Errorf("watch %s: %w", kind, err)
		}
		go keepWatch(ctx, b.log, kind, versions[kind], updates,
			func(ctx context.Context, afterVersion int64) (<-chan controlplane.AppliedResource, error) {
				return b.controlPlane.WatchResource(ctx, kind, afterVersion)
			},
			waitFor,
			func(update controlplane.AppliedResource) int64 { return update.Version },
			func(update controlplane.AppliedResource) { b.applyRuntimeResourceUpdate(ctx, kind, update) })
	}
	return nil
}

// applyRuntimeResourceUpdate re-runs the layering a reload runs and
// republishes the result through config.Holder. The base is the published
// snapshot rather than the boot struct: reloadConfig publishes through the
// same holder and does not update the boot config, so the holder is the one
// view that is current in every path. The layering reads every kind from the
// store over it, so a scheduling-policy document written during this
// process's life and every other kind's last stored value land together, and
// the merge is idempotent (every kind it reads replaces its fields).
//
// The published snapshot is replaced only after the same check the reload
// and the boot layering pass: on a validation failure nothing is published,
// the previous settings keep running (last-known-good), and the failure is
// reported through apply status, which keeps the version still live on the
// record. A refused update therefore reaches the settings page the way a
// refused workflow-execution-settings update does
func (b *boot) applyRuntimeResourceUpdate(ctx context.Context, kind string, update controlplane.AppliedResource) {
	if update.Err != nil {
		// A stream failure carries no version at all (versions start at 1,
		// store.PutResource), and a process must never report an unreachable
		// store as its own refusal; the reconnect is keepWatch's business.
		if update.Version > 0 {
			b.applyStatus.Report(ctx, kind, update.Version, update.Err)
		}
		b.log.Error("runtime settings watch failed", "kind", kind, "err", update.Err)
		return
	}
	if kind == controlplane.PluginSettingsKind {
		if err := b.refuseSkillsDirChange(ctx); err != nil {
			b.applyStatus.Report(ctx, kind, update.Version, err)
			b.log.Error("plugin settings refused; the running settings stay", "version", update.Version, "err", err)
			return
		}
	}
	base := b.cfgHolder.Get()
	// Boot publishes before the model catalog is merged in, so the snapshot
	// this read returns can lack the catalog's model limits and discovered
	// providers. The merge is idempotent, and reloadConfig re-applies it
	// before layering for the same reason.
	catalog, _ := b.catalogState()
	applyModelCatalog(&base, catalog)
	cfg, _, err := b.runtimeConfig(ctx, base)
	if err != nil {
		// runtimeConfig reported the refusal through apply status for every
		// kind it layers, keeping the version each of them last applied.
		b.log.Error("runtime settings update rejected; the running settings stay", "kind", kind, "version", update.Version, "err", err)
		return
	}
	b.publishConfig(ctx, cfg)
	b.log.Info("runtime settings applied", "kind", kind, "version", update.Version)
	switch kind {
	case controlplane.ProviderSettingsKind, controlplane.ModelRoleAssignmentsKind:
		b.rebuildChatModelRuntime(cfg)
	case controlplane.ToolSettingsKind:
		// The document's server set and tool entries are live now; reconcile
		// them and report the reconciliation's own outcome, which is the one
		// that knows whether a server failed to connect.
		if err := b.reconcileToolSettings(ctx, cfg); err != nil {
			b.applyStatus.Report(ctx, kind, update.Version, err)
			b.log.Error("tool settings reconciled with errors; the running servers stay", "version", update.Version, "err", err)
		}
	case controlplane.PluginSettingsKind:
		// The document's directories are live now; load what they hold and
		// report the reconciliation's own outcome, which is the one that knows
		// whether a removal is outstanding.
		b.reconcileRuntimePlugins(ctx)
	case controlplane.ContainerRuntimePoliciesKind:
		// The pool reads the republished config itself; the dispatcher holds
		// its limit, so a raise must be woken to admit queued work now and a
		// lower must be honoured as running tasks finish.
		b.resizeTaskDispatcher(cfg.Containers.MaxConcurrency)
	}
}

// resizeTaskDispatcher applies a new containers.max_concurrency to the
// daemon's running dispatcher. The pool needs no poke because it reads the
// published config on every acquire. b.d is nil in the boot window in which
// the watches are already live but the daemon is not built yet; there the
// dispatcher is created later from the config the next drain reads.
func (b *boot) resizeTaskDispatcher(maxConcurrency int) {
	if b.d != nil {
		b.d.ResizeTaskDispatcher(maxConcurrency)
	}
}

// startPluginReconcile establishes the plugin and module directory
// reconciliation on the apply-status restamp interval. Boot calls it
// after loadPlugins and loadWorkflows have loaded
// the directories, so the seed records what is already running and the first
// tick loads only what appears or changes afterwards.
func (b *boot) startPluginReconcile(ctx context.Context) {
	r := newPluginReconciler(b.log, pluginReconcileTargets{
		host:    b.capabilityHost,
		modules: b.modules,
		dirs:    func() config.Config { return b.cfgHolder.Get() },
		relayer: func(ctx context.Context) error {
			cfg, _, err := b.runtimeConfig(ctx, b.cfgHolder.Get())
			if err != nil {
				return err
			}
			b.publishConfig(ctx, cfg)
			return nil
		},
		report: func(ctx context.Context, err error) {
			b.applyStatus.Report(ctx, controlplane.PluginSettingsKind, b.applyStatus.AppliedVersion(controlplane.PluginSettingsKind), err)
		},
	})
	b.pluginReconciler = r
	r.seed()
	go r.run(ctx, applystatus.RestampInterval)
}

// reconcileRuntimePlugins runs one directory reconciliation after a stored
// plugin-settings change, so a new directory loads on the tick the document
// lands rather than waiting for the poller.
func (b *boot) reconcileRuntimePlugins(ctx context.Context) {
	if b.pluginReconciler == nil {
		return
	}
	_ = b.pluginReconciler.reconcile(ctx)
}

// refuseSkillsDirChange refuses a stored plugin-settings document whose
// skills_dir differs from the value in force. skills_dir is the one directory
// with no live consumer, so a change to it stays a restart-scoped edit rather
// than a value the daemon reports as applied.
func (b *boot) refuseSkillsDirChange(ctx context.Context) error {
	var stored struct {
		SkillsDir string `json:"skills_dir"`
	}
	_, found, err := b.controlPlane.Query(ctx, controlplane.PluginSettingsKind, func(value []byte) error {
		return json.Unmarshal(value, &stored)
	})
	if err != nil {
		return fmt.Errorf("read plugin settings: %w", err)
	}
	if !found {
		return nil
	}
	if running := b.cfgHolder.Get().SkillsDir; stored.SkillsDir != running {
		return fmt.Errorf("skills_dir applies on restart: stored %q, running %q", stored.SkillsDir, running)
	}
	return nil
}

// rebuildChatModelRuntime re-derives the gateway chat runtime's provider set
// after a live change to provider-settings or model-role-assignments. The
// turn runner reads the runtime through boot.chatLLM, so the swap reaches its
// next turn without rebuilding the runner, and the model manager re-derives
// the references it offers from the new role assignments and the catalog.
func (b *boot) rebuildChatModelRuntime(cfg config.Config) {
	if b.chatModels == nil {
		return // this process serves no chat turns
	}
	b.setLLM(agentexec.NewRuntime(executionProviders(cfg)))
	b.chatModels.SetConfigured(cfg.Models)
	catalog, models := b.catalogState()
	b.chatModels.SetModelCatalog(catalog, models)
	b.log.Info("chat model runtime rebuilt", "providers", len(cfg.Providers), "models", len(cfg.Models))
}
