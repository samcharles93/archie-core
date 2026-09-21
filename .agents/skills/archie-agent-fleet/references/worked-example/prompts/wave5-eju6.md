# Wave 5: eju6, the end-to-end restart test

One lane, one `cp-writer`, against merged `main`. This is the only bead that
proves the feature rather than adding to it, so it runs last and alone.

Gate: `cd <worktree> && go test ./internal/app/controlplane/... -count=1`,
then `task check` through the merge train as usual.

```text
Bead archie-core-eju6. Run `bd show archie-core-eju6` first.

docs/prds/runtime-control-plane.md ("First implementation") lists an end-to-end
restart test as a deliverable and there is none.
internal/app/controlplane/controlplane_test.go covers seeding and catalog only.
No test drives a change through the API, restarts a process, and asserts the
change survived.

Deliver that test: a real end-to-end check, not a unit test, and not a rewrite
of the existing file.

It must, in one test:
  1. write a settings change through the control-plane API, as the dashboard
     would, attributing to identity.SystemID;
  2. observe the change land in the audit trail as an ordinary edit;
  3. restart the process that consumes it;
  4. assert the process came back on the stored value, not the file value;
  5. assert the apply-status row for that process reports the version it
     applied, and that a record older than 90 seconds reads as "not reporting"
     rather than as current.

Facts you need and must not re-derive:
  - boot.runtimeConfig is the single layering call, and the SIGHUP reload makes
    the same call over the document it just resolved (9af013eb). A reload cannot
    revert a database-owned setting to its file value, and that is the property
    under test.
  - Execution budgets arrive on a watch, not a resource query, so boot records
    the last settings it applied and the reload re-applies them from there.
  - PutApplyStatus/ListApplyStatus are administrative on the State Store
    contract: a task-scoped grant is denied both by the deny-by-default arm of
    authorizesTaskScopedCall. Your harness needs the administrative token.
  - Rows are keyed on (process, kind) and replaced in place; every process
    re-stamps every 30 seconds.
  - The control plane fails closed: a stored value that will not validate stops
    archied starting.

If the test fails, that is a finding about the feature, not about the test.
Report the failure with the output and stop. Do not weaken an assertion, do not
add a retry loop to paper over a race, and do not mark it t.Skip.

If a real end-to-end restart cannot be driven in-process, say so explicitly and
describe what harness it would need, rather than substituting a unit test that
asserts less and claims the bead.
```
