# Backend-owned configuration schema for the webui -- decision

**Status:** Approved for `archie-core-b6ew` implementation
**Date:** 2026-09-02
**Beads issue:** `archie-core-b6ew` (epic), with children `.1`-`.5`

## Decision

The dashboard's Configuration page keeps reading a secret-free,
hand-picked view of `config.Config` -- that allowlist is a safety
boundary and stays exactly as it is. What changes is who defines the
page around it: today `ui/src/settings/settings.js` and
`config-row.js` hardcode every row's label, section, type, and
editability by hand; after this, the backend attaches that metadata to
each field it already allows through, and the frontend renders
sections and fields generically from what the backend sends.

This is **not** a reflection-based schema over `config.Config`.
`internal/config/config.go`'s own package doc states it is "scheduled
for dissolution: its types and methods are to be reassigned to the
domains whose behaviour they describe" -- building a generic
schema-from-struct-tags mechanism over a struct on its way out would be
building on a foundation this repo has already decided to remove.
Instead, every field already in `ConfigView` (`internal/webui/api_config.go`)
got there because someone deliberately vetted it as safe to show; the
schema adds a hand-authored descriptor to that same deliberate act,
not a new mechanism that re-derives safety automatically.

## Why now

Task #6's investigation (`/9/26`) found the concrete failure this
produces: `Repo.AllowConcurrent` and `Repo.MaxRetries` already reach
`GET /api/config` (`RepoView` in `api_config.go`), but the
repositories card renders a static read-only table that never shows
them, so the fix for "why isn't a second agent running" required an
agent tracing Go source instead of a checkbox on the dashboard.
`Repo.ReviewEnabled` is worse: it never reaches the API at all. Both
are symptoms of the same root cause -- the frontend decides what a
field means and whether it's shown, and nothing keeps that in sync
with what the backend actually allows.

## Shape

```go
// internal/webui/config_schema.go (new)

type ConfigFieldType string

const (
    FieldString     ConfigFieldType = "string"
    FieldInt        ConfigFieldType = "int"
    FieldBool       ConfigFieldType = "bool"
    FieldDuration   ConfigFieldType = "duration"
    FieldEnum       ConfigFieldType = "enum"
    FieldStructured ConfigFieldType = "structured" // repositories, models, providers
)

type ConfigField struct {
    Key             string          `json:"key"` // dotted path, matches UpdateConfig's key space
    Label           string          `json:"label"`
    Description     string          `json:"description,omitempty"`
    Type            ConfigFieldType `json:"type"`
    Value           any             `json:"value"`
    Editable        bool            `json:"editable"`
    LockedReason    string          `json:"locked_reason,omitempty"`
    Overridden      bool            `json:"overridden"`
    Options         []string        `json:"options,omitempty"` // enum only
    RestartRequired bool            `json:"restart_required"`
}

type ConfigSection struct {
    ID     string        `json:"id"`
    Label  string        `json:"label"`
    Fields []ConfigField `json:"fields"`
}
```

`handleConfig` builds `[]ConfigSection` from the existing per-field
values it already assembles, plus `overlay.DeniedKeys` for
`LockedReason` and the existing `Overridden` list -- both already
computed today, just re-attached per field instead of returned as
side-channel maps the frontend cross-references by key string.
`Locked`/`Overridden`/`Reload`/`Provenance` stay as top-level
`ConfigView` fields; they describe runtime state (what happened on
the last reload, which keys the overlay refuses), not the field
catalog, and duplicating that logic per-field would be the same drift
risk this change exists to remove.

`FieldStructured` fields (repositories, models, providers) carry
`Value` as today's structured JSON and are rendered by dedicated
editors, not the generic renderer -- see "What this does not do".

## Sections

Matches the cards the settings page already has: `identity`,
`repositories`, `models`, `providers`, `budgets`, `storage`,
`containers`, `web`. No new sections; this is metadata on existing
rows, not new configuration surface.

## Frontend

`config-row.js`'s `row(label, value, opts)` becomes the renderer's
private implementation detail, called generically:

```js
for (const section of schema.sections) {
  const rows = section.fields
    .filter(f => f.type !== "structured")
    .map(f => row(f.label, f.value, {
      key: f.key, type: f.type, options: f.options,
      locked: f.locked_reason, overridden: f.overridden,
      restartRequired: f.restart_required,
    }));
  // structured fields render through their dedicated editor (b6ew.4)
}
```

`settings.js` stops knowing that `containers.max_concurrency` is an
`int`, belongs in the "Sandboxed containers" card, or requires a
restart. It knows how to render a section and a field of a given type.

## What this does not do

- **Does not marshal `config.Config` wholesale or add reflection over
  it.** The allowlist boundary in `api_config.go` is unchanged; this
  adds a label next to a value that was already being shown.
- **Does not make every backend field appear automatically.** A field
  still requires someone to write its descriptor, same as it requires
  someone to add it to `ConfigView` today -- the fix is one
  source of truth instead of two, not zero review.
- **Does not solve structured editing generically.** Repositories and
  maps get their own typed editor (`archie-core-b6ew.4`), not a
  generic "array of objects" renderer -- a repo's fields have their
  own validation shape (owner/name pair identity, gate command
  argv) that a generic structured-field renderer would either get
  wrong or reinvent per-type anyway.
- **Does not change what `UpdateConfig`/`overlay.DeniedKeys` accept.**
  `Editable` on a field descriptor mirrors that existing policy; it
  does not grant new write access.

## Acceptance criteria (epic-level; see child issues for per-task detail)

1. Every field currently rendered as a hardcoded row in `settings.js`
   has a backend-authored descriptor with type, label, and section.
2. `Repo.ReviewEnabled` reaches the API and the dashboard, alongside
   `AllowConcurrent` and `MaxRetries`.
3. Adding a new scalar field to the schema requires one backend
   descriptor and zero frontend changes to appear correctly labeled,
   sectioned, and editable.
4. `TestHandleConfigNeverLeaksSecrets`-style coverage extends to the
   schema response; no descriptor exists for a secret-bearing field.
5. A schema-fixture-driven frontend test asserts the generic renderer
   needs no field-specific branch to render every scalar field type.

## Work breakdown

- `archie-core-b6ew.1` -- descriptor types + hand-authored section/field
  metadata for every existing `ConfigView` field.
- `archie-core-b6ew.2` -- `handleConfig` returns the schema-driven
  shape (depends on `.1`).
- `archie-core-b6ew.3` -- generic frontend renderer for scalar/bool/
  int/enum/duration fields (depends on `.2`).
- `archie-core-b6ew.4` -- structured editors: repositories (incl. the
  `ReviewEnabled` gap), models, providers (depends on `.2`).
- `archie-core-b6ew.5` -- contract tests for metadata completeness,
  secret-safety, and generic-renderer coverage (depends on `.3`, `.4`).
