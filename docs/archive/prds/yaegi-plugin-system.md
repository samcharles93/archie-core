# archie-core Yaegi Plugin System -- PRD

**Author:** Archie (Hermes agent)  
**Date:** 2026-07-21  
**Status:** Draft

---

## Summary

Embed the [Yaegi](https://github.com/traefik/yaegi) Go interpreter to let
archie-core load and execute `.go` files at runtime. This enables three things
that shell commands can't do: **custom gates with deep worktree inspection**,
**custom workflow stages defined per-repo**, and **skill scripts written in Go
instead of shell**.

---

## Why Yaegi

### The problem with shell-only gates

`[[repos.gate]]` is command lists. That's fine for standard tooling (`go vet`,
`golangci-lint`), but it can't express project-specific rules:

- "No new `panic()` calls in the diff"
- "No new dependencies in `go.mod` without justification"
- "All public functions in `pkg/api/` must be documented"
- "No `time.Sleep` in production code paths"

These are rules that need to read the diff, parse Go ASTs, or walk the worktree
-- things shell scripts can do but shouldn't have to.

### Why not shell scripts?

Shell scripts for gates are fine until they're not. They can't import the
archie-core types, can't use the Go parser, and are a second language to
maintain alongside the daemon. Yaegi runs Go -- the same language as the daemon,
the same language as the repos archie-core works on.

### Why not Go's native `plugin` package?

Go's `plugin` package (`.so` files) requires:

- Exact same Go version and deps as the host binary
- Linux/macOS only
- No unload
- Painful distribution

Yaegi interprets `.go` source files directly -- no compilation step, portable
everywhere, loads from filesystem paths.

### Production precedent

- **Traefik:** 200+ community plugins via Yaegi, loaded from plugins.traefik.io
- **Vikunja:** Adopted Yaegi for plugin system (see PR #2178), loads plugins
  from `plugins/` directory

---

## Design

### Three extension surfaces

| Surface                    | Location                              | When loaded                                  | What it does                                                          |
| -------------------------- | ------------------------------------- | -------------------------------------------- | --------------------------------------------------------------------- |
| **Custom gate functions**  | `.archie/gate.go`                     | During the gate stage (after shell commands) | Returns `[]Finding` -- each finding has severity, message, file, line |
| **Custom workflow stages** | `.archie/stages/<name>.go`            | When the workflow references the stage name  | Returns a `workflow.Stage` implementation                             |
| **Skill scripts**          | `.archie/skills/<skill>/scripts/*.go` | When the parent skill is activated           | Called by the skill's instructions                                    |

### Gate functions (`.archie/gate.go`)

The most important extension surface. After the shell-based gate passes,
archie-core loads and evaluates `.archie/gate.go` in the worktree directory. The
script exposes a `Check` function:

```go
// .archie/gate.go  --  per-repo gate rules interpreted at runtime
package gate

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
)

// GateContext carries everything the gate checker can inspect.
type GateContext struct {
    Diff        string   // unified diff of all changes
    ChangedFiles []string // list of changed file paths (repo-relative)
    Dir         string   // absolute worktree path
    BaseRef     string   // base branch (e.g. "origin/main")
    Repo        string   // "owner/name"
}

// Finding is one gate violation.
type Finding struct {
    Level    string // "error" (blocks commit) or "warn" (advisory)
    File     string // optional file path
    Line     int    // optional line number
    Message  string
}

// Block defines a named rule that can prevent passing.
type Block struct {
    Name    string
    Message string
}

// Check returns findings for the gate. Return nil for a clean gate.
func Check(ctx GateContext) []Finding {
    var findings []Finding

    // Rule: no new panic() calls in committed code
    for _, line := range strings.Split(ctx.Diff, "\n") {
        if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
            if strings.Contains(line, "panic(") {
                findings = append(findings, Finding{
                    Level:   "error",
                    Message: "new panic() call  --  use error returns instead",
                })
            }
        }
    }

    // Rule: no time.Sleep in non-test files
    for _, f := range ctx.ChangedFiles {
        if strings.HasSuffix(f, "_test.go") {
            continue
        }
        path := filepath.Join(ctx.Dir, f)
        b, err := os.ReadFile(path)
        if err != nil {
            continue
        }
        if strings.Contains(string(b), "time.Sleep") {
            findings = append(findings, Finding{
                Level:   "warn",
                File:    f,
                Message: "time.Sleep in non-test code  --  use time.Ticker or context.WithTimeout instead",
            })
        }
    }

    return findings
}
```

The daemon:

1. Reads `.archie/gate.go` from the worktree
2. Creates a Yaegi interpreter with `GateContext` and `Finding` types pre-loaded
   via `Use()`
3. Evaluates the file
4. Calls `Check(ctx)` via `Eval("gate.Check")` + `Interface()`
5. Logs findings and blocks the gate if any have `Level: "error"`

### Custom workflow stages (`.archie/stages/`)

A repo can define custom pipeline stages that archie-core discovers and makes
routable:

```go
// .archie/stages/run-migrations.go
package stages

import "github.com/samcharles93/archie-core/internal/workflow"

func Stage() workflow.Stage {
    return workflow.Stage{
        Name: "db-migration",
        Run: func(ctx context.Context, tc *workflow.TaskContext) error {
            // Check if go.mod contains migration tools
            // Run migration tests
            // Verify schema compatibility
            return nil
        },
    }
}
```

Registered stages appear alongside built-in stages. A workflow in `config.toml`
can reference them by name:

```toml
[[workflows.stages]]
name = "db-migration"        # matches the .go file
source = "repo"              # loaded from .archie/stages/
```

### Skill scripts (`.archie/skills/<skill>/scripts/`)

Skills can ship Go scripts that archie-core interprets:

```go
// .archie/skills/security-audit/scripts/gitleaks.go
package main

import (
    "fmt"
    "os/exec"
    "context"
)

func main() {
    ctx := context.Background()
    cmd := exec.CommandContext(ctx, "gitleaks", "detect", "--no-git", "--verbose")
    out, _ := cmd.CombinedOutput()
    fmt.Println(string(out))
}
```

Skills reference these scripts naturally in their instructions: "Run
`scripts/gitleaks.go` to scan for secrets."

---

## API surface archie-core exposes to Yaegi

Via `i.Use()`, the daemon pre-loads symbols that interpreted code can import:

| Package                         | What's exposed                            |
| ------------------------------- | ----------------------------------------- |
| `archie-core/internal/workflow` | `Stage`, `TaskContext`, `Outcome`         |
| `archie-core/internal/gate`     | `GateContext`, `Finding`, `Block`         |
| `stdlib`                        | Full Go standard library (stdlib.Symbols) |

Generated with `yaegi extract` -- same approach Traefik and Vikunja use. A
`//go:generate yaegi extract ...` line in the source triggers symbol table
generation during `go generate`.

---

## Limitations

From Yaegi's own docs:

- **No assembly files** (`.s` not supported)
- **No CGo** (can't call C code -- the daemon is pure Go, so this doesn't
  matter)
- **No compiler/linker directives** (no `//go:embed`, etc. -- fine for gate
  scripts)
- **Interfaces from pre-compiled code can't be added dynamically** -- the daemon
  must pre-compile wrapper types for any interface interpreted code needs to
  implement
- **Slower than compiled code** -- gate scripts run once per commit, not in hot
  paths; negligible impact

The Vikunja PR documents a specific Yaegi quirk: **typed factory functions
required.** You can't do `v.Interface().(MyInterface)` on a value returned from
interpreted code. Each interface needs its own factory:

```go
func NewGate() Gate             { return &MyGate{} }
func NewReportable() Reportable { return &MyGate{} }
```

---

## Implementation phases

### Phase 1: Gate functions -- implemented

- `yaegi` added as a dependency
- Symbol table for `GateContext`, `Finding` generated via `yaegi extract` into
  `internal/gate/gateextract` (a separate package from `internal/gate` itself,
  avoiding the self-import cycle extracting a package's own symbols creates)
- `internal/gate/gateeval.Evaluate`: loads `.archie/gate.go` from the worktree,
  interprets it, calls `Check()`; panics are recovered
- Wired in as `workflow.StageYaegiGate`, run after the final commit in both the
  `implement` and `tdd` workflows
- Error findings park the task; warn findings are logged only

### Phase 2: Custom stages -- implemented

- Symbol table for `workflow.Stage`/`TaskContext`/`Outcome` generated into
  `internal/workflow/wfextract`
- `internal/workflow/wfeval.Discover` scans `.archie/stages/*.go` in filename
  order and calls each file's exported `Stage()` function
- `TaskContext.CustomStages` is an injected `func(dir string) ([]Stage, error)`
  -- the composition root (`cmd/archied`) wires it to `wfeval.Discover`, keeping
  `internal/workflow` decoupled from its own generated symbol table
- `workflow.StageRepoStages` runs discovered stages in order right after the
  worktree is prepared, in both `implement` and `tdd`
- Simplification vs. the original sketch: stages are auto-discovered and always
  run (like `.archie/gate.go`) rather than referenced by name from `config.toml`
  -- the workflow engine has no data-driven stage graph today, so wiring
  arbitrary named stages into arbitrary positions would need a larger
  config-driven rework. Auto-run-in-order covers the PRD's actual use case (a
  repo-defined pre-flight/setup hook) without it.

### Phase 3: Skill scripts -- implemented

- `internal/skillscript.Run` interprets a `.go` file via `i.EvalPath`, which
  (like `go run`) evaluates and calls its `main()`; stdout/stderr are captured
  via `interp.Options`
- Uses `stdlib.Symbols` + `stdlib/unrestricted.Symbols` (not just `stdlib`) --
  `os/exec` lives in the unrestricted set, and the PRD's own example script
  shells out via `os/exec`
- No archie-core-specific symbol table needed here -- skill scripts only touch
  the Go standard library
- Exposed to the agent loop as a new `run_go_script` tool
  (`internal/agentexec/inprocess.go`), available in every agent stage; path is
  validated to stay inside the workspace
- This is how "the skill's instructions reference a script" cashes out
  concretely: the agent calls `run_go_script` with the path instead of needing a
  Go toolchain in the sandbox

### Phase 4: Hot reload -- not needed, by construction

- All three loaders (`gate.Evaluate`, `wfeval.Discover`, `skillscript.Run`) read
  their `.go` source from disk on every call -- no in-memory cache exists
  anywhere in the implementation
- Every task additionally gets a fresh `git clone` (`worktree.Manager.Prepare`
  removes and re-clones the worktree directory per task)
- Consequently every stage already re-reads `.archie/gate.go`,
  `.archie/stages/*.go`, and skill scripts fresh, every run -- there is no stale
  state a file watcher would ever need to invalidate
- A file-watching daemon-runtime reload mechanism would add machinery with
  nothing to do given this architecture; revisit only if the daemon starts
  reusing long-lived worktrees across tasks

---

## Open questions

1. **Symbol table generation:** Run `yaegi extract` as a `go generate` step, or
   generate on daemon startup?
2. **Error handling:** If the interpreted gate panics or times out, park the
   task? Or fall back to shell-only gate?
3. **Budget:** Interpreted gate scripts run in the gate stage -- should they
   have their own timeout/step budget?
4. **Template system:** Should archie-core ship with common gate templates (e.g.
   `go: no panic, no Sleep, no hardcoded paths`)?
5. **Remote gates:** Could `.archie/gate.go` be pulled from a shared remote
   registry, like skills?
