# Wave 1 sub-prompt: shape first

**One child. fwmp alone.** Nothing else runs until it merges, and Wave 2 branches
from the merged shape.

xrux is no longer a Wave 1 probe. Its verdict is already measured with evidence
(Wave 0R claims audit c6) and the answer is **ZEROES**. Re-measuring buys nothing,
so the characterization test is folded into the `config` lane's red stage, where
it is committed and the evidence lives in the tree rather than in a transcript.
See the `config` brief in `lanes.json`.

---

## fwmp: the shared shape (cp-writer, worktree `/work/apps/cp-shape`, branch `cp/shape`)

Gate: `go test ./internal/domain/workflow/... ./internal/app/controlplane/... ./internal/app/agentworker/... -count=1`

```text
Bead archie-core-fwmp. Run `bd show archie-core-fwmp` first.

docs/prds/runtime-control-plane.md:95 says plugins can add step types. Today every
call site hardcodes workflow.BuiltinStepRegistry():
internal/app/agentworker/task_execution.go:319 and :335,
internal/app/controlplane/workflow_definitions.go:26 and :33, and
internal/app/controlplane/client.go:41. The step vocabulary is closed to the six
shipped workflows.

Verified by the Wave 0 claims audit, so treat as fact and do not re-derive:
- Those are exactly five non-test call sites, plus five test call sites.
- BuiltinStepRegistry() is defined at internal/domain/workflow/definition.go:149 and
  is built only from legacyBuiltinWorkflows() (definition.go:181).
- StepRegistry is a plain `map[string]StepFactory` (definition.go:32). There is no
  merge/widen function; BuiltinStepRegistry() is itself the only registration site.
- All five production call sites pass the identical function, so nothing diverges
  today. The gap is the absence of a guard, not a live defect.

Deliver: a registry assembled from the plugin set, resolved identically on both
sides.

The load-bearing constraint is not the extension point, it is agreement. The
validating side is the State Store; the executing side is archie-agent. A
definition that validates and then fails to compile at run time is the failure
this bead exists to prevent, and it is exactly the failure two packages that each
pass their own tests produce. No test today ties the two sides: no test file
imports both internal/app/controlplane and internal/app/agentworker. Your change
is not done until one test asserts that both sides resolve the same step
vocabulary from the same plugin set. Write that test first and let it fail for the
right reason.

Do NOT edit internal/domain/workflow/definition_test.go:8
TestParseDefinitionRejectsUnsafeDefinitions. The audit established it is not an
obstacle: its pin is name-exact ("shell" at :11, implement.prepare's own factory at
:12), so a settings-bearing plugin step type under any other name passes it
unchanged. Adding a step type is archie-core-8szi's lane, not this one.

Do NOT fix the workflow-version pin opportunistically. The audit found it is never
persisted over gRPC and its digest guard is dead in production; that is filed as
archie-core-zf8h and gets its own lane and Sam's eyes. Report it if you touch it,
do not repair it here.

Before adding a plugin engine surface, satisfy
ARCHITECTURE.md#plugin-engine-rule-strict.

This is the shape every other lane branches from, so it lands alone and it lands
first. Do not implement any step type; archie-core-8szi does that afterwards
against what you build. Do not widen scope to the .archie gate behaviour.

Working contract: print pwd, git branch --show-current and git status --short
before editing and repeat them in your report. Red then green. Package-scoped
tests only, never `task check`. No //nolint, no t.Skip, no deleted assertions.
Adopt `task fmt` verbatim. Commit on cp/shape with a conventional subject.
```
