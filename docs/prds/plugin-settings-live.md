# Plugin settings reconcile live

**Status:** Draft
**Revision:** 2
**Tracking:** GitHub [#1154](https://github.com/samcharles93/archie-core/issues/1154), epic [#998](https://github.com/samcharles93/archie-core/issues/998)
**Authority:** `docs/prds/runtime-control-plane.md` decides each feature owns how
its changes take effect; `docs/architecture/plugins-and-extensions.md` owns the
plugin engine rule; `docs/prds/control-plane-apply-status.md` owns where apply
reports go.

## Outcome

An operator drops a plugin, module or secret-engine file into a configured
directory while archied runs, and the daemon loads it without a restart. An
edit to an installed file replaces what is running. A delete never stops running
code; apply status says so. A stored `plugin-settings` change applies live for
`plugin_dir`, `module_dir` and `secret_engine_dir`; a change to `skills_dir` is
refused.

## Triggers

`PluginSettingsKind` joins `runtimeResourceKinds` in
`internal/app/archied/control_plane.go`, and its descriptor `ApplyMode` becomes
`live` in `internal/app/controlplane/operational_settings.go`.

A poller reconciles on the apply-status restamp interval, so a dropped file
loads without a filesystem watch. Each tick re-runs both halves the boot path
runs, against the directories in the running config: the directory loads
(`secret.Registry.LoadDir`, `ModuleRegistry.Register`, `plugin.LoadDir`) and the
layering (`boot.runtimeConfig`), which re-resolves provider credentials. A
provider disabled at boot for an unresolvable engine is re-resolved on the first
tick that finds it; a changed directory retargets the next tick without a
restart.

## Rules

- Adding and changing load; removing never unloads. Yaegi interpreters are not
  reclaimed while a caller still exists and there is no unload path; the running
  set can only grow. A removal stays outstanding in the apply-status error until
  the process restarts.
- Each file is loaded through a fresh interpreter (the boot pattern) and
  content-hashed, so only new and changed files load.
- A file that fails to load is skipped and reported; already-running code keeps
  running, the degrade-and-skip convention `plugin.LoadDir` and
  `Registry.LoadDir` already apply at boot. Boot keeps its reject-at-load rule
  for the module directory; a broken file at runtime skips without aborting.
- A directory that does not exist reconciles to an empty set.

## The three registries

- **Secret engines** (`internal/secret`). Reconciliation re-runs
  `Registry.LoadDir` against the live directory. `Register` replaces by name, so
  an edited engine takes effect for the next resolution and a dropped engine
  resolves on the next tick. A deleted engine stays registered: existing
  references keep resolving through it until restart. Engines are stateless
  resolvers, so a swap needs no lifecycle step.
- **Modules** (`internal/domain/eda/module`). Reconciliation re-runs
  `ModuleRegistry.Register` for the kind's file. Playbook dispatch resolves
  through the registry at invoke time, so the swap reaches a running playbook
  store unchanged; a deleted file leaves the old invoker in place, running.
- **Plugins** (`internal/plugin`). `Host` grows running-state operations beside
  `Register`, `Start` and `Stop`, and `Register` keeps its rule that
  registration is allowed only before the first `Start`, so these are new host
  methods rather than a relaxed `Register`. One adds a single module while the
  host is running: inserted in dependency order, started alone, and rolled back
  alone on failure, leaving every previously started module running. The other
  replaces a module whose manifest id matches: stop it, start the new one, and
  restart the old one when the new start fails; replacing a module another
  running module depends on is refused. A module's `Start` is its only check
  (`plugin.Module` has no probe, and one manifest id cannot be registered
  twice), so a replacement checks by starting and keeps a rollback rather than
  switching first. These operations obey the plugin engine rule: the host
  invokes start and stop itself and isolates the failure to one module. A
  deleted file makes no host call: the module runs on.

## `skills_dir`

`skills_dir` is not part of this change. The daemon refuses a `plugin-settings`
document whose `skills_dir` differs from the value in force. The check is a
pre-layer comparison on the live path (`boot.applyRuntimeResourceUpdate` in
`internal/app/archied/control_plane.go`), not an addition to
`validatePluginSettings` in `internal/app/controlplane/operational_settings.go`,
which stays value-agnostic per the #1143 decision recorded there: a
change-immutability rule needs the running config, and #1143's validates-nothing
still holds for values. The write lands, the store keeps the version, the daemon
publishes nothing, the refusal is reported against that stored version, and
last-known-good keeps running.

The layering assigns all four directories from one document, so a refused
`skills_dir` refuses the whole document and the other kinds still layer. That is
acceptable: `skills_dir` is the one field with no live consumer, a
whole-document refusal is the honest signal that none of it took effect, and an
operator changing `skills_dir` alongside another directory writes the two
changes separately.

## Apply status

One record per process: `archied` on `plugin-settings`, reported against the
version the process is running. The record's error field carries the outstanding
problem: a deleted file still running, a file that failed to load, the directory
failing to read, or a refused replace of a module another running module depends
on. A clean reconciliation clears the error. An add that succeeded is live even
while a removal is outstanding, and the error says so rather than showing the
kind as restart-required.

`ApplyMode` becomes `live`, so `RestartPendingBanner` no longer names
`plugin-settings`. The gateway's lag is shown instead by the per-process
applied-version rows on the Plugins page, which read the state
`applyStatusForKind` derives from `applied_version < storedVersion` in
`ui/src/stores/control-plane.ts`; that derivation is independent of
`ApplyMode`.

`archie-gateway` layers the `plugin-settings` document at boot through
`boot.loadRuntimeConfig` and reports it, but never calls `boot.loadPlugins` or
`boot.loadModules` and never reads the four directories, so it loads no plugin,
module or secret engine. With no live watch its row lags the stored version
until restart; that lag is a reporting lag, not a behaviour gap.

## Verification

- A plugin, module and secret-engine file dropped while the daemon runs loads on
  the next tick; an unchanged file is not reloaded.
- An edited file replaces the running interpretation; a deleted file leaves the
  running code running, with the process's error naming it until restart.
- A `skills_dir` change refuses the document: the store keeps the version, the
  record carries that version and the refusal, and the running `skills_dir` is
  unchanged.
- A plugin replacement whose new `Start` fails restarts the old module and
  reports the failure; a replacement of a depended-on module is refused and the
  dependent keeps running.
- A provider disabled at boot for a missing engine resolves again after the
  engine file loads and a tick re-layers.
- The gateway reports the last version it layered at boot, loads no directory,
  and its lag appears in its applied-version row rather than the restart banner.
