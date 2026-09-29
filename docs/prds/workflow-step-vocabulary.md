# Workflow step vocabulary: what a step type is and how binaries agree on it

**Status:** Approved
**Date:** 2026-09-29
**Beads:** `archie-core-m7oc` (agreement between separately deployed binaries), `archie-core-ze7y` (the deleted `.archie/stages/*.go` equivalent)
**Extends:** `docs/architecture/plugins-and-extensions.md` (plugin engine rule), `docs/prds/event-automation.md` (the workflow model step types serve)

**Authority:** `docs/architecture/plugins-and-extensions.md` — the workflow step vocabulary is a Workflow capability family, and its extension point follows the plugin engine rule.

## Decision

A workflow step type is a named Go factory: the word a YAML workflow writes in
a step's `type` field, plus the factory that builds its stage. The name is a
stable identifier; the identity also declares what plugin family claims it,
so a contribution that shadows a shipped stage is refused instead of
overriding it.

The Workflow domain owns the typed family contract — `StepTypeProvider` for a
contribution and `Manager` as the owning registry. A provider contributes a
whole set of step types under its own stable-identifier name; the manager
validates the entire contribution before applying any of it (named provider,
well-formed step-type names, non-nil factories, no step type claimed twice,
no claim on a name another provider holds) and applies it atomically. The
generic `plugin.Plugin` contract stays metadata-only.

The vocabulary is a compiled-in, closed set. `internal/infrastructure/workflowsteps`
is the one place a step type is bundled into any binary that resolves one: the
shipped workflow stages, the repository-hook replacements (`workflow.diff-rules`)
and the repository-free event steps each arrive as one provider, and adding a
step type means adding a provider to that set. Interpreted plugin sources
cannot contribute step types: the Yaegi plugin host satisfies only the
metadata contract, and no bridge to `StepFactory` exists.

Every binary that resolves a step type builds its manager from that same
provider set at its composition root, before its first resolution, and
injects the manager everywhere it resolves definitions. Agreement is per
build: the State Store, the daemon's workflow-definitions client and the agent
worker built from the same source register identical vocabularies by
construction; binaries built from different sources can disagree, and only a
matching deploy fixes that.

An unknown step type is refused twice, and never degraded. The validator
`ParseDefinition` refuses a definition naming an unregistered type at save
time, on the validating side the composition root built (the State Store's
workflow-definitions resource validates there before anything is stored). The
compiler refuses the same definition at run, so a binary handed a definition
written against a vocabulary it does not have fails that run loudly instead of
silently skipping steps. Both checks live in the Workflow domain against the
injected manager, which is why no binary can opt out of the check by
substituting its own registry.

## Relationship between the questions

`archie-core-ze7y` is the same question seen from the deleted
repository-authored stages: those `.archie/stages/*.go` files held arbitrary
stages, and their gate half landed as `workflow.diff-rules` settings rather
than interpreted Go. Their stage half has no replacement and does not get one
that approximates it: no interpreted-Go stage type is built, and no command
step type is created by this decision to stand in for them. The refusal of
`.archie/stages/*.go` stays until the maintainer's trust decision
(`archie-core-ndm8`) says otherwise.

If that decision ever allows a command step type, it arrives as a provider in
the compiled-in set under the rules above — a new contributor of exactly one
step type at a named provider. It is not a plugin contribution mechanism, and
it does not change any answer in this document.

## Rejected alternatives

- **A vocabulary version or hash on the wire.** The executing side already
  refuses an unknown step type at its first compile, which is the mismatch
  detector, and it fails closed. A wire field would only narrow the
  deploy-skew window that a matching deploy closes, at the cost of a new
  contract field.
- **A Yaegi-plugin-to-`StepFactory` bridge.** It would run repository-supplied
  interpreted code inside the daemon's process with daemon privileges. The
  trust boundary invariant requires repository-authored code to run in the
  task container, not in an interpreter embeddable into any binary.
- **A package-level process-global registrar.** Global mutable registry state
  is unreadable at the composition root, collides with itself when one
  process builds the vocabulary twice, and is what made the first attempt at
  this question worse instead of better.
- **Degrading on an unknown step type.** A run that skips steps it cannot
  compile would finish green while silently dropping the behaviour the
  definition asked for; refusal at save and at run is the only honest option.