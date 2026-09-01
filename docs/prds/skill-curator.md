# Skill curator (reference implementation) -- decision

**Status:** Approved for archie-core-1786637499206 implementation
**Date:** 2026-09-02
**Beads issue:** `archie-core-1786637499206`
**Parent epic:** `archie-core-1786637500725` (curator engine)

## Decision

The skill curator's v1 scope is **mechanical validation and safe
normalization only** -- it never rewrites skill prose, never deletes a
skill, and never calls the model. Each pass:

1. Lists every skill under one configured root.
2. Reads each `SKILL.md` and attempts to parse its frontmatter
   (`internal/skill.Parse`).
3. Records an `Action` for any structural problem: a parse failure, or a
   missing `name`/`description` in the frontmatter that parsed.
4. Normalizes trivial whitespace (trailing whitespace per line, exactly
   one trailing newline) and writes back **only** when normalization
   actually changes the bytes, recording an `Action` for that write.

Nothing here needs judgment. "Review, prune, and improve" -- the epic's
framing -- is real for v1 but deliberately narrow: review is structural
validation, improve is whitespace normalization, and prune is *reporting*
a problem, never removing a skill. `Manifest.Skills = true` still marks
this curator agentic (existing `agentic()` rule), so it receives model
access from the registrar -- unused today, but nothing about the contract
or the runtime needs to change to let a later version reason about
content quality using it.

## Why this scope, not more

An autonomous process that rewrites or deletes a user's skill content
is a real risk with no undo path today (no draft/approval flow for
curator writes exists yet, unlike the workflow engine's PR-based review).
The epic's own stated purpose for this issue is proving the curator
contract holds for a second, structurally different consumer than the
session-memory curator (files-as-skills vs. observations-as-memory), not
solving "what makes a skill good." Mechanical validation is real,
useful, safe to run unattended, and fully exercises
List/Read/Write --  every method `curator.SkillStore` declares except
`Delete`, which this curator's Pass never has a safe reason to call in
v1 (kept implemented on the store adapter regardless, since the
interface requires it and a future consumer may).

## Why `internal/skill` isn't `curator.SkillStore` directly

`internal/skill.Discover` parses every `SKILL.md` under a root and
returns an error for the **whole** call if any one of them fails to
parse -- the read side was built for "load the catalog to start the
daemon," where one bad skill failing the whole boot is arguably correct,
not for "review each skill and report what's wrong with it
individually." A per-skill parse failure must become one `Action`, not
abort the pass before any skill is reviewed. So
`internal/infrastructure/skillcurator` gets its own thin store adapter
that lists raw directory names and reads/writes raw `SKILL.md` bytes
per name; the skill curator itself calls `internal/skill.Parse` on each
one, isolated per skill.

## Root scope

One root only, resolved the same way `loadWorkflows` already resolves
`skillsBase` (`cfg.SkillsDir`, falling back to `cfg.WorkDir`) -- not
every root `skill.CatalogRoots`/`DefaultRoots` would read for a model
(which layers a shared dir over the per-profile one). Write and Delete
need one unambiguous target; a curator that could write into a shared,
multi-tenant skills directory is a bigger decision than this issue
covers. The model still sees the full multi-root catalog through the
existing read path -- this curator only maintains the local one.

## Shape

### `internal/infrastructure/skillcurator/store.go`

```go
// Store implements curator.SkillStore over one root directory's
// .agents/skills/*/SKILL.md files, tolerating a per-skill parse failure
// as the curator's problem to report rather than this store's problem
// to abort on.
type Store struct { root string }

func NewStore(root string) *Store

func (s *Store) List(ctx context.Context) ([]curator.SkillRef, error)
func (s *Store) Read(ctx context.Context, name string) (curator.Skill, error)
func (s *Store) Write(ctx context.Context, sk curator.Skill) error
func (s *Store) Delete(ctx context.Context, name string) error
```

`Read` best-effort parses frontmatter for `Skill.Description` (empty on
parse failure -- not this method's job to report that; `Pass` does, via
its own `skill.Parse` call on `Skill.Content`). `Write` rejects a name
that does not already exist as a directory (this store curates existing
skills; creating a new one is out of scope). `Delete` removes the whole
skill directory, matching "delete a skill," not just `SKILL.md`.

### `internal/infrastructure/skillcurator/skillcurator.go`

```go
// Curator implements domain/curator.CuratorEngine. See
// docs/prds/skill-curator.md for what a pass does and why.
type Curator struct { /* unexported: registrar view, interval */ }

func New(interval time.Duration) *Curator

func (c *Curator) Manifest() curator.Manifest {
    return curator.Manifest{Interval: c.interval, Skills: true}
}

func (c *Curator) Check(ctx context.Context) (bool, error) // len(skills) > 0
func (c *Curator) Pass(ctx context.Context, in curator.PassInput) (curator.PassResult, error)
```

`Pass` walks every skill via `Bind`'s `Skills` (the filtered
`SkillStore` the registry already narrows to declared curators, see
`Registry.filter`), classifying each into exactly one of:

- **parse failure** -- `Action{Type: "skill.invalid", Reason: "frontmatter failed to parse"}`
- **missing required field** -- `Action{Type: "skill.incomplete", Reason: "name" | "description"}`
- **normalized** -- content changed by whitespace cleanup, written back,
  `Action{Type: "skill.normalized", Detail: "<name>: trimmed trailing whitespace"}`
- **clean** -- no action recorded (a pass with nothing to report is not
  an error, and not every skill needs an entry every time)

One pass reviews every skill currently on disk; there is no
per-skill "already reviewed, skip" state; this errs toward re-checking
over missing a regression, and the cost is cheap (no model calls, just
file reads).

## What we deliberately do NOT do

- **No LLM-authored rewrites of skill content.** Real "improve" in the
  sense of better prose is out of scope until there is a review/approval
  path for autonomous content changes to something a human wrote.
- **No automatic deletion of a skill**, however broken. `Delete` exists
  on the store for a future consumer; this curator's `Pass` never calls
  it.
- **No dangling-tool-reference checking** (a skill's
  `metadata.archie.tools` naming a tool that no longer exists). The
  mechanism to check "does this tool exist" belongs to a tool registry
  this curator has no access to and shouldn't be given for this --
  `curator.ToolBuilder.Build` resolves the curator's *own* declared
  tools, not an arbitrary existence check against skill-referenced
  names. A real fix here is a separate, better-scoped issue.
- **No multi-root awareness.** One configured root, matching
  `loadWorkflows`'s existing resolution, not the full layered catalog a
  chat turn sees.
- **No new curator-specific webui or chat surface.** Activity is visible
  through the existing curator observability surface
  (`archie-core-1786637489932`, already shipped) -- `/api/curators` and
  the dashboard Curators page already show any registered curator's
  recent actions.

## Acceptance criteria

Restated from the issue against this shape:

1. Registers and runs through `curator.Registry` with no curator-specific
   special-casing in the runtime -- proven by using the existing
   `Registry.Register`/`Runtime` path, no new registry method.
2. Its changes to skills are observable and attributable -- every write
   is preceded by an `Action` recording what changed and why, surfaced
   through the existing curator activity tracker.
3. It operates within its declared tool set and cannot reach tools it
   did not declare -- `Manifest.Tools` is empty; nothing about this
   curator ever touches `Registrar.Tools`.

## Files this change adds

- `internal/infrastructure/skillcurator/store.go` -- `curator.SkillStore` adapter
- `internal/infrastructure/skillcurator/store_test.go`
- `internal/infrastructure/skillcurator/skillcurator.go` -- the `CuratorEngine`
- `internal/infrastructure/skillcurator/skillcurator_test.go`
- `internal/app/archied/bootstrap.go` -- register it in `setupCurators`, root
  resolved the same way `loadWorkflows` resolves `skillsBase`
