# Review settings resource

**Status:** Draft
**Beads issue:** `archie-core-0ry6`
**Authority:** `docs/prds/runtime-control-plane.md` for the resource model,
`docs/architecture/configuration.md` for ownership, and
`docs/prds/pr-review-agent.md` for what the two dials do.

## Decision

`review.precision_gate` and `review.approve_before_post` are one control-plane
resource, `review-settings`. The file's `[review]` section seeds it, the stored
document outranks the file from then on, and the dashboard writes it through the
same settings machinery as every other resource.

The resource is deployment-wide, like every definition the State Store seeds
(`storecontract.DefaultOrgID`): not per-repository, per-identity, or per-run.

## Why its own kind

The two dials are pr-review policy — what a review may post, and whether a human
decides — not a limit on a task run. The document that carries task limits
belongs to that feature. `runtime-control-plane.md` splits settings by feature
and refuses documents unrelated features share; `configuration.md` puts a typed
runtime setting with the domain whose behaviour it describes. One document per
feature is the rule, not one document per settings page.

A new kind also has no legacy documents. Its first value is the seed derived
from the file, so "the store outranks the file" begins from an explicit copy of
both fields rather than from a document that predates them.

## Document

New file `internal/app/controlplane/review_settings.go`:

```go
const ReviewSettingsKind = "review-settings"

type reviewSettings struct {
    PrecisionGate     bool `json:"precision_gate"`
    ApproveBeforePost bool `json:"approve_before_post"`
}
```

Its `Definition` sets `ApplyMode: "live"`, `Document: reviewSettings{}`, and a
`Seed` returning `reviewSettings{cfg.Review.PrecisionGate, cfg.Review.ApproveBeforePost}`,
and a `Validate` wrapping `validateAs`: both fields are free booleans with no cross-field
rule, so the validator is a named place to put one rather than an inline no-op
(`validatePluginSettings` is the precedent). No `Normalize`, because the kind
has no second wire shape to converge.

## Seeding

`ImportConfig`/`seededValues` write the seed for a kind the store does not hold,
so a deployment that never edited the dials runs the values in `[review]`. Once
a document is stored, the file key stops mattering, which is the rule for every
database-owned setting.

## Apply

`runtimeConfigFrom` in `internal/app/controlplane/runtime_config.go` layers the
stored document over the file's `Review`, assigning `out.Review.PrecisionGate`
and `out.Review.ApproveBeforePost` field by field. Field-by-field, not
`out.Review = ...`: a field added to `config.Review` later keeps the file's value
until the document carries it.

`ReviewSettingsKind` joins `runtimeResourceKinds` in
`internal/app/archied/control_plane.go`, so a new stored version re-runs the
layering and republishes through `config.Holder`. Live rather than
restart-required, because every consumer re-reads it: `Review` is on
`reloadableFields` in `internal/app/archied/reload.go`, and the value reaches a
run through `Daemon.configFor` → `Config.ForTask` → `TaskConfig.ToConfig` →
`TaskContext.Cfg`, which `stagePRPrecisionGate` and `stagePROperatorApproval`
read. The next task run uses the new value; a run in flight keeps the value it
started with.

## Projection and page

`BuildConfigView` gains a plain `Review` field carrying both values out of
`cfg.Review`, the way `BudgetsView` carries the layered budgets, so the published
config snapshot tells any reader what is in force. The Review page renders the
two toggles from the control-plane store's draft for `review-settings` — the
shape `SystemTasksPage.vue` uses for `workflow-execution-settings` — and shows
the projected value beside them; a Settings route plus a `PAGE_RESOURCES` entry
(`ui/src/stores/control-plane.ts`) bind the page to the kind.

Nothing else re-advertises them. `configFieldDescriptors` and `ConfigView.Schema`
gain no `review.*` row: the config catalog's generic renderer was deleted, so a
descriptor row adds a field nothing can edit, which is the shape `becb0b5e`
removed.

## What the test proves

`internal/app/controlplane/runtime_config_test.go` adds the round trip that
builds the effective config the way boot does: seed the kind from a file config
with both dials set one way, store a document with both flipped, layer it
through `runtimeConfigFrom`, and assert the effective `cfg.Review` equals the
stored document. A second case covers the absent kind, where the file's values
stay in effect. The assertion is on `cfg.Review` — the value `TaskContext.Cfg`
carries into the two stages — not on a struct the test set directly.
`TestRuntimeResourceKindsApplyLive` gains `review-settings` as live. The
validator needs no `validators_agree_test.go` case: it wraps `validateAs` with
no rule, because no combination of the two booleans is invalid, and a case that
cannot fail is a test the project then carries for nothing.

## Rejected: extending workflow-execution-settings

That resource is `workflow.ExecutionSettings`, "the limits captured when a
workflow execution starts". Putting review policy in it makes the review feature
a field of the limits document, gives a boolean edit a ride on a document whose
candidate is validated and swapped as one unit with the budgets, and forces
absence-aware pointer fields onto booleans so a document stored before the fields
existed does not turn a file's `precision_gate = true` into `false`. It buys one
less kind at the cost of the ownership rule the model is built on.

## Out of scope

- Per-repository, per-identity, or per-workflow review settings. A scope key is
  a later decision with its own resource shape.
- Any other review knob. A field joins the document only with a consumer, the
  rule every config field carries.
- The reviewer pipeline's behaviour. This document places the settings; it does
  not change what they do.
