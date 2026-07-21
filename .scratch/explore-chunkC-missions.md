# missions

Here is the complete report.

---

## 1. The `AgentStage` Struct (from `/work/apps/archie-core/internal/workflow/agent.go`)

**Lines 16-44.** The struct has 10 fields:

| Field | Type | Line | Purpose |
|---|---|---|---|
| `Name` | `string` | 17 | Stage name (e.g. `"plan"`, `"build"`) |
| `Role` | `string` | 20 | Model role key, looked up in `cfg.Models[Role]` — falls back to `cfg.Models["builder"]` |
| `ReadOnly` | `bool` | 21 | When true, disables write protection entirely |
| `Mission` | `func(*TaskContext) string` | 23 | Produces the task statement string sent to the agent |
| `Gate` | `func(*TaskContext) agentexec.Gate` | 26 | Returns the quality gate; nil = ungated |
| `ExtraRules` | `string` | 28 | Literal string appended to the agent's system prompt |
| `MaxSteps` | `int` | 31 | Overrides budget when > 0 |
| `ProtectGlobs` | `func(*TaskContext) []string` | 37 | Globs blocked from writes, combined with repo-configured suffix protection |
| `CaptureTools` | `func(*TaskContext) []agentexec.CaptureTool` | 40 | Structured-output tools whose calls return as data |
| `OnResult` | `func(*TaskContext, agentexec.Result) error` | 43 | Consumes a passed result |

---

## 2. How `AgentStage.Stage()` Calls `Mission` and Builds the `Request` (from `/work/apps/archie-core/internal/workflow/agent.go`)

**Lines 47-148.** The `Stage()` method is the adapter that converts an `AgentStage` into the engine's `Stage` type.

### The Request Construction (lines 94-109)

```go
req := agentexec.Request{
    Version:      agentexec.ProtocolVersion,         // 1
    TaskID:       tc.Task.ID,
    Attempt:      tc.Task.Attempt,
    Stage:        a.Name,
    Model:        modelRef,
    Mission:      a.Mission(tc),                      // <-- Mission called HERE
    ExtraRules:   a.ExtraRules,                       // <-- ExtraRules passed directly
    ReadOnly:     a.ReadOnly,
    Budget:       budget,
    Gate:         gate,
    Preflight:    preflight,
    Protection:   protection,
    Notes:        tc.Task.Notes,
    CaptureTools: captureTools,
}
```

Key observations:

- **`a.Mission(tc)` is called at line 100** to produce the `Mission` string. It receives the full `*TaskContext`, giving it access to `tc.Task.Title`, `tc.Task.Body`, `tc.Task.Plan`, `tc.Repo.FullName()`, `tc.Task.IssueNumber`, and any other field on `TaskContext`.
- **`a.ExtraRules` is used directly as a static string** (line 101). It is never processed, templated, or constructed — it is literally the `string` field set at AgentStage construction time.
- The result string from `a.Mission(tc)` is placed verbatim into `req.Mission`. It is not combined with ExtraRules there. The agent runtime receives both as separate fields and presumably concatenates or places them into the system prompt.

### The agent run (line 110)

```go
res, err := tc.Agent.Run(ctx, tc.Dir, req)
```

The `Request` is passed to the agent runner. The response is validated (lines 114-116), persisted notes are saved (lines 117-124), and if the result status is not `"passed"`, it returns an error (lines 136-142). Otherwise `a.OnResult` is called if set (lines 143-145).

---

## 3. Every `AgentStage` in Implement, TDD, and Feasibility

### 3a. Implement (`/work/apps/archie-core/internal/workflow/implement.go`)

**Stage: `"plan"` (lines 43-73)**

| Field | Value |
|---|---|
| `Name` | `"plan"` |
| `Role` | `"planner"` |
| `ReadOnly` | `true` |
| `MaxSteps` | `15` |
| `Mission` | Lines 48-64. Produces a `fmt.Sprintf` string: |
| | `"Produce a concrete implementation plan for this GitHub issue on the repository %s.\n"+prd+"\n<issue number=%d>\n# %s\n\n%s\n</issue>\n\nExplore the codebase with your read-only tools, then call finish with status \"passed\" and the plan as the summary: files to touch, the approach, acceptance criteria, and what tests should prove it. Keep the plan tightly scoped to the issue — call finish with status \"blocked\" if the issue is too vague or too large for one PR."` |
| | If `tc.Task.Plan != ""` (from a feasibility PRD), it prefixes: `"\n<approved_prd>\n" + tc.Task.Plan + "\n</approved_prd>\n"` |
| `Gate` | `nil` (no gate — read-only) |
| `ExtraRules` | `""` (empty) |
| `ProtectGlobs` | `nil` |
| `CaptureTools` | `nil` |
| `OnResult` | Line 65-72: Sets `tc.Task.Plan = res.Summary`, posts a comment to the issue |
| *Full mission example when no PRD exists:* `"Produce a concrete implementation plan for this GitHub issue on the repository owner/repo.\n\n<issue number=42>\n# Issue Title\n\nIssue body text\n</issue>\n\nExplore the codebase..."` |

**Stage: `"build"` (lines 75-98)**

| Field | Value |
|---|---|
| `Name` | `"build"` |
| `Role` | `"builder"` |
| `ReadOnly` | `false` |
| `MaxSteps` | `0` (uses default budget) |
| `Mission` | Lines 83-93. Template: |
| | `"Implement this GitHub issue on the repository %s, following the plan below.\n\n<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<plan>\n%s\n</plan>\n\nMake the smallest change that satisfies the issue and the plan's acceptance criteria. Do not run git — the orchestrator commits and pushes for you. When done, call finish with status \"passed\" and a summary written for the human who will review the pull request: what changed, why, and how it was verified."` |
| `Gate` | Line 78-79: `GateFromRepo(tc.Repo, tc.Cfg.Budgets)` — full quality gate |
| `ExtraRules` | Lines 81-82: `"Files matching the repository's protected suffixes (e.g. generated code) are write-blocked — edit their sources instead; the gate regenerates them."` |
| `ProtectGlobs` | `nil` |
| `CaptureTools` | `nil` |
| `OnResult` | Line 94-97: Sets `tc.BuildSummary = res.Summary` |

### 3b. TDD (`/work/apps/archie-core/internal/workflow/tdd.go`)

**Stage: `"analyse"` (lines 26-46)**

| Field | Value |
|---|---|
| `Name` | `"analyse"` |
| `Role` | `"planner"` |
| `ReadOnly` | `true` |
| `MaxSteps` | `15` |
| `Mission` | Lines 31-41. Template: |
| | `"Analyse this bug report for the repository %s and determine the problem surface.\n\n<issue number=%d>\n# %s\n\n%s\n</issue>\n\nExplore the code read-only and call finish with status \"passed\" and a summary containing: the root cause (file and function), the exact conditions that trigger it, the expected vs actual behaviour, and which test cases would prove the bug. Call finish with status \"blocked\" if you cannot locate a plausible cause."` |
| `Gate` | `nil` |
| `ExtraRules` | `""` |
| `ProtectGlobs` | `nil` |
| `CaptureTools` | `nil` |
| `OnResult` | Lines 42-45: Sets `tc.Task.Plan = res.Summary` |

**Stage: `"repro-tests"` (lines 48-69)**

| Field | Value |
|---|---|
| `Name` | `"repro-tests"` |
| `Role` | `"builder"` |
| `ReadOnly` | `false` |
| `MaxSteps` | `0` |
| `Mission` | Lines 57-68. Template: |
| | `"Write tests that REPRODUCE this bug in the repository %s. Do NOT fix the bug yet.\n\n<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<analysis>\n%s\n</analysis>\n\nAdd tests that pass once the bug is fixed but FAIL today because of it. The gate is inverted for this stage: it requires the full gate to pass EXCEPT %s which is required to FAIL. Do not touch non-test files. Call finish with status \"passed\" once the failing repro is in place, summarising which tests capture the bug."` |
| | Note: `%s` at the end is `testCommand(tc.Repo)` — the last gate command (e.g. `go test ./...`). |
| `Gate` | Lines 54-56: `tddReproGate(tc.Repo, tc.Cfg.Budgets)` — inverted gate where the last command expects failure |
| `ExtraRules` | `""` |
| `ProtectGlobs` | `nil` |
| `CaptureTools` | `nil` |
| `OnResult` | `nil` |

**Stage: `"fix"` (lines 92-123)**

| Field | Value |
|---|---|
| `Name` | `"fix"` |
| `Role` | `"builder"` |
| `ReadOnly` | `false` |
| `MaxSteps` | `0` |
| `Mission` | Lines 109-118. Template: |
| | `"Fix the bug in the repository %s. Failing repro tests are already committed; make them pass.\n\n<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<analysis>\n%s\n</analysis>\n\nThe full quality gate (including 'go test ./...') must pass. Make the smallest fix that makes the repro tests pass without changing them. Call finish with status \"passed\" and a summary for the PR reviewer: root cause, the fix, and verification."` |
| `Gate` | Lines 95-97: `GateFromRepo(tc.Repo, tc.Cfg.Budgets)` — normal gate |
| `ExtraRules` | Lines 107-108: `"The repro tests written in the previous stage are the bug's specification. Test files are write-protected in this stage — make them pass by fixing the code they exercise."` |
| `ProtectGlobs` | Lines 100-105: `tc.Repo.ResolvedTestGlob()` — protects test files |
| `CaptureTools` | `nil` |
| `OnResult` | Lines 119-122: Sets `tc.BuildSummary = res.Summary` |

### 3c. Feasibility (`/work/apps/archie-core/internal/workflow/feasibility.go`)

**Stage: `"assess"` (lines 28-73)**

| Field | Value |
|---|---|
| `Name` | `"assess"` |
| `Role` | `"planner"` |
| `ReadOnly` | `true` |
| `MaxSteps` | `15` |
| `Mission` | Lines 34-44. Template: |
| | `"Assess whether this feature request fits the project %s.\n\n<issue number=%d>\n# %s\n\n%s\n</issue>\n\nRead the repository's AGENT.md, ROADMAP.md, and README (whichever exist) plus enough code to judge scope and architectural fit. Then call the decide tool EXACTLY ONCE with fit=true or fit=false and your reasons, and afterwards call finish with status \"passed\" summarising the assessment."` |
| `Gate` | `nil` |
| `ExtraRules` | `""` |
| `ProtectGlobs` | `nil` |
| `CaptureTools` | Lines 33, 123-136: `decideCaptureTools` — provides a `decide` structured-output tool with boolean `fit` and string `reasons` |
| `OnResult` | Lines 45-72: Decodes the `decide` capture, sets `tc.decision`. If not a fit, closes the issue with `Outcome{Status: store.StatusClosedWontDo}` |

**Stage: `"prd"` (lines 75-95)**

| Field | Value |
|---|---|
| `Name` | `"prd"` |
| `Role` | `"planner"` |
| `ReadOnly` | `true` |
| `MaxSteps` | `20` |
| `Mission` | Lines 80-90. Template: |
| | `"Write a PRD for this accepted feature request on %s.\n\n<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<assessment>\n%s\n</assessment>\n\nExplore the affected code surface, then call finish with status \"passed\" and the PRD as the summary: problem, proposed solution, files/components affected, acceptance criteria, explicit non-goals, and estimated diff size. It will be read by a human deciding whether to green-light implementation."` |
| | The last `%s` is `tc.decision.Reasons` |
| `Gate` | `nil` |
| `ExtraRules` | `""` |
| `ProtectGlobs` | `nil` |
| `CaptureTools` | `nil` |
| `OnResult` | Lines 91-94: Sets `tc.Task.Plan = res.Summary` |

---

## 4. The `Workflow` Struct, `Stage` Struct, `Registry` Type, and `Route()`

### `Stage` (from `/work/apps/archie-core/internal/workflow/workflow.go`, lines 86-89)

```go
type Stage struct {
    Name string
    Run  func(ctx context.Context, tc *TaskContext) error
}
```

### `Workflow` (lines 92-95)

```go
type Workflow struct {
    Name   string
    Stages []Stage
}
```

### `Registry` (line 98)

```go
type Registry map[string]Workflow
```

### `Route()` (lines 103-141)

`Route(t *store.Task, reg Registry) Workflow`:

1. If `t.Workflow` is pre-assigned (non-empty) and exists in the registry, return it immediately (line 104-108).
2. Otherwise, iterate over `t.Labels` (comma-separated):
   - `"bug"` -> workflow `"tdd"`
   - `"feature"` -> workflow `"feasibility"`
   - `"bootstrap"` -> workflow `"bootstrap"`
3. No label match -> fall back to `"implement"`, then `"default"`.
4. If nothing found, returns a no-op "fail" workflow (lines 135-141).

---

## 5. What Would Need to Change to Replace Hardcoded Mission Strings with SKILL.md Content

This is a read-only analysis of the changes required. Here is the full scope:

### 5a. Where the strings live today

Every Mission is an inline `func(*TaskContext) string` closure that calls `fmt.Sprintf` with a hardcoded format string. The seven unique templates are:

| Workflow | Stage | File | Lines |
|---|---|---|---|
| implement | plan | `implement.go` | 48-64 |
| implement | build | `implement.go` | 83-93 |
| tdd | analyse | `tdd.go` | 31-41 |
| tdd | repro-tests | `tdd.go` | 57-68 |
| tdd | fix | `tdd.go` | 109-118 |
| feasibility | assess | `feasibility.go` | 34-44 |
| feasibility | prd | `feasibility.go` | 80-90 |

### 5b. The `ExtraRules` field is also hardcoded

| Workflow | Stage | String | File:Line |
|---|---|---|---|
| implement | build | `"Files matching the repository's protected suffixes..."` | `implement.go:81-82` |
| tdd | fix | `"The repro tests written in the previous stage are the bug's specification..."` | `tdd.go:107-108` |

### 5c. All the pieces that must change

**Layer 1: `AgentStage` struct definition** (`agent.go`, lines 16-44)

- The `Mission` field is currently `func(*TaskContext) string`. To source missions from a file, you have three options:
  - **A)** Keep the same signature but have the function read a file at runtime (not great — makes the function impure and harder to test).
  - **B)** Add a new field like `MissionSource string` (path to SKILL.md) and modify `Stage()` to load content from that path when `Mission` is nil. This cleanly separates inline missions from file-sourced ones.
  - **C)** Replace `Mission` entirely with a `func(*TaskContext) string` that loads and templates from SKILL.md. But you still need per-stage sections within the file.

**Layer 2: `Stage()` method** (`agent.go`, lines 94-109)

- Currently line 100: `Mission: a.Mission(tc)` — the mission is called synchronously.
- If files are loaded at construction time or stage-run time, this call site needs to either:
  - Use a pre-loaded template string (loaded during `Implement()`, `TDD()`, `Feasibility()` construction), or
  - Load the file at runtime inside the closure (less efficient but avoids an init-time dependency on the filesystem).

**Layer 3: `ExtraRules` sourcing**

- Currently a plain `string` field. If it should also come from SKILL.md, you need the same treatment: either load it at construction time or add a load-on-use path.

**Layer 4: How the template variables get injected**

The current missions use Go `fmt.Sprintf` with arguments like:
- `tc.Repo.FullName()` (passed as `%s`)
- `tc.Task.IssueNumber` (passed as `%d`)
- `tc.Task.Title` (passed as `%s`)
- `tc.Task.Body` (passed as `%s`)
- `tc.Task.Plan` (passed as `%s` — for implement build and tdd fix)
- `tc.decision.Reasons` (passed as `%s` — for feasibility prd)
- `testCommand(tc.Repo)` (passed as `%s` — for tdd repro-tests)

If you move to a SKILL.md file, you need a template language. Options:
- **Go `text/template`** — already in the stdlib, no dependency. You would pass `*TaskContext` as the data context. Templates like `{{.Repo.FullName}}`, `{{.Task.Title}}` replace the `%s` slots.
- **Keep `fmt.Sprintf`** with numbered placeholders — but then the SKILL.md author must know argument ordering, which is fragile.

**Layer 5: The SKILL.md file format**

You need a way to express multiple missions in one file. Options:
- **One SKILL.md per workflow** (e.g., `SKILL_implement.md`, `SKILL_tdd.md`, `SKILL_feasibility.md`) with YAML front-matter for per-stage sections.
- **One section per stage** delimited by headings (`## plan`, `## build`, etc.) and a convention for ExtraRules.
- **YAML front matter** with a `stages` map: each key is a stage name with `mission` and `extra_rules` fields.

Example YAML-front-matter approach:
```yaml
---
stages:
  plan:
    mission: |
      Produce a concrete implementation plan for this GitHub issue on the repository {{.Repo.FullName}}.
      ...
    extra_rules: ""
  build:
    mission: |
      Implement this GitHub issue on the repository {{.Repo.FullName}}...
    extra_rules: "Files matching the protected suffixes..."
---
```

**Layer 6: Where SKILL.md lives**

You need a lookup strategy. Likely candidates:
- Repository root (e.g., `.archie/SKILL.md`) — repo-specific missions
- Config-defined path (`cfg.SkillFile` or similar)
- Embedded in the binary as a fallback

**Layer 7: Loading and caching**

- When does the file get read? Per `Run()` call (every stage loads it), once at workflow construction in `Implement()`/`TDD()`/`Feasibility()`, or on daemon startup?
- If per-workflow construction, add a `loadSkillMissions(dir string)` helper called at the top of each workflow factory.
- Caching: the file is small, so a simple `sync.Once` per path or an `fsnotify` watch is overkill unless performance is critical.

**Layer 8: Fallback behavior**

- If no SKILL.md exists (which is the default for all repos today), the workflow must fall back to the current hardcoded missions. The simplest approach: keep the existing `Mission` closures as the zero-config default, and only apply SKILL.md content when the file is present.

### 5d. Concrete minimal change list

1. **`internal/workflow/agent.go`**: Optionally add a `MissionLoader func(*TaskContext) (string, error)` field or modify `Stage()` to detect and load from a path. Keep backward compat with inline `Mission`.

2. **`internal/workflow/workflow.go`**: Add a `SkillPath string` (or similar) to `TaskContext` so stage missions can locate the skill file from the worktree directory `tc.Dir`.

3. **Each workflow factory** (`implement.go`, `tdd.go`, `feasibility.go`): At the top of `Implement()`/`TDD()`/`Feasibility()`, attempt to load `tc.Repo.Dir + "/.archie/SKILL.md"` (or wherever configured). Parse per-stage templates. If the file doesn't exist, keep the existing inline closures as-is.

4. **Template parsing**: Introduce a package-level helper, e.g. `renderMission(tmpl string, tc *TaskContext) string`, that uses `text/template` against `*TaskContext`.

5. **`ExtraRules`**: Same treatment — source from the SKILL.md per-stage front matter when present.

6. **Test the fallback**: Every existing test must continue passing without a SKILL.md file present, proving backward compatibility.