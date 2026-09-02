# Dynamic workflow triage

Epic: archie-core-enfj. Scope: give `workflow.Route()` a real, content-aware
fallback instead of unconditionally defaulting every unlabeled task to the
heaviest workflow.

## Problem

`Route()` (`internal/domain/workflow/workflow.go:254`) has exactly two real
signals today: an explicit `t.Workflow`, and label matches via
`workflowForLabels` (`routing.go`, `bug`→tdd, `feature`→feasibility,
bootstrap→bootstrap). Anything else — which is every chat-spawned task,
since `task_spawn` rarely sets labels — falls straight through to
`reg["implement"]`, the full `prepare → baseline → plan → build →
commit-push → review → open-pr` pipeline, regardless of what the task
actually asks for.

`task_spawn`'s own tool schema overpromises: *"workflow: … Omit to let the
daemon route it"* (`internal/gateway/task_tools.go:240`) implies real
routing exists. It doesn't — "route" currently means "check labels, then
give up and run the heaviest workflow anyway."

Confirmed in production (task 6, 2026-09-02): a chat-spawned task whose
entire content was "this is just a test, close it when you receive it" ran
the full `implement` pipeline end to end — 669,421 tokens, ~7 minutes —
when nothing about the request needed a worktree-driven build at all. The
`plan` stage's own LLM output *correctly* concluded no code change was
needed, but that conclusion arrives two expensive stages (`baseline`,
`plan`) too late to save anything.

## Decision

Add a **`triage` workflow**: cheap, classifies once, then either closes the
task immediately or hands off to the workflow the task actually needs.
`Route()`'s final fallback (currently `reg["implement"]`) becomes
`reg["triage"]` when a `triage` entry is registered, else the existing
`implement` fallback — a two-line change, fully backward compatible when
`triage` isn't wired up.

Triage does **not** replace label-based routing. A labeled task
(`bug`/`feature`/bootstrap) already has a free, reliable signal and goes
straight to its known workflow exactly as today — spending a classification
call there would be pure waste. Triage only fires in the gap that currently
defaults to `implement` blindly: no explicit workflow, no label match.

### Triage workflow shape

```
StagePrepareWorktree()   -- still needed: AgentStage mounts tc.Dir into the
                             agent container: internal/domain/workflow/agent.go:74
                             passes tc.Dir straight through. No cheaper
                             primitive exists in this codebase for a
                             directory-less classification call.
AgentStage "classify"    -- ReadOnly, ONE cheap agent turn, no exploration
                             tools required beyond what ReadOnly already
                             grants. Mirrors feasibility.go's decideCaptureTools
                             pattern: a structured capture tool, called
                             exactly once, MaxCalls: 1.
```

Mission: read the task's title/body/labels (already in `taskPromptBlock`)
and decide, via a captured `decide` tool call:

```json
{"needs_code_change": bool, "workflow": "implement" | "tdd" | "feasibility", "reasons": "..."}
```

`needs_code_change: false` → close path: reuse `closeNoChangesIssue`
(`steps.go:67`) directly — issue closed if forge-backed, task marked
`StatusMerged`/`"completed -- no changes required"` otherwise. No baseline,
no plan, no build, no commit, no PR. This is the actual fix for the
production incident: a no-op request now costs one classification call
(low thousands of tokens) instead of 669,421.

`needs_code_change: true` → sets `tc.Task.Workflow` to the classifier's
chosen workflow (`implement`/`tdd`/`feasibility`, defaulting to
`implement` if the field is missing or unrecognized) and an Outcome that
requeues the task under it, the same requeue mechanism
`chatTaskControllerAdapter.ApproveChatTask` already uses
(`main.go:529`, `requeue(ctx, taskID, fromStatus, workflow)`). The task then
re-enters `Route()` on its next claim with `t.Workflow` now set, and takes
the normal path for that workflow from there — triage never runs its
classification twice for the same task.

### Non-goals (v1)

- Full bug/feature intent classification quality — the classifier only
  needs to be *directionally* right; a wrong `tdd` vs `implement` call
  still produces a working PR, just via a slightly less-tailored pipeline.
  Tightening this is a follow-up, not a blocker.
- Any change to label-based routing, `Route()`'s explicit-workflow branch,
  or `Bootstrap()`/`Feasibility()`/`TDD()` themselves.
- Triage for forge-sourced (real GitHub issue) tasks with no labels —
  in scope structurally (same fallback branch), but the dominant case this
  fixes is chat-spawned tasks, which essentially never carry labels today.

## Testing

- `workflow_test.go`: `Route()` picks `reg["triage"]` over `reg["implement"]`
  for an unlabeled task when both are registered; falls back to
  `reg["implement"]` unchanged when `triage` isn't registered (regression
  guard for every existing `Route()` test).
- New `triage_test.go`: classify → `needs_code_change: false` closes
  without ever calling `tc.Trees.Prepare` a second time or touching
  `tc.Agent.Run` again; classify → `needs_code_change: true` sets
  `tc.Task.Workflow` and produces a requeue Outcome, using a fake
  `agentRunnerFunc` returning a captured decision, mirroring
  `feasibility_test.go`'s existing `decideCaptureTools` test style.
- Integration-shaped test reproducing the production scenario: a
  chat-spawned task with no labels, title "this is just a test, close it
  when you receive it" — asserts the task closes without ever reaching a
  `baseline`/`plan`/`build` stage.
