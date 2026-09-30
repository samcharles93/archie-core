# Plugin settings reconcile live

**Status:** Draft
**Tracking:** GitHub [#1154](https://github.com/samcharles93/archie-core/issues/1154), epic [#998](https://github.com/samcharles93/archie-core/issues/998)
**Authority:** `docs/prds/runtime-control-plane.md` decides each feature owns how
its changes take effect; `docs/architecture/plugins-and-extensions.md` owns the
plugin engine rule; `docs/prds/control-plane-apply-status.md` owns where apply
reports go.

## Outcome

An operator drops a plugin, module or secret-engine file into the configured
directory while archied runs, and the daemon loads it without a restart. An
edit to an installed file replaces what is running. Deleting an installed file
never stops running code; apply status says so. A stored change to
`plugin-settings` applies live for additions instead of requiring a restart.

## Triggers

`plugin-settings` joins the kinds archied watches (`PluginSettingsKind` moves
to the live list in `internal/app/archied/control_plane.go`). After the daemon
layers a new version it reconciles the three directories the document names.
A second trigger needs no document change: a poller reconciles on an interval,
so a file dropped into a directory loads on its own. The poll interval is the
apply-status restamp constant; no filesystem-watch dependency is added. The
poller reads the current document's directories from the running config each
tick, so a changed `plugin_dir`, `module_dir` or `secret_engine_dir` retargets
reconciliation without a restart.

## Rules

- Adding and changing load; removing never unloads. Yaegi interpreters are not
  reclaimed while a caller still exists and there is no unload path; the
  running set can only grow.
- Each file is loaded through a fresh interpreter (the boot pattern). A changed
  file therefore loads cleanly, and replacement switches consumers to the new
  interpretation; files are content-hashed so only new and changed files load.
- A file that fails to load is skipped and reported; already-running code keeps
  running. This is the degrade-and-skip convention `plugin.LoadDir` and
  `Registry.LoadDir` already apply at boot. Boot keeps its reject-at-load rule
  for the module directory; a broken file at runtime skips without aborting.
- A directory that does not exist reconciles to an empty set.

## The three registries

- **Secret engines** (`internal/secret`). Reconciliation re-runs
  `Registry.LoadDir` against the live directory. `Register` replaces by name,
  so an edited engine takes effect for the next resolution. A deleted engine
  stays registered: existing references keep resolving through it until
  restart, and the record says so. Engines are stateless resolvers, so a swap
  needs no lifecycle step.
- **Modules** (`internal/domain/eda/module`): reconciliation re-runs
  `ModuleRegistry.Register` for the kind's file. The invoker maps
  last-write-wins, and playbook dispatch resolves through the registry at
  invoke time, so the swap reaches a running playbook store unchanged. A file
  deleted for a kind leaves the old invoker in place, running; reported.
- **Plugins** (`internal/plugin`): `Host` grows running-state operations beside
  `Register`, `Start` and `Stop`. One adds a single module while the host is
  running: it is inserted in dependency order, started alone, and on failure
  rolled back alone, leaving every previously started module running. The
  other replaces a module whose manifest id matches: stop it, start the new
  one, and restart the old one when the new start fails. Replacing a module
  that another depends on is refused, not swapped. These operations obey the
  plugin engine rule: the host still invokes start and stop itself, isolates
  the failure to the one module, and keeps a rollback. A deleted file makes no
  host call: the module runs on and is reported.

## `skills_dir`

`skills_dir` is not part of this change. The daemon refuses a candidate
`plugin-settings` document whose `skills_dir` differs from the value it is
running, reporting that the field applies on restart, until a skill
reconciliation exists to carry it. The refusal is the existing rejected-edit
apply-status shape: the store keeps the version, the document is not applied,
last-known-good keeps running.

## Apply status

One record: `archied` on `plugin-settings`. The reconciliation outcome is
reported against the version the process is running. The record's error field
carries the outstanding reconciliation problem: a deleted file still running, a
file that failed to load, or the directory failing to read. A reconciliation
with nothing outstanding clears the error. The version stays the version the
process is running; an add that succeeded is live even while a removal is
outstanding, and the error says so rather than showing the kind as
restart-required. archie-gateway keeps boot-time loading; its record simply
stays on the version it last applied until it restarts, which the per-process
apply-status page already reads correctly.