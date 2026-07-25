---
name: go-quality-gate
description: >
  Run the full Go quality pipeline: gofumpt, go fix, golangci-lint, go test -race,
  and task deadcode. Use before any commit in a Go project. Triggered automatically
  by archie-core's implement and tdd workflows via the per-repo gate config.
version: 1.0.0
metadata:
  archie:
    tools: [go, golangci-lint, gofumpt, task]
    engine: any
---
# Go Quality Gate

## When this runs

arcie-core enforces this gate after every builder agent stage. The specific commands
come from the repository's `[[repos.gate]]` config, not this skill. This skill documents
what a correct gate looks like and how to diagnose failures.

## Standard gate (Tau and similar Go projects)

Run in this exact order. archie-core does NOT run these  --  the `ai-sdk/agentloop` gate
does. This list is what you should configure in `config.toml`:

```toml
[[repos.gate]] = ["gofumpt", "-w", "."]
[[repos.gate]] = ["go", "fix", "./..."]
[[repos.gate]] = ["golangci-lint", "run", "./..."]
[[repos.gate]] = ["go", "test", "-race", "-count=1", "./..."]
```

Or if the project uses Task:

```toml
[[repos.gate]] = ["task", "check"]
```

The LAST command in the gate list is the test runner by convention. archie-core's
TDD workflow inverts only that last command during the repro stage.

## Diagnosing gate failures

### gofumpt / go fix
These mutate files. If they change anything, the next `git diff` will catch it.
The agent should re-run them after making changes.

### golangci-lint
Common failures and fixes:
- `unused`  --  remove the unused function or add `//nolint:unused`
- `errcheck`  --  handle the error or assign to `_`
- `gosec`  --  review the security finding; never blindly suppress

### go test -race
Data races are non-deterministic. If `-race` fails:
1. Identify the race with `go test -race -run=TestName`
2. Add a mutex or channel to protect the shared state
3. Re-run with `-count=5` to confirm it's gone

### task deadcode
Scoping rule: if deadcode fails on packages you didn't touch, scope it:
```bash
golangci-lint run --tests=false --enable-only=unused,staticcheck ./changed/pkg/...
```

## When the TDD workflow runs

During `repro-tests`, the test command's expectations are INVERTED  --  it must FAIL.
During `fix`, the full gate runs normally and test files are write-protected.
The agent cannot modify tests in the fix stage; it must fix the code to make them pass.
