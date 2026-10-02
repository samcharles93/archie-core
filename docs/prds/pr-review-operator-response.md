# PR review: the operator's answer to the approval gate

**Status:** Draft
**Authority:** `docs/prds/pr-review-agent.md` ("Operator approval") for the gate, and `docs/prds/state-store-contract.md` (rev. 2e) for every task field that crosses the State Store.
**Compounds with:** `docs/prds/execution-tree-state-machine.md` (a run cannot resume partway through its stage list).

## Decision 1 — where the operator answers

The dashboard's `POST /api/tasks/{id}/action` (`internal/webui/api_tasks.go`, `handleTaskAction`) is the authoritative surface: the only one that resolves the actor from a credential and can carry a payload. Chat is a shortcut, not a second authority — `/approve` (`internal/gateway/gateway.go`, `Router.handleApprove`) answers the post decision with every offered finding selected, and gains no instruction or selection syntax.

`taskstate.Action` gains `rereview`, and `taskstate.Actions` returns `approve`, `reject` and `rereview` for `WaitingHuman`. `approve` posts the review, `reject` discards it through the existing terminal decline, `rereview` reruns the review phases with instructions. The action body carries `instructions`, required by `rereview`, and `findings`, the finding keys `approve` posts; an absent selection means all of them.

The path is `handleTaskAction` → `applyOperatorTaskAction` → `Chat.Contract.ApplyOperatorTaskAction` → the daemon's `LocalChatAdapter` → `taskactions.Client` (NATS `archie.gateway.task-action`) → `taskactions.Service.Apply` → one guarded store write that records the decision and requeues. Chat's `/approve` path (`StoreTaskController.Approve` → `chatTaskControllerAdapter.ApproveChatTask`) must reach that same write through the same function, so the two surfaces cannot record different decisions for one operator intent.

A requeue resumes the workflow the waiting state names, never a name the handler hardcodes. `taskactions.Service.apply` and `chatTaskControllerAdapter.ApproveChatTask` requeue with an empty workflow, which `Requeue` reads as "keep the task's", so neither surface hardcodes a workflow name: an approval whose wait recorded `pr-review` resumes `pr-review`. The feasibility handoff therefore has to name `implement` on the task before it enters the wait, which is the invariant the handler relies on — the workflow a task carries is the workflow it runs next. `Requeue` treats an empty workflow as "keep the task's".

### The review the operator answers

The gate writes the review it is holding onto the task before it ends the run: one JSON column, `review_gate`, a sibling of `review_payload`, holding the scored findings, the head SHA, the pull request's identity and the workflow the wait resumes. The run's existing `Store.Update` persists it. The same column carries the answer back: the response path's guarded write fills in the outcome, the selection and the instructions, and clears the offer for a re-review.

The operator reads the offer through the existing task read — `taskView.Task` on `GET /api/tasks` and `GET /api/tasks/{id}/debug` — so no new read endpoint is needed.

This record is load-bearing because the run that computed the findings ends at the gate and the engine cannot resume it partway. Without it, "post the selected findings" cannot mean the findings the operator saw: the resumed run recomputes them with agent calls. An `approve` resume therefore posts the recorded review filtered by the selection, and the review the resumed phases recompute never reaches the output.

## Decision 2 — the cap

The cap is a column: `rereview_rounds` on `tasks`, `bigint NOT NULL DEFAULT 0`, read by the response path and incremented in the same guarded write that requeues a re-review. A re-review at the cap is refused with the conflict sentinel the dashboard answers 409 with, and nothing is requeued. Two is a constant in the domain, not a config knob: the PRD fixes it.

`Task.Attempt` cannot carry the count, because it counts every requeue. The two alternatives are worse.

- The execution tree measures passes through the gate, not operator requests. A crash recovery (`RecoverStale`) or an operator retry re-runs the pipeline to the gate and would consume the cap; and a stage has no step read (`task.Store` carries none), so this route needs a new interface method anyway.
- `Task.Notes` is the agent memory ledger, not scratch. `AgentStage.buildRequest` passes it into every generic agent request, `agentexec` renders it as a prompt section, and `persistAppendedNotes` appends to it. A counter parsed out of a field agents write to is the encoded-state defect this repository rejects, and instructions parked there would reach every unrelated workflow's agents.

`RemediationRounds` is the precedent: one writer, one reader, one meaning, kept separate from `RetryCount` so unrelated retries cannot draw a budget down.

The write crosses the State Store. A goose migration adds the column; `internal/infrastructure/postgres/queries/tasks.sql` and its sqlc output carry it; `internal/infrastructure/postgres/store.go` maps it; `proto/state/v1/state.proto` carries it as a new `Task` field with its regenerated contracts and `staterpc/values.go` mapping both ways; `internal/domain/workflow/task/task.go` declares it; `internal/infrastructure/taskactions/store.go` adds it to the `taskactions.Task` projection the response path reads. The write itself is a guarded requeue method beside `RetryTask`, which increments a counter, and `BeginRemediation`, which records a payload in the same transaction: one transition-table guard, one increment and one requeue in one write. It must join every column-listing query, which `docs/development/state-store.md` names.

Tests that prove it: the guarded write increments for a re-review and for no other requeue; a re-review at the cap is refused and leaves the task waiting; and the field survives the wire, which `TestTaskProtoCarriesEveryDomainField` asserts for every field of the domain task.

## Decision 3 — how the instructions reach the stages

The recorded instructions enter the pipeline at phase 3 and phase 4 only, which is what the PRD asks for: a labelled operator-instructions block appended to `runLens`'s mission in `stagePRLenses` and to `runReviewer`'s mission.

They cannot ride `AgentStage.buildRequest`. Every pr-review stage builds its `agentexec.Request` in `runPRReviewAgentRecorded`, which sets no `Request.Notes`, so nothing the generic path threads reaches these calls. The pipeline restores the instructions from the task record at the same point it restores the review, and the two mission builders read them there.

## The gate is not reachable from archie's own PRs

`StagePRReviewAndOpenPR` splices `prReviewDecisionStages()`, the list `PRReview()` uses, so the gate is reachable from the implement workflow too. It must not be. The change is one entry moving: `stagePROperatorApproval()` leaves the shared list for `PRReview()`'s own stage list, immediately before the merge gate, and the own-PR splice keeps taking the shared list unchanged. This is a prerequisite for the response channel and a change of its own.

Three reasons, the second deciding it alone.

- The gate's question is which findings to post, and on archie's own PRs there is no pull request to post to at the point the splice reaches the gate. That trigger's human decision point is its park on an unchallenged blocking finding.
- Approving a wait inside the implement workflow re-enters that workflow at its first stage against a worktree that already carries the change. A builder that then reports no changes sets `BuildNoChanges`, `StageCommitPush` calls `closeNoChangesIssue`, and the issue is closed with no pull request ever opened. Nothing the gate decides is worth a route to silently closing an issue.
- Short-circuiting instead would put a conditional no-op in every stage whose work a wait had completed, which changes the resume semantics of the whole pipeline rather than this gate.

## What a full restart gives, and what it costs

A requeue re-runs the workflow's stage list from its first stage; the engine has no partway resume. That satisfies the PRD's "reruns phases 3 to 8" for free, and it is acceptable for the standalone pipeline precisely because that pipeline has no stage that writes and then closes. The costs:

- Phases 1 and 2 recompute. Both are code except two agent calls — the machine-written score and the anatomy narrative — so those re-run, and a changed score can change whether the hallucination-check dimension is added.
- The pull request's snapshot is checked out again.
- The run's cost and wall-clock budgets are per attempt, so a re-review starts with a fresh allowance, which the cap bounds.

## Verification

- A gate wait on the standalone `pr-review` workflow answered `approve` posts exactly the findings the operator selected, and no finding the resumed phases produced.
- A re-review at the cap is refused, the task stays waiting, and the count survives a store round trip.
- A re-review's instructions reach the lens and reviewer missions; a first run's do not.
- The gate is unreachable from the implement workflow's stage list.
