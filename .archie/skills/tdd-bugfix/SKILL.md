---
name: tdd-bugfix
description: >
  Fix bugs using test-driven development. Write a failing repro test first, prove
  it captures the bug, then fix the code without touching the tests. archie-core
  routes issues labelled "bug" through this workflow automatically.
version: 1.0.0
metadata:
  archie:
    tools: [go, golangci-lint]
    engine: any
---
# TDD Bugfix Workflow

## When this runs

arcie-core routes any GitHub issue labelled `bug` through the TDD workflow.
The workflow has five stages: analyse → repro-tests → capture-proof → fix → deliver.

## Stage 1: Analyse (read-only planner)

The planner agent explores the codebase read-only and produces:
- Root cause: the exact file, function, and condition
- The expected vs actual behaviour
- Which test cases would prove the bug exists

No code is written. The plan is stored but not posted — the repro proof speaks louder.

## Stage 2: Repro Tests (write failing tests only)

The agent writes tests that PROVE the bug. Critical constraints:
- **Do not touch non-test files.** Only `*_test.go` (or the ecosystem's test glob).
- **The gate is inverted.** The test command (last in `[[repos.gate]]`) must FAIL.
  If it passes, the repro didn't capture the bug and the stage parks.
- **Make the failure explicit.** The test should assert the correct behaviour,
  not just `panic()` or `t.FailNow()`.

## Stage 3: Capture Proof (deterministic)

arcie-core runs the test command directly (not through the agent loop) and captures
the output. This is deterministic proof that the tests fail before the fix.

## Stage 4: Fix (code only)

The agent fixes the bug. Critical constraints:
- **Test files are write-protected.** The agent CANNOT modify `*_test.go`.
  This is an environmental constraint, not a prompt rule.
- **The full gate runs normally.** All quality commands plus the test suite must pass.
- **Make the smallest fix.** Change only what's needed to make the repro tests pass.

## Stage 5: Deliver

arcie-core:
1. Commits both the repro and the fix as separate commits (telling the PR story)
2. Pushes the branch
3. Opens a PR with the builder's summary
4. Posts the captured failure proof as a PR comment

## Common pitfalls

### The repro tests pass unexpectedly
This means the test doesn't actually capture the bug. Check:
- Is the test calling the right function with the right inputs?
- Is the assertion checking the correct behaviour (not the buggy behaviour)?
- Are there test setup issues (missing imports, wrong package)?

### The fix stage modifies test files
This shouldn't happen — test files are write-protected. If it does, the agent is
bypassing the constraint. The worktree should be inspected.

### The gate is misconfigured
If the repo has no `[[repos.gate]]` entries, TDD cannot run — it needs at least
one gate command, with the test runner last.
