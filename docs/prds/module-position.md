# Module position -- design

**Status:** Draft, awaiting sign-off (not yet in `docs/architecture/`)
**Date:** 2026-09-03
**Parent:** `docs/prds/eda-playbook-engine.md`, epic `archie-core-t2db`

## Problem

`eda-playbook-engine.md` names four logic positions a playbook action can
target: Workflow, Module, Channel, Forge. Workflow (`t2db.9`/`.10`/`.11`)
and the standalone lint tool (`t2db.12`) are shipped. Module -- the
position that makes an action "not restricted to code-generation" -- has
not started, because it needs a schema-generation mechanism the parent
doc deliberately deferred: "This document does not attempt to design the
full generator today." This is that design.

## Do not invent a new mechanism -- generalize the one already shipped three times

Yaegi-interpreted extension code with a fixed, resolved-by-name exported
function already exists in this codebase for three surfaces, and all three
follow the identical shape:

| Surface | Fixed signature | Export path | Symbol table |
|---|---|---|---|
| Custom gate (`internal/gate/gateeval`) | `func(gate.GateContext) []gate.Finding` | `"gate.Check"` | `gateextract.Symbols` |
| Workflow stage plugin (`skillbuild`) | stage-specific | skill-declared | `wfextract.Symbols` |
| Skill plugin (`internal/skill/plugin.go`) | `func(string) string` | `"main.Run"` | none (stdlib only) |

Each is: read a `.go` file -> `yaegiutil.New` with that surface's symbol
table -> `yaegiutil.Resolve[T](i, src, exportPath)` -> call the resolved
function wrapped in `yaegiutil.Safe` for panic recovery. **Module is a
fourth instance of this same shape**, parameterized by action kind instead
of hardcoded to one fixed signature. No new interpreter construction, no
new panic-recovery mechanism, no new symbol-table generation tool --
`go generate ... yaegi extract` is already how `wfextract`/`gateextract`
are produced today (see their `//go:generate` directives), and Module
schema generation reuses that exact command.

## No generic `Module` interface -- one typed contract per action kind

The plugin engine rule's invariant 1 requires "real capability-specific
operations," and invariant 5 forbids adding capability methods to a
generic interface. A single `Module` interface with `Run(args any) (any,
error)` would be exactly the untyped hook the rule (and
`event-sources-and-reactions.md`'s rejection of a generic `Source`
interface) exists to prevent -- restating the mistake the whole EDA
document was written to move away from (`wfextract`'s hardcoded
`reflect.ValueOf(workflow.Implement)` table is what a closed, un-typed-per-
case system already looks like in this repo).

Instead, **each action kind is its own tiny package with its own
generated contract**, exactly like `gate.GateContext`/`gate.Finding` are
one hand-written Go file that `gateextract` turns into a Yaegi symbol
table:

```go
// internal/domain/eda/module/notify/types.go (hand-written, the schema source)
package notify

// SchemaVersion is independent of a playbook's own workflow_version, per
// eda-playbook-engine.md's schema-versioning point.
const SchemaVersion = 1

type Args struct {
    Channel string
    Message string
}

type Result struct {
    Delivered bool
}

//go:generate go run github.com/traefik/yaegi/cmd/yaegi extract -name notifyextract github.com/samcharles93/archie-core/internal/domain/eda/module/notify
```

A Yaegi module implementing this kind exports a fixed function name
against these generated types, resolved the same way `gate.Check` is:

```go
// operator's notify.go, interpreted at load
package notify

func Run(a notify.Args) notify.Result { ... }
```

`internal/domain/eda/module/notify` is the schema; `notifyextract.Symbols`
is its generated Yaegi bridge; the operator's file is the implementation.
This is the same three-way split `internal/gate` / `gateextract` /
`.archie/gate.go` already has.

## The registry: `ModuleRegistry`, keyed by kind, not a service locator

Per invariant 2 (owning family manager) and invariant 4 (narrow host
access): a `ModuleRegistry` in `internal/domain/eda/module` maps a kind
name (`"notify"`, `"image-gen"`) to a resolved, type-erased invoker --
the same `map[string]reflect.Value`-shaped erasure `wfextract.Symbols`
already uses to bridge Yaegi and Go, not a new pattern and not exposed as
the plugin's contract with the daemon (the plugin still writes and is
checked against a fully typed `func(Args) Result`; the registry's
internal storage is the only place erasure appears, exactly as it already
does inside `wfextract`).

```go
type ModuleRegistry struct {
    // kind -> (validate args against generated schema, invoke, marshal result)
}

func (r *ModuleRegistry) Register(kind string, dir string) error // discovers <kind>.go, resolves against that kind's generated symbols
func (r *ModuleRegistry) Invoke(ctx context.Context, kind string, rawArgs map[string]any) (map[string]any, error)
```

`Invoke` decodes `rawArgs` into that kind's generated `Args` struct before
calling the interpreted function -- a shape mismatch is a reported load/
dispatch failure per the design doc's "schema defines the accepted
message" rule, not a silent zero-value fill. This mirrors `plugin.Registry`
(`internal/plugin/plugin.go`)'s `Register`/dispatch split, scoped to one
capability family instead of generic daemon plugins.

Lifecycle (invariant 3): stateless invocation is the default -- explicit
no-op start/health/stop, matching "Stateless providers may make these
operations explicit no-ops." A future Module kind that owns a resource
(e.g. a persistent connection) can add optional lifecycle methods later,
the same way `internal/memory.MemoryProvider`'s optional capability
contracts (system-prompt contributions, prefetch, shutdown) layer onto a
minimal required set without forcing every provider to implement them.

## Trust boundary (closes `eda-playbook-engine.md` open question 3)

Invariant 6 draws the line: operator-trusted in-process code vs.
repository-supplied code that must run in a container. Module code is
**operator-installed, in-process, daemon-privileged** -- the same trust
tier as `PluginDir` (daemon plugins) and `SecretEngineDir` (secret engine
plugins) already occupy, both Yaegi-interpreted and already running
in-process today. It is **not** repository-supplied code from a task
(that stays containerized, unchanged). A Module directory is therefore a
new config field the operator points at their own trusted directory,
mirroring `PluginDir`'s shape exactly -- not something a forge webhook or
an untrusted playbook source can populate.

## Playbook YAML shape for a Module action

```yaml
actions:
  - position: module
    kind: notify
    args:
      channel: telegram
      message: "build finished"
```

`args` is validated against `notify.Args` (via the registry's decode
step) before invocation -- an unknown or missing required field is the
same "dropped and reported to the caller" failure the routing layer
already applies to unrecognised kinds/labels, not a new error philosophy.

## Interaction with the unresolved execution-time gaps

`eda-playbook-engine.md`'s "Execution-time gaps" section already gates
this: mid-run failure semantics and idempotency keying "must be resolved
before Channel/Forge actions or multi-action playbooks ship." Module is
where those gaps stop being theoretical -- a `notify` action has a real
side effect. Concretely:

- A **first Module kind should be side-effect-free** (e.g. a `log`/`echo`
  kind that only writes to the daemon's own log) so the schema-gen ->
  registry -> playbook-dispatch mechanism can be proven end to end without
  yet needing an idempotency answer.
- **Any Module kind with a real external side effect (`notify`,
  `image-gen`, forge writes) is blocked on resolving those two gaps
  first** -- this document does not relitigate or resolve them, it
  inherits the existing gate.

## Recommended first slice

1. `internal/domain/eda/module` package: `ModuleRegistry`, the
   discover/resolve/invoke mechanics, generalizing `gateeval`'s pattern.
2. One proof-of-concept kind, side-effect-free (`log`), with its schema
   source + generated extract file, to prove schema -> `go:generate` ->
   Yaegi load -> registry -> playbook dispatch end to end.
3. Config field for the Module directory (`ModuleDir`, mirroring
   `PluginDir`), wired at daemon startup alongside the existing
   `PlaybookDirs` loading.
4. Do **not** implement `notify` or any side-effecting kind in this slice
   -- that is explicitly gated on the execution-time gaps above.

**Status 2026-09-03: first slice shipped (t2db.13).** The schema-gen ->
go:generate -> Yaegi load -> registry mechanism is proven end to end with
`internal/domain/eda/module/log` (hand-written schema + `logextract`
generated symbols) and `ModuleRegistry` (Register/Invoke, strict arg
decode, panic recovery via yaegiutil -- no new interpreter-construction
or recovery mechanism). `ModuleDir` config field mirrors `PluginDir`;
wired in bootstrap.go with log-and-abort on load failure.

Playbook YAML action dispatch is **deliberately out of this slice**: a
minimal 'run this one module action, no chaining' dispatch still requires
the parent doc's data-flow mechanism (now resolved: CEL expressions
over an evaluation context -- see eda-playbook-engine.md's resolved open
question 1) and what the result feeds. Dispatch is a follow-up ticket
that lands once the coordinator exists, and it will consume this
registry as-is.

## Open questions

1. **Discovery convention.** Does a Module kind's implementation file need
   to declare its kind name explicitly (frontmatter-style, like a skill's
   `metadata.archie.workflow`) or is the kind inferred from directory/file
   naming (`module/notify/impl.go` implies kind `notify`)? Follow the
   skill catalog's existing declared-metadata convention unless there's a
   reason not to -- not decided here.
2. **Where schema source files live.** This doc assumes
   `internal/domain/eda/module/<kind>/types.go` per kind, but the exact
   package layout (one package per kind vs. one package with per-kind
   subtypes) should be confirmed against `organisation.md`'s file-layout
   rule before the first slice starts.
3. **Result surfacing.** A Module's `Result` needs to reach later actions
   in the same playbook once data-flow is designed -- resolved in the
   parent doc as CEL expressions over `actions.<id>.result.<field>` (see
   eda-playbook-engine.md's resolved open question 1); the `map[string]any`
   result shape above is what the evaluation context exposes, so it stays
   compatible by construction.
