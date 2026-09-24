# Mission: clear the archie-core control-plane backlog with a bounded high-throughput fleet

> **Where this file's paths point.** This is the committed copy of a live run.
> Paths under `.pi/control-plane-run/` (sections 6, 9, 10) name that run's own
> disposable copies of `wave0.js`, `wave2.js`, `review.js` and
> `gate-evidence.sh`; they are gitignored run state and may lag the skill's
> normative versions in `~/.agents/skills/high-throughput-programming/`. The
> skill is the one to trust. Paths under `/work/apps/archie-core` and
> repo-relative source paths are unchanged.

You are the orchestrator. You do not write production code yourself. You run
waves of subagents through the `subagent` tool, verify their work against git
evidence rather than their reports, and land finished slices on `main`.

Repository: `/work/apps/archie-core`, branch `main`, working tree clean at
start. Gate: `task check`. Tracker: `bd`. Read `CLAUDE.md` and
`docs/architecture/organisation.md` before Wave 1.

---

## 1. Hard rules

1. **One top-level `subagent` call per wave.** Every child launches inside that
   call's `workflowScript`. Never open a second top-level orchestration while a
   wave is running.
2. **Read-only fans out wide. Writers do not.** Recon and review children are
   cheap and parallel. Writer children are bounded by file overlap and by this
   machine (16 cores), not by the spawn budget.
3. **At most one `task check` at a time, ever.** It compiles 117 Go packages,
   runs the full test suite and the Node dashboard tests, and saturates the
   machine. Writer children run package-scoped tests only. The full gate belongs
   to the merge train in Wave 4, which is strictly serial.
4. **A worker's report is not evidence.** Accept a lane only on: the handoff
   manifest path, `git -C <worktree> status --short`, `git log --oneline`, and
   `git diff --stat` against the lane's own base. A bead marked closed with no
   commits means the work did not happen.
5. **No suppression.** Children may not add `//nolint`, `t.Skip`, build tags
   that exclude a failing test, or delete an assertion to make a gate pass. Say
   this in every writer and fix task. A child that suppresses gets its lane
   reverted, not patched.
6. **Formatting is law.** Adopt `task fmt` output verbatim. Never revert a
   linter or formatter diff.
7. **Zero patch stacking.** Two failed fixes on the same logic block means the
   block gets rewritten, not patched a third time. This is a `CLAUDE.md` rule
   and it applies to children.
8. **Do not push.** Commit gate-clean work to `main` in the captain checkout.
   Sam pushes manually and does not always announce it, so never assume a commit
   is local or remote without checking `git log origin/main..main`.
9. **No `.beads/` commits.** Bead state moves through Dolt. Closing a bead is
   not commit-worthy on its own.
10. **Scratch stays out of the tree.** Children write scratch to `/tmp` only.
    `.pi/` is gitignored; nothing else you create may land in a commit.
11. **Separation of duties.** No agent merges, closes or approves its own work.
    Writers write, reviewers review, and you alone run the merge train. You do
    not write production code; if you find yourself editing a lane's files, the
    lane was scoped wrong.
12. **Every wave ends with an adversarial review pass** before the next wave
    starts. Reviewers are read-only, parallel and cheap; a defect caught at the
    end of the wave that produced it costs one lane, and the same defect caught
    three waves later costs the tree.

---

## 2. Ground truth: do not spend agents rediscovering this

This came from the session that just ended. Treat it as fact.

- `docs/prds/runtime-control-plane.md` is Approved and is the spec. Its resource
  surface, YAML workflow definitions, identity-backed attribution and audit
  trail are already built.
- **Restore is not its own command.** A revision carries its value, so restoring
  replays it through the ordinary replace. Do not add a rollback RPC.
- **Dashboard writes attribute to `identity.SystemID`.** One shared bearer
  token, no per-user session. `webAudit` in `internal/webui/api_identities.go`
  is the only source. Named attribution needs a dashboard login first.
- **`internal/webui` must not link the workflow engine.** It decodes definitions
  with the projection types in `internal/domain/workflow/task`.
  `cmd/archie-ui`'s deletion gate fails if anything reaches
  `internal/domain/workflow`.
- Shipped defaults ride `ResourceDescriptor.defaults_json`. The API is fixed at
  four operations, so defaults get no REST route.
- `archie-messaging` fails closed when the State Store is unreachable. The
  database is authoritative for channel settings. File config is not a fallback.
- **The control plane fails closed with no way back.** A stored value that will
  not validate stops `archied` starting. Nothing bypasses the control plane at
  boot. `docs/architecture/safe-change-and-recovery.md` records this posture.
  Resource history is the only in-band remedy and needs the State Store up.
- **`task check` will not catch a field the server stopped sending.** Optional
  chaining made a dead dashboard banner typecheck silently;
  `delivery-snapshot.sh` scanned for a symbol its composition root no longer
  calls. Both fixed in `85f0fd0b`. When a server-side field is deleted, grep the
  dashboard and the snapshot scripts by name. Add this to every review task.
- **Reload re-reads the database layer and can fail.** `boot.runtimeConfig` is
  the one layering call; the SIGHUP callback makes the same call over the
  document it just resolved (`9af013eb`). A new database-owned layer goes in
  `runtimeConfig` and nowhere else.
- **Apply status lives on the State Store contract, not the control plane.**
  `PutApplyStatus`/`ListApplyStatus` are administrative, keyed on
  `(process, kind)`, replaced in place, re-stamped every 30s, stale at 90s, and
  a stale record renders as "Not reporting", never as current. `applystatus.New`
  refuses a process name `Processes()` does not list.
- Five commits are local as of 2026-09-21: `731a9a06`, `1dd0d874`, `d9dd7885`,
  `9accf112`, `d38e27fd`. `archie-core-pskb` is closed. There is no post-commit
  hook; `core.hooksPath` is `.beads/hooks` and those are beads shims.

---

## 3. The backlog

Seven ready beads carry the `control-plane` label. Their real shape is what
decides the lanes, and it is not the same as their titles.

| Bead               | Title shape                                              | Primary surface                                               |
| ------------------ | -------------------------------------------------------- | ------------------------------------------------------------- |
| `archie-core-nwa0` | check a live replacement before switching to it          | live apply path + apply-status surface                        |
| `archie-core-fwmp` | let plugins register workflow step types                 | `workflow.BuiltinStepRegistry()` and all five call sites      |
| `archie-core-8szi` | registered steps covering the deleted `.archie` gate     | `internal/domain/workflow/steps.go`, needs fwmp's answer      |
| `archie-core-6zw0` | offline backup/restore/validate/rollback for `archie.db` | new `cmd/` binary + state-store infrastructure                |
| `archie-core-i3qm` | stop TOML blocking State Store startup                   | `internal/app/archied/control_plane.go`, bootstrap validation |
| `archie-core-xrux` | `ApplyOverlayValues` is dead, confirm the file overlay   | `internal/infrastructure/configuration/loader.go`, `apply.go` |
| `archie-core-eju6` | end-to-end restart test for a database-backed change     | test only, proves the whole loop                              |

Three couplings you already know about, and Wave 0 exists to find the rest:

- **fwmp is the shared shape.** It changes the registry every call site
  hardcodes (`app/agentworker/task_execution.go:319,:335`,
  `app/controlplane/workflow_definitions.go:26,:33`,
  `app/controlplane/client.go:41`) and the validating side and the executing
  side must resolve it identically. 8szi has nothing to register against until
  fwmp lands. These two are never concurrent writers.
- **xrux decides i3qm's blast radius.** xrux is an investigation with two
  opposite outcomes: either the file overlay zeroes omitted map fields, in which
  case `foldOverrides` moves there and it is a live bug, or it does not, in
  which case `apply.go`, `apply_test.go` and `Loader.ApplyOverlay` are deleted.
  Both land in the same package i3qm rewrites. One lane owns both, in that
  order. `archie-core-xrux` is a trap, not a cleanup. Read the bead before
  deleting `apply.go`.
- **eju6 proves the others.** It goes last, against everything merged.

---

## 4. Models: resolve before Wave 0

Run `subagent({ action: "models" })` first and pick exact `provider/id` strings.
Bare ids resolve only when unique. Then print the table you resolved and use it.

| Tier   | Used by                              | Pick                                                                                                                                                    |
| ------ | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Fast   | recon, inventory, grep-shaped probes | cheapest capable, low or medium thinking. Default `ollama/deepseek-v4.1-flash:cloud`.                                                                   |
| Strong | writers, integrator                  | best reasoning model authenticated here, high thinking. Prefer a `deepseek-v4-pro` class id if `models` lists one.                                      |
| Review | reviewers, oracle                    | a **different family** from the writer that produced the diff. Model diversity catches different failure modes; it does not rescue an ambiguous prompt. |

If a tier cannot be resolved, say so and fall back to the session default rather
than inventing an id. Provider 429s return from that model; do not kill a rate
limited child, it keeps its context and recovers.

---

## 5. Concurrency: the numbers that actually bind

Defaults in this install (`pi-subagents` 0.70.0) are not 64.

- `globalConcurrencyLimit` defaults to **20** concurrent children per run. Set
  it explicitly on each top-level call.
- `maxSubagentSpawnsPerRun` defaults to **64** cumulative admissions per run
  tree, never refunded. Retries and fix passes spend it too.
- **Each wave is its own top-level run, so each wave gets its own budget.** This
  is why the mission is split into waves rather than one giant script.
- `runs.lanes` is capped at 32 lanes, 16 stages per lane, 64 stages total.

Per-wave dials:

| Wave           | Shape                                                                    | `globalConcurrencyLimit` | `maxSubagentSpawnsPerRun` |
| -------------- | ------------------------------------------------------------------------ | ------------------------ | ------------------------- |
| 0 recon        | `runs.all`, read-only                                                    | 32                       | 64                        |
| 0R audit       | `review.js`, angles `claims`                                             | 16                       | 24                        |
| 1 shape-first  | one writer plus one probe                                                | 2                        | 8                         |
| 1R review      | `review.js`, full angle set, one target                                  | 12                       | 16                        |
| 2 writer lanes | `runs.lanes`, one worktree each                                          | 4                        | 32                        |
| 2R review      | `review.js`, full angle set, four targets                                | 24                       | 48                        |
| 3 fix pass     | `runs.lanes`, retained writers                                           | 4                        | 24                        |
| 4 merge train  | serial, one `task check` at a time                                       | 1                        | 8                         |
| 4R post-merge  | `review.js` on merged `main`, angles `boundary`, `deadfield`, `contract` | 8                        | 12                        |
| 5 eju6         | one lane, then its own review                                            | 2                        | 12                        |

Do not set a hard `toolBudget` or a tight `usageBudget` on any writer, fix
worker, or reviewer with edit authority: the default budget blocks read and
search tools rather than mutation tools, which leaves a half-written tree. Bound
writers with `timeoutMs` plus `checkpointBeforeDeadlineMs` instead. Hard caps
are fine on explicitly read-only children.

---

## 5a. The constraint is review, not generation

Generation is no longer the bottleneck and has not been for a while. The
measured evidence is consistent: agent-authored changes take far longer to reach
a first review, merge at a lower rate than human-authored ones, and raise more
issues per change; one randomized study found experienced developers 19% slower
with agent tooling while expecting to be faster. Feature-branch throughput rises
while main-branch throughput falls. Adding writer lanes past what review can
absorb does not raise throughput, it grows a queue, and queued work ages badly
because the tree moves underneath it.

Act on that as follows.

- **WIP is capped by review, not by cores.** Four writer lanes exist because
  four is what the review pass and the merge train can absorb per cycle. Do not
  raise it to use spare concurrency.
- **Put the judgement upstream.** Before a writer starts, its task must carry
  intent, acceptance criteria and non-goals, so verification has an external
  reference. "Does this do what it was supposed to do" is checkable; "does this
  look right" is not, and fluent output passes a read-through scan regardless of
  whether it is correct.
- **Spend the effort on the verifier, not the prompt.** A parallel fleet is only
  as good as the check that decides whether its output is right. A better test
  is worth more than a better instruction.
- **Every handoff carries evidence.** Intent, non-goals, files touched with a
  reason each, alternatives rejected, commands actually run with pasted output,
  the test that would fail first, and known limitations. A report that only
  describes its own diff is not evidence.
- **Deterministic gates run before any reviewer looks.** `gate-evidence.sh` runs
  the lane's tests on the host and emits JSON that becomes the child's
  structured output, and it fails the lane on added `//nolint`, `t.Skip`, ignore
  build tags, removed tests or removed assertions. Gate gaming is cheap to
  detect and expensive to miss, so detect it mechanically rather than asking a
  reviewer to notice.
- **Keep batches small.** Generation being cheap is a reason for smaller
  changes, not bigger ones. A lane whose diff passes roughly 400 changed lines
  gets split at a coherent boundary.
- **Route risk, do not review uniformly.** These surfaces get Sam's eyes before
  merge, named explicitly in your report: the fail-closed boot path
  (`boot.runtimeConfig`, bootstrap validation), the State Store wire contract
  and its error sentinels, task-scoped grants and credentials, anything that
  writes or migrates `archie.db`, and the release or CI surface. Everything else
  merges on gate plus reviewer verdict, with a sample called out for him to
  spot-check.
- **Watch the right numbers.** Report per wave: reviewer findings raised versus
  accepted, lanes blocked by gate versus by review, rework passes per lane, and
  wall-clock from lane start to merged. Lines written and beads closed measure
  how fast the queue fills.

---

## 6. Wave 0: recon fanout (read-only, wide)

One top-level call, `async: true`,
`workflowScriptPath: ".pi/control-plane-run/wave0.js"`.

Three probes per bead, plus four cross-cutting probes: 25 children, all
`cp-recon`, all read-only, all returning structured output. The probe text is
composed inside `wave0.js`; do not retype it.

Per bead:

- **spec** — extract the exact PRD clauses the bead must satisfy, with line
  anchors, and the acceptance criteria the gate can actually check.
- **blast** — trace every call site, consumer and test of the symbols the bead
  names. Return package paths and file paths, not prose.
- **tests** — inventory the tests that already cover this surface and name the
  one test that would fail first if the change were wrong.

Cross-cutting:

- **overlap** — for all seven beads, the set of packages and files each one must
  modify, in one table.
- **gate-cost** — time `task build` and
  `go test ./internal/app/controlplane/...` and report wall-clock, so Wave 2
  timeouts are measured rather than guessed.
- **dead-field** — grep `ui/src/` and `scripts/delivery-snapshot.sh` for every
  control-plane field name the server sends today, and flag any the server no
  longer sends. This is the failure class `task check` cannot see.
- **contract-pairs** — find every place the validating side (State Store) and
  the executing side (archie-agent) must agree on the same vocabulary, and name
  the single test that would tie each pair together.

Every probe returns `structuredOutput` matching the schema in `wave0.js`. Long
findings go to a file via `outputMode: "file-only"`; do not pull transcripts
into your context.

**Do not let a recon child draw the conclusion.** A scout that measures
correctly can still land the wrong call, and its write-up reads confident either
way. Take their facts, make the call yourself.

---

## 7. Checkpoint A: lane plan, then stop

Build the conflict matrix from the `overlap` probe and the per-bead `blast`
results. Two beads may be concurrent writers only if their file sets are
disjoint. Directory names are not evidence of disjointness: check the imports.

Then report to Sam, in under 30 lines:

- the matrix, as bead by bead package intersections;
- the proposed lanes, and which beads you are deliberately serializing;
- any bead whose description no longer matches the tree, with the commit that
  moved underneath it;
- the measured gate cost and the timeouts you derived from it;
- anything in Wave 0 that contradicts section 2.

Wait for approval before Wave 1. This is the only mandatory stop.

---

## 8. Wave 1: shape-first, serial

Land the shared shape before anything forks against it. Five workers each adding
a field to one struct produced eight cleanup commits on this repo in July; the
merge cost scales with shared-file overlap, not worker count.

Sub-prompts for both children are in `prompts/wave1.md`.

1. **fwmp** alone: one `cp-writer`, `worktree: true`, red-green, gate
   `go test ./internal/domain/workflow/... ./internal/app/controlplane/... ./internal/app/agentworker/...`.
   Requirement: one contract test asserting the validating registry and the
   executing registry resolve the same step vocabulary. Two sides that each pass
   their own tests and disagree with each other is the exact failure this bead
   exists to prevent.
2. Merge fwmp through the Wave 4 train immediately, before Wave 2 starts. Wave 2
   branches from the merged shape.
3. **xrux investigation** runs concurrently with fwmp, because it is read-only
   until it has a verdict: one `cp-recon` writes a characterisation test in a
   scratch worktree that answers the single question, does `Loader.overlayFile`
   zero omitted fields of a map-valued entry. It returns the verdict and the
   test, and nothing else. The verdict decides the i3qm lane's stages.

---

## 9. Wave 2: bounded writer lanes

One top-level call, `runs.lanes`, `worktree: true` on every writer stage,
`baseRef: "HEAD"` resolved after fwmp merged. Four lanes, concurrency 4.

| Lane       | Beads          | Stages                                          |
| ---------- | -------------- | ----------------------------------------------- |
| `live`     | nwa0           | red, green, self-gate, challenge                |
| `recovery` | 6zw0           | red, green, self-gate, challenge                |
| `steps`    | 8szi           | red, green, self-gate, challenge                |
| `config`   | xrux then i3qm | verdict-apply, red, green, self-gate, challenge |

Stage contract, enforced by `wave2.js` and the `cp-writer` agent definition:

- **red**: write the failing table-driven test first, run it, and paste the
  failure. Verify the failure comes from an assertion, not a compile error. A
  test that cannot fail for an interesting reason is noise in the gate.
- **green**: minimal implementation that satisfies the failing test.
- **self-gate**: a typed `gate` pointed at
  `.pi/control-plane-run/gate-evidence.sh`, which runs the lane's package-scoped
  test command on the host and returns JSON that becomes the child's
  `structuredOutput`: branch, sha, changed files, uncommitted files, test exit
  and tail, plus the gate-gaming counters. Never `task check`. The evidence
  comes from the host, so a child cannot report a pass it did not get.
- **challenge**: `resume: "previous"` against the same writer, with the
  adversarial prompt built into `wave2.js`'s challenge stage. The writer still
  holds the context; a fresh child pays for that context twice. Only resume a
  child `children.list` reports as `resumable`; otherwise start a same-role
  fallback and label it a fallback.

Each lane's writer also returns the evidence handoff in `wave2.js`: intent,
non-goals, files with reasons, alternatives rejected, commands run with pasted
output, the test that would fail first, and known limitations. A lane whose diff
passes roughly 400 changed lines stops at a coherent boundary and reports what
remains rather than growing further.

Every writer opens by printing `pwd`, `git branch --show-current` and
`git status --short`, and repeats that evidence in its final report. A worker
that finishes in the wrong checkout looks exactly like a worker that did
nothing.

A failed stage blocks only its lane. Do not repair a lane by hand and do not
fall back to running the work yourself, `pi -ne`, or an external CLI: a child
launch or tooling failure is a lane infrastructure blocker, so report the exact
failure, the run status, and the worktree state, then retry through the same
path.

---

## 10. Review after every wave

`.pi/control-plane-run/review.js`, read-only `cp-reviewer` children on a
different model family from the producer, one child per angle per target. The
angles live in that script: `bead`, `invariant`, `contract`, `deadfield`,
`boundary`, `operator`, `tests`, `gaming`, `intent`, plus `claims` for auditing
recon findings.

| After  | Targets                       | Angles                              |
| ------ | ----------------------------- | ----------------------------------- |
| Wave 0 | the recon findings themselves | `claims`                            |
| Wave 1 | `cp/shape`                    | full set                            |
| Wave 2 | the four lane branches        | full set                            |
| Wave 4 | merged `main`                 | `boundary`, `deadfield`, `contract` |
| Wave 5 | the eju6 branch               | `tests`, `operator`, `intent`       |

The Wave 0 audit is the cheapest of these and catches the most: a recon child
that measures correctly can still carry a wrong conclusion into your lane plan,
and its write-up reads confident either way. Feed the audit the claims your lane
plan is about to rest on, and check the load-bearing ones before Checkpoint A,
not after.

Reviewers label findings P0/P1/P2 by evidence, not by severity language, carry
`file:line` with a quoted proof, and end with one of `Merge verdict: BLOCK`,
`Merge verdict: OK`, `Merge verdict: OK with notes`. Prose is never parsed for
control flow: you decide the lane's fate from the findings.

Fixes go back to that lane's retained writer, batched into one pass, not one
child per finding. Resume only a writer `children.list` reports as `resumable`;
otherwise start a same-role fallback and label it as a fallback. A second failed
fix on the same logic block means the lane is reverted and requeued, not patched
again.

Count the pass as complete only when every angle returned. A missing angle is an
unreviewed axis, not a clean one.

---

## 11. Wave 4: merge train, strictly serial

The procedure, with the exact commands, is in `prompts/wave4-merge-train.md`.
One branch at a time, in dependency order, in the captain checkout at
`/work/apps/archie-core`. Per branch:

1. `git merge-base --is-ancestor main <branch>`. A stale branch computed its
   deletions against code that has since changed; rebase or re-derive, never
   merge it as is.
2. Apply the lane's handoff patch.
3. `task check`, once, alone. Nothing else runs while it does.
4. On failure: one fix pass through the lane's writer with the real output. On a
   second failure, revert the lane and requeue it; do not stack a third patch.
5. Commit with a conventional scope (`feat(controlplane): ...`,
   `fix(config): ...`). Include generated assets under `ui/dist/` in the same
   commit; they are LAW, never reverted or ignored.
6. `bd close <id>`. Do not stage `.beads/`.
7. Verify it landed: `git log --oneline -1`, then `git branch --no-merged main`.
   Done has three states: no commits, committed and unmerged, merged. Only the
   third counts.

Your own edits get the same gate as the children's. A coordinator `sed` broke
the build on this repo once and nobody else reviews your diffs.

---

## 12. Wave 5: eju6

Sub-prompt in `prompts/wave5-eju6.md`. Last, against merged `main`. One lane: an
end-to-end test that drives a change through the API, restarts a process, and
asserts the change survived. Not a unit test, and not a rewrite of
`internal/app/controlplane/controlplane_test.go`, which covers seeding and
catalogue only. This is the proof the whole feature works across a restart,
which nothing currently provides.

If it fails, that is a finding about the feature, not about the test. Report it
and stop rather than weakening the assertion.

---

## 13. Final report

- Beads closed, with the commit that closed each one.
- Beads deliberately not attempted, and why.
- `task check` result on final `main`, quoted, not summarised.
- Every branch still unmerged, and whether it holds work.
- Commits now local and unpushed (`git log --oneline origin/main..main`), stated
  as fact without pushing.
- Anything you found that contradicts `docs/prds/runtime-control-plane.md` or
  section 2 of this prompt, filed as a new bead.
- New beads for identified debt or follow-ups, filed via `bd`.
- The wave metrics: per wave, reviewer findings raised versus accepted, lanes
  blocked by the gate versus by review, rework passes per lane, and wall-clock
  from lane start to merged. Say which stage was the constraint in practice and
  whether it matched the prediction that review would be.
- The risk-routed surfaces that changed and need Sam's eyes: the fail-closed
  boot path, the State Store wire contract and error sentinels, grants and
  credentials, anything touching `archie.db`, and the release or CI surface.
