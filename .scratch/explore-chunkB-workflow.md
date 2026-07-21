# workflow

Here is the full analysis across all five files.

---

## 1. Fields AgentStage reads from TaskContext to build the Request

The `AgentStage.Stage()` method (lines 47-148 of `internal/workflow/agent.go`) constructs a `agentexec.Request` and reads the following from `TaskContext`:

| Request field (line) | TaskContext source | Notes |
|---|---|---|
| `Version` (94) | Constant `agentexec.ProtocolVersion` | Not from TaskContext |
| `TaskID` (96) | `tc.Task.ID` | |
| `Attempt` (97) | `tc.Task.Attempt` | |
| `Stage` (98) | `a.Name` (the AgentStage field) | Not from TaskContext |
| `Model` (99) | `tc.Cfg.Models[a.Role]` | Falls back to `tc.Cfg.Models["builder"]` at line 54 if the role key is empty; returns an error at line 57 if still empty |
| `Mission` (100) | `a.Mission(tc)` | The Mission func receives the full `*TaskContext` — implement's plan/build missions read `tc.Task.Title`, `tc.Task.Body`, `tc.Task.Plan`, `tc.Task.IssueNumber`, `tc.Repo.FullName()` |
| `ExtraRules` (101) | `a.ExtraRules` | Static field on AgentStage, not read from TaskContext per se |
| `ReadOnly` (102) | `a.ReadOnly` | Static field |
| `Budget` (103) | `tc.Cfg.Budgets.MaxSteps`, `.MaxTokens`, `.WallClock.Std()` | Overridden by `a.MaxSteps` if > 0 (line 65-67) |
| `Gate` (104) | `a.Gate(tc)` | The Gate function receives TaskContext; implement's build stage calls `GateFromRepo(tc.Repo, tc.Cfg.Budgets)` (agent.go line 152-161) |
| `Preflight` (105) | `tc.Repo.ResolvedPreflight()` | Split into `agentexec.Command` structs (lines 86-92) |
| `Protection` (106) | `tc.Repo.Protect` + `a.ProtectGlobs(tc)` | Combined; set to zero Protection if `a.ReadOnly` is true (line 82-84) |
| `Notes` (107) | `tc.Task.Notes` | |
| `CaptureTools` (108) | `a.CaptureTools(tc)` | The CaptureTools function receives TaskContext |

At line 110, `tc.Agent.Run(ctx, tc.Dir, req)` is called, which also reads `tc.Dir` (the worktree directory path) and `tc.Agent` (the Runner interface).

The Mission function for the **plan** stage (implement.go lines 48-64) reads `tc.Task.Plan`, `tc.Task.IssueNumber`, `tc.Task.Title`, `tc.Task.Body`, `tc.Repo.FullName()`.

The Mission function for the **build** stage (implement.go lines 83-93) reads `tc.Task.IssueNumber`, `tc.Task.Title`, `tc.Task.Body`, `tc.Task.Plan`, `tc.Repo.FullName()`.

---

## 2. What happens after AgentStage.Run returns — Result processing

The full post-Run sequence is in `agent.go` lines 110-146:

1. **Line 111-113: Early-return on hard transport error.** If `err != nil && res.Version == 0`, the error is wrapped and returned immediately — the result is unusable (version 0 signals no valid response).

2. **Line 114-116: Validate.** `res.ValidateFor(req)` checks that the result is consistent with the request (e.g., tool call parameters match what was requested). Returns validation error.

3. **Lines 117-124: Persist appended notes.** Any entries in `res.AppendedNotes` are appended to `tc.Task.Notes` (prefixed with "- ") and saved via `tc.Store.Update(ctx, tc.Task)`. If the store write fails, the error is returned.

4. **Lines 125-127: Return on agent-level error.** If `err != nil` (and we've already passed the version-zero check), wraps and returns it as `"agent run: ..."`. This catches cases where the agent loop ran but produced an error.

5. **Lines 129-130: Accumulate usage.** `tc.Task.TokensUsed += res.TokensUsed; tc.Task.Iterations += res.Iterations`.

6. **Lines 131-134: Emit event.** `tc.Emit("agent_finish", a.Name, res.Summary, ...)` with status, stop_reason, tokens, iterations, model.

7. **Lines 136-142: Check status.** If `res.Status != agentexec.StatusPassed`, it returns an error of the form `"agent {status} ({stop_reason}): {detail or summary clipped to 2000 chars}"`. This handles "blocked", "idle", "parked", etc. — any non-passing status causes the stage to fail.

8. **Lines 143-145: OnResult callback.** If the stage's `OnResult` func is non-nil, it is called with `(tc, res)`. The implement workflow's plan stage OnResult (lines 65-72 of implement.go) sets `tc.Task.Plan = res.Summary` and posts it as a forge comment. The build stage OnResult (lines 94-97 of implement.go) sets `tc.BuildSummary = res.Summary`.

9. **Line 146: Return nil** if no OnResult or OnResult succeeded.

---

## 3. Full list of stage names in the implement workflow; which call AgentStage

From `implement.go` lines 38-109, the stages in order:

| Order | Stage name | Calls AgentStage? | Implementation |
|---|---|---|---|
| 1 | `"prepare"` | No | `StagePrepareWorktree()` — clones repo, checks out branch (steps.go:21-31) |
| 2 | `"repo-stages"` | No | `StageRepoStages()` — runs Yaegi custom stages from `.archie/stages/*.go` (steps.go:84-104) |
| 3 | `"baseline"` | No | `StageBaselineGate()` — verifies the gate commands pass at base commit before any agent work (implement.go:16-30) |
| 4 | `"plan"` | **Yes** | `AgentStage{...}` — read-only planner, max 15 steps, posts the plan as a comment on the issue (implement.go:43-73) |
| 5 | `"build"` | **Yes** | `AgentStage{...}` — builder with gate, no ReadOnly, mission includes the plan, OnResult captures `tc.BuildSummary` (implement.go:75-98) |
| 6 | `"commit-push"` | No | `StageCommitPush(...)` — commits all worktree changes and pushes the branch (steps.go:51-58) |
| 7 | `"custom-gate"` | No | `StageYaegiGate()` — evaluates `.archie/gate.go` for project-specific rules (steps.go:111-151) |
| 8 | `"diff-cap"` | No | `StageDiffCap()` — parks if the diff exceeds configured line cap (steps.go:63-76) |
| 9 | `"open-pr"` | No | `StageOpenPR(...)` — opens the pull request with BuildSummary as body (steps.go:170-174) |

Two stages call AgentStage: **"plan"** (read-only, no gate, posts plan comment) and **"build"** (read-write, has gate, captures summary).

---

## 4. Every TaskContext.Forge call in steps.go and implement.go

All invocations of `tc.Forge.*` or `tc.TaskContext.Forge.*`:

**In `implement.go`:**

- **Line 68** — `tc.Forge.Comment(context.Background(), tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, body)` — Post the plan as an issue comment (inside the plan stage's `OnResult`). Uses `context.Background()` rather than the stage's context.

**In `steps.go`:**

- **Line 160** — `tc.Forge.CreatePR(ctx, t.Owner, t.Repo, title, tc.Branch, tc.Repo.BaseBranch(), body)` — Create the pull request (inside `OpenPR()` helper, called by `StageOpenPR`).

**In `workflow.go` (not steps.go or implement.go, but part of the workflow engine that uses `tc.Forge`):**

- **Line 195** — `tc.Forge.SetStateLabel(...)` — Set the state label on the issue when a workflow finishes with an outcome (inside `finish()`).
- **Line 221** — `tc.Forge.SetStateLabel(...)` — Set the "parked" label on the issue (inside `park()`).
- **Line 225** — `tc.Forge.Comment(ctx, t.Owner, t.Repo, t.IssueNumber, body)` — Post the park reason as an issue comment (inside `park()`).

Total distinct forge operations during workflow execution: **Comment** (plan posting, park notification), **CreatePR** (opening PRs), **SetStateLabel** (three call sites: finish outcome, park, and park again for the label).

Note: Forge is also used in `daemon.go` outside the workflow engine — `AcceptInvitations`, `VerifyPush`, `React`, `IssuesWithLabel`, `AssignedIssues`, `PRState`, `RepliesAfter`, `CloseIssue`, `SetStateLabel` (for queue/working labels). But in the workflow stages themselves, only the three operations above.

---

## 5. If agent execution moves to NATS: what stays in archied vs. moves to archie-agent

The central boundary is `tc.Agent.Run(ctx, tc.Dir, req)` at `agent.go:110`. This is the single point where workflow execution crosses from deterministic orchestration into LLM-driven agent execution.

### Stays in archied (all non-agent orchestration)

- **Workflow engine** (`workflow.go`): the `Run()` loop that iterates stages, persists task state after each stage, handles outcomes/parking/finish.
- **All deterministic stages** (steps.go, implement.go):
  - `StagePrepareWorktree` — git clone and branch checkout via `tc.Trees`
  - `StageRepoStages` — Yaegi custom stage discovery and execution
  - `StageBaselineGate` — runs shell gate commands at base commit
  - `StageCommitPush` — git commit and push via `tc.Trees`
  - `StageYaegiGate` — Yaegi gate evaluation
  - `StageDiffCap` — diff line counting
  - `StageOpenPR` — forge PR creation
- **AgentStage Result processing** (`agent.go` lines 110-146) — validation, note appending, token/iteration accumulation, event emission, OnResult callbacks (plan posting to forge, BuildSummary capture).
- **TaskContext construction** (`daemon.go:481-492`) — the daemon builds TaskContext from store, config, trees, forge, etc.
- **All forge interactions** (`tc.Forge.*`) — commenting on issues, setting labels, creating PRs.
- **Store operations** (`tc.Store.*`) — task persistence, transitions, claims.
- **Task queueing and polling** (`daemon.go`): `poll`, `pollNATS`, `pollSQLite`, `checkWaiting`, `reconcilePRs`, `drainSQLite`, `drainNATS`.

### Moves to archie-agent (the LLM execution layer)

- **The actual `tc.Agent.Run(ctx, tc.Dir, req)` call** — the entire agent loop: LLM chat completion, tool execution (read/write files in the worktree, run shell commands, execute gates), budget tracking, stop reason detection.
- **The worktree directory (`tc.Dir`)** and all file I/O within it. The agent's file read/write/edit tools operate on `tc.Dir`. If archie-agent is a separate process, it needs access to the same worktree (either via shared filesystem, or by serializing file operations back to archied, or by archied exposing a filesystem server).
- **Preflight command execution** (lines 86-92 of agent.go) — `tc.Repo.ResolvedPreflight()` commands are baked into the `Request.Preflight` field and executed by the Runner; they would run in archie-agent.
- **Gate execution** (the gate commands in `Request.Gate`) — run by the Runner during the agent loop.

### What the NATS bridge layer must carry

The NATS message would need to convey:

**From archied to archie-agent (the Request):**
- TaskID, Attempt, Stage name, Model reference
- Mission string (already a string — the produced mission text)
- ExtraRules, ReadOnly flag
- Budget (MaxSteps, MaxTokens, WallClock)
- Gate (command list, max failures)
- Preflight (command list)
- Protection (suffixes + globs)
- Notes
- CaptureTools definitions
- **Worktree access** — either the path (if shared filesystem) or the repo coordinates + branch + commit SHA for the agent to clone locally

**From archie-agent to archied (the Result):**
- Version, Status, StopReason
- Summary, Detail
- TokensUsed, Iterations
- AppendedNotes (string list)
- Full tool call records (for CaptureTools)
- File modifications list (if archied needs to track what changed)

### Architectural observation about worktree access

The critical coupling is `tc.Dir` being passed directly to `tc.Agent.Run` (line 110). Currently the agent loop reads and writes files inside that directory. If archie-agent runs as a separate process:
- **Option A (shared filesystem)**: Both archied and archie-agent mount the same worktree directory. ARCHied passes the path, archie-agent writes directly. Simple but requires shared storage.
- **Option B (archied as filesystem proxy)**: Archie-agent sends file read/write/stat requests back to archied over NATS (or a separate channel). Looser coupling but higher latency and complexity.
- **Option C (archied streams the Request, agent clones independently)**: Archie-agent receives the repo/branch/commit via NATS and clones its own worktree copy. This avoids file-routing latency but requires git credentials in the agent process.

### Summary diagram

```
archied (stays)                          NATS bridge                 archie-agent (moves)
───────────────────────                 ──────────                 ──────────────────────
Poll → Enqueue → Claim
Build TaskContext                         
  ↓                                       
workflow.Run()                           
  prepare (git clone)                    
  repo-stages                            
  baseline (gate verify)                 
  plan (AgentStage) ───── Request ────→  ──────→  agent loop (LLM + file I/O)
                      ←──── Result ────           ←── gate/preflight execution
    OnResult: post plan comment           
  build (AgentStage)  ───── Request ────→  ──────→  agent loop (LLM + file I/O)
                      ←──── Result ────           ←── gate/preflight execution
    OnResult: capture BuildSummary        
  commit-push (git)                      
  custom-gate (yaegi)                    
  diff-cap                               
  open-pr (forge CreatePR)               
  finish/outcome                         
```

The workflow engine, stage orchestration, all forge/store interactions, and AgentStage result processing stay in archied. Only the `tc.Agent.Run()` call — the LLM agent loop with its file and command execution — moves to archie-agent.