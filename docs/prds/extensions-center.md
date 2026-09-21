# Extensions center

**Status:** Draft

Epic: `archie-core-1786637490708-35-38424ead` / GitHub `#56` ("Replace Skills
with a bundled Extensions center").

Skills, Workflows, and Tools/MCP already exist as separate, working
inventories with their own APIs (`api_skills.go`, `api_workflows.go`,
config-driven MCP servers) and their own family-owned controller pattern
(`curator.Registry`, `health.Registry`, `image.Registry`,
`workflow.Registry` all already prove this shape). `plugin.Plugin`
(`internal/plugin/plugin.go`) is already metadata-only, per
`docs/architecture/plugins-and-extensions.md`. None of that is rebuilt here.
What's missing, confirmed by grep, is: any config schema for enabling or
disabling a capability, any "pending restart" state tracking, and any single
inventory that reconciles configured/bundled/discovered/active state across
families. This document decides those three things and the UI surface that
presents them. It extends `plugins-and-extensions.md` rather than replacing
it — that doc's decisions (metadata-only `Plugin`, typed capability
families, no generic dispatch) are unchanged.

Out of scope: any remote or arbitrary-URL plugin installation path (the
epic's own acceptance criteria forbid this, and `plugin.LoadDir`
(`plugin.go`) already only reads `~/.config/archie/plugins/*.go` — this
document does not add a fetch-by-URL variant to it).

## Problem

`internal/config/config.go`'s `MCPServer` and `ToolsConfig` structs have no
`enabled` field (confirmed by the investigation grep). `api_config.go`'s
`UpdateConfig`/`handleConfigUpdate` seam persists changes but has no state
distinguishing "saved, takes effect now" from "saved, needs restart" — the
only existing reload path (`ReloadChannel`, `server.go`) is
channel-specific and explicitly does not cover plugins or MCP servers.
`server.go` already wires five separate optional inventories
(Workflows, Skills, Curators, Channels, Captures/Mappings/Bindings) as
independent fields with no unifying type — an operator has no single
place to see "what's available, what's enabled, what's actually running,
and what's degraded" across all of them. `ui/src/` has `settings/`,
`skills/`, and `workflows/` directories already; there is no
`ui/src/extensions/`.

## Design

### Unified inventory type

```go
package extensions // internal/domain/extensions — new, app-level aggregator

type State string

const (
    StateAvailable      State = "available"       // bundled, not configured
    StateEnabled        State = "enabled"          // configured on, not yet live
    StateActive         State = "active"           // configured on and running
    StateDegraded       State = "degraded"         // active but unhealthy
    StateSkipped        State = "skipped"          // optional, disabled by config
    StatePendingRestart State = "pending_restart"  // config changed, not yet live
)

type Entry struct {
    Family      string // "skill" | "workflow" | "tool" | "plugin" | "integration"
    Name        string
    Core        bool   // non-disableable
    State       State
    Health      string // reuses whatever the family's own registry already reports
}

type Inventory interface {
    List(ctx context.Context) ([]Entry, error)
}
```

**Decision.** `extensions.Inventory` is a read-only aggregator, not a new
domain that owns state. It queries each family's existing registry
(`curator.Registry`, `health.Registry`, `workflow.Registry`, the MCP client
set, `plugin.LoadDir`'s result) for live state, and compares that against
the config-desired state (below) to compute `StatePendingRestart` — the diff
is computed on read, not persisted, so there is no new durable state to keep
consistent with the registries themselves. This follows
`migration-decisions.md`'s rule that cross-cutting aggregation composes
existing domain-owned contracts rather than duplicating their state.

**Call site.** New package `internal/domain/extensions/inventory.go`.
Composed in `internal/app/archied` (wherever `server.go`'s other optional
fields — Workflows, Skills, Curators — are wired) since it needs
references to every family's registry, which only application-layer
composition has.

### Config-backed enablement

**Decision.** Add `enabled bool` (default `true` for pre-existing configured
entries, so no upgrade silently disables anything already configured) to
each family's config element: `MCPServer.Enabled`, and equivalent fields on
whatever config types back skills/workflows/plugins if they don't already
have an on/off switch. Core capabilities (identity, workflow engine, State
Store connectivity — named in `organisation.md`'s cross-cutting packages)
have no `enabled` field at all; a config value can't be added to something
that doesn't expose the toggle, which is the "non-disableable" property
enforced structurally rather than by a runtime check that could be
bypassed.

**Call site.** `internal/config/config.go` — add the field per family
struct, not one generic `map[string]bool` (typed fields keep
`docsgen`/`contracts.json` generation working, per `tools/docsgen`'s
"treat flags as executable truth" rule — a generic map has no type to
generate from). `config.example.toml` documents each new field with its
default.

**Reuse check.** The config `Holder` overlay system
(`internal/infrastructure/configuration`) already handles safe saves; this
adds fields to existing structs, it does not add a second config-write path.

### Pending-restart and reconciliation

**Decision.** No new "pending restart" flag is persisted. `Inventory.List`
computes `StatePendingRestart` by comparing the config's `Enabled` value
against whether the family's registry reports that entry as
live — if they disagree, the entry is pending restart. This is simpler than
tracking a flag through a save-then-restart cycle and can never go stale,
because it's recomputed on every read.

**Call site.** `internal/domain/extensions/inventory.go`'s comparison logic.

### API and UI surface

**Decision.** New `api_extensions.go` alongside the existing `api_skills.go`
/`api_workflows.go` pattern, exposing `Inventory.List` and a save endpoint
that writes through the existing `UpdateConfig` seam (`api_config.go`) —
this document does not add a second config-write endpoint. New
`ui/src/extensions/` (colocated `.js`/`.css` per CLAUDE.md's frontend rule)
as an index/dashboard that **links to**, not replaces, the existing
`ui/src/skills/` and `ui/src/workflows/` detail views — Extensions center is
where an operator sees everything and toggles enablement; the family-specific
pages remain where they inspect a family's own detail.

**Call site.** `internal/webui/api_extensions.go` (new, follows
`api_skills.go`'s shape), `ui/src/extensions/` (new).

## Call site inventory

| concern | file | change |
|---|---|---|
| Plugin metadata type | `internal/plugin/plugin.go` | none — already handles this |
| Family controllers (curator/health/image/workflow registries) | `internal/domain/curator/registry.go`, `internal/domain/health/health.go`, etc. | none — pattern already proven, reused not rewritten |
| Unified inventory | `internal/domain/extensions/inventory.go` | new |
| Composition wiring | `internal/app/archied/server.go` (near existing optional fields) | new |
| Config enablement fields | `internal/config/config.go` | new fields per family |
| Config docs | `config.example.toml` | new field documentation |
| API surface | `internal/webui/api_extensions.go` | new |
| Existing skills/workflows APIs | `internal/webui/api_skills.go`, `api_workflows.go` | none — linked from, not replaced |
| Config save path | `internal/webui/api_config.go` (`UpdateConfig`) | reused, not duplicated |
| UI | `ui/src/extensions/` | new |
| Existing UI detail views | `ui/src/skills/`, `ui/src/workflows/` | none — linked from Extensions center |

## Execution: multi-agent team breakdown

| sub-feature | issue | implementer scope | suggested council lenses | why |
|---|---|---|---|---|
| Unified inventory type + composition | file as child of `#56` | `internal/domain/extensions/inventory.go`, wiring in `server.go` | `lens-boundary`, `lens-deletionist` | boundary: aggregator must not duplicate family-owned state; deletionist: must not become a new domain that shadows existing registries |
| Config enablement fields | file as child of `#56` | `config.go` per-family `Enabled` fields, `config.example.toml` | `lens-contract` | config field is a wire contract other tooling (`docsgen`) generates from |
| API + UI surface | file as child of `#56` | `api_extensions.go`, `ui/src/extensions/` | `lens-maintainer` | new user-facing state machine (available/enabled/active/degraded/skipped/pending-restart) must be legible to an operator with no prior context |

## File and link the beads

- `#56` itself has no filed children yet — file the three rows above as
  child issues linking to this doc's matching `##` heading.
- Each issue's acceptance criteria should restate this doc's per-family
  `Enabled` default (`true` for existing config, no field at all for core
  capabilities) so "non-disableable core" isn't re-litigated per bead.
- Add the suggested lenses to each issue.
