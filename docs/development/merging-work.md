# Merging work

Parallel lanes (see `.agents/skills/archie-agent-fleet`) merge into an
integration branch before `main`. Each merge is a change in its own right and
passes the same gate.

## Resolving conflicts

- **Generated files** (`*.pb.go`, `postgresdb/*.go`, `ui/dist/`,
  `docs/data/generated/*.json`): take either side, then regenerate. Never merge
  them by hand.
- **One side deletes a file, the other edits it**: the deletion wins when the
  deleting lane replaced the thing the file served. Move the other side's
  change onto its replacement.
- **One side changes how code is built (a new store, renamed packages), the
  other what it does**: take the behaviour side's version and reapply the
  mechanical change to it. That is usually cleaner than merging line by line.
- **Error sentinels** are rows in `wireErrors`, not switch cases.

## After resolving

1. Search for leftovers of anything the other side removed: the old package
   path, the old type, and comments describing the old behaviour.
2. Tests written against one side's semantics may pass there and fail after
   the merge. A test that used to pass on the old store may be asserting a rule
   the other side deliberately changed. Fix the test's setup to the merged
   rule; do not loosen the rule to the test.
3. Run the full suite without `-short`, then `task check`, then commit. Check
   `git status` for files a test wrote into the tree before staging.
4. **A merge can surface a bug neither lane's own tests exposed alone.** Two
   real cases from 2026-09-26: a test that dialled no NATS URL passed on a
   machine with an ambient dev server running and failed the moment it landed
   on a clean CI runner (start an ephemeral server per test, never rely on a
   default port resolving to something); and a cycle-detector that walked a
   Go map in range order reported the same cycle two different ways from run
   to run, invisible until enough fresh (uncached) runs hit the unlucky
   ordering. Both looked like "the merge broke a passing test" but were
   latent defects the merge only had the opportunity to expose. Fix the
   defect the failure is pointing at, not the assertion that caught it — see
   CLAUDE.md's test-quality bar before writing the replacement.
5. `go test`'s cache reuses a package's last result when nothing in its
   dependency graph changed. A merge that touches one package can leave a
   sibling's flaky test silently cached "pass" locally while CI runs it
   fresh. `task check` passing on your machine is not proof a merge is
   clean — rerun anything nondeterminism-prone with `-count=1` before
   trusting it, and let CI's from-scratch run be the tiebreaker it is.
