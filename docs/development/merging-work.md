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
