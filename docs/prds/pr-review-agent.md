# PR review agent

**Status:** Approved
**Authority:** `docs/architecture/agent-system.md`; supersedes the reviewer
executor and blocking rule in `docs/architecture/adversarial-review.md`, and
the designs in `docs/prds/operator-pr-review.md` and
`docs/prds/review-calibration.md`.
**Compounds with:** `docs/prds/execution-tree-state-machine.md` (every agent
call is a StepExecution), `docs/prds/external-agent-harness.md` (any agent
step may run on a harness), `docs/prds/inline-review.md` (posting),
`docs/prds/pr-review-remediation.md` (consumes the findings).

## Decision

Archie ships a pull request reviewer as its first end-to-end product. It
reviews any pull request on a watched repository, a PR an operator names, and
archie's own PRs before they open.

The reviewer is a fixed pipeline, not a single agent. The review strategy is
generated per PR: three lens agents propose review dimensions from the PR's
content, one reviewer definition runs once per dimension, and later agents
verify, challenge and combine the findings. Everything that can be computed
is code: diff parsing, clustering, blast radius, evidence extraction,
scoring, deduplication, line mapping and the review event.

The pipeline is **recall-first**. It posts every evidenced finding above the
confidence threshold, up to the comment cap. A precision gate exists as an
operator dial and is off by default.

Acceptance is measured, not asserted: see Verification.

## Pipeline

Each numbered phase is a stage of the `pr-review` workflow. Each agent call is
an `agent` StepExecution; fan-out phases record one child per call.

1. **Intake.** Code reads the PR metadata and diff statistics: type,
   complexity, languages, areas touched, risk signals. Depth comes from lines
   changed: under 100 is `quick` (at most 3 dimensions), under 500 is
   `standard` (6), otherwise `deep` (12). An operator may set depth
   explicitly.
2. **Anatomy.** Code parses the diff into files and hunks, clusters related
   files, and computes the blast radius: unchanged files that import or are
   imported by changed files. One agent writes the PR narrative, risk
   surfaces, changes that do not fit the narrative, and gaps between the
   description and the diff.
3. **Dimension selection.** Three lens agents run in parallel over the intake,
   anatomy and diff, each able to read the repository:
   - **behaviour:** where the old and new code diverge for any input; API
     contracts, concurrency and state, security, error handling, data flow;
   - **mechanics:** whether the code runs at the language and framework
     level; types, signatures and every caller, decorators and middleware,
     framework contracts, imports;
   - **fit:** patterns, complexity, abstraction, test adequacy, documentation
     that no longer matches, dependencies, migration completeness.

   Each lens returns review dimensions: a name, a reviewer prompt written for
   this PR, target files, context files and a priority. Code merges them,
   drops duplicates and keeps the highest-priority dimensions up to the
   depth's cap.
4. **Review.** One reviewer definition runs once per dimension, in parallel,
   at most 8 at a time. Each reads its target and context files and may
   follow references. Each finding carries file, line range, severity
   (`critical`, `important`, `suggestion`, `nitpick`), title, body, an
   optional suggested fix, quoted evidence, confidence and tags.
5. **Verification layer**, over the review findings:
   - code extracts an evidence package per finding: the code at the cited
     lines and snippets from its callers;
   - an evidence verifier checks high-priority findings against their
     packages;
   - an adversary confirms or challenges each finding, looks for issues every
     reviewer missed, and is more sceptical when intake scores the PR as
     likely machine-written;
   - a compound agent looks, within each cluster of confirmed findings, for
     defects that only exist in combination.
6. **Coverage and consistency**, in parallel:
   - a coverage gate checks every cluster and high-exposure blast-radius file
     was reviewed, and runs gap reviewers for what was not, at most 2 rounds;
   - consistency verification lists every cross-location obligation the
     changed code creates (what must be true elsewhere for this line to be
     correct), then verifies each by reading both ends. A broken obligation
     becomes a finding.
7. **Synthesis**, code only:
   - score = severity weight (1.0, 0.7, 0.3, 0.1) × confidence × multipliers:
     compound 1.5, adversary-confirmed 1.3, adversary-challenged 0.5, likely
     machine-written PR 1.2, blast radius over 10 files 1.2;
   - drop findings under the severity's confidence floor: critical 0.2,
     important 0.3, suggestion 0.4, nitpick 0.4;
   - merge exact duplicates (file, overlapping lines, category) in code, and
     near-duplicates through a small classification call that keeps both
     when unsure;
   - map lines to the diff's coordinates, sort by score, cap at 25 inline
     comments.
8. **Merge gate.** One classification call per finding decides blocking or
   advisory: blocking only for broken builds, security, data loss, contract
   breaks and regressions. A failed call means advisory.
9. **Output.** One call per comment tightens its wording, keeping the
   original on failure. The review is posted as one forge review with inline
   comments. Any blocking finding requests changes; otherwise the review is a
   comment. Archie never posts an approval.

## Precision dial

With `review.precision_gate = true`, a post-worthiness pass runs after phase 4
and before the verification layer. It keeps every concrete, evidenced defect
and drops nitpicks, style and unverifiable claims, keeping a finding when
unsure. It trades recall for precision and cuts verification cost by
filtering early.

## Operator approval

With `review.approve_before_post = true`, synthesis ends in
`waiting_human`. The operator chooses which findings to post, rejects the
review, or asks for a re-review with instructions. A re-review reruns phases
3 to 8 with the instructions added to the lens and reviewer prompts, at most
2 times. A review with no findings posts nothing and does not wait.

## Isolation

Every agent in the pipeline reads a `.git`-free snapshot of the PR head, with
read-only tools rooted at it, and never the implementer's worktree or
transcript. No pipeline agent holds a forge credential; posting is code.

## Budget

A run has a cost cap and a wall-clock cap, split into per-phase shares. A
phase whose share is spent is skipped and recorded on its StepExecution. The
posted review names every skipped phase, so an exhausted run never reads as a
clean one. A run whose review stage fails posts nothing and
fails.

## Triggers

- **Watched repositories:** a PR opened or updated on a repository with
  `review.enabled` starts a run. An update cancels the in-flight run for the
  same PR.
- **Operator:** a PR URL from the dashboard or chat starts a run on any
  repository the identity can read.
- **Archie's own PRs:** the implement workflow runs the pipeline before
  opening its PR. A blocking finding the adversary did not challenge parks
  the task with the findings. A pipeline that fails parks the task. Advisory
  findings are posted after the PR opens.

## Organisations

The reviewer is a workflow, so `docs/prds/orgs-and-access.md` applies to it
unchanged: it runs as the workflow's identity under the run credential, reads
only repositories that identity may read, and posts through the forge RPC
under that credential. A watched repository belongs to a workspace, and its
PR events start reviews through that workspace's bindings. Findings,
StepExecutions and the posted review carry the run's org and workspace.

## Models

Each agent phase names a model role. Lens, reviewer, verification,
adversary, compound and consistency agents use the review role. Intake,
coverage, dedup, merge gate and polish calls use the classification role.
Either role may run on the built-in runner or a harness.

## Out of scope

- SARIF and standalone report formats. The findings are StepExecution outputs
  and the forge review.
- Reviewing a diff without a forge PR.
- Self-consistency reruns.

## Plan

1. Deterministic core: diff parsing, clustering, blast radius, evidence
   extraction, scoring, dedup, line mapping, review event. Table-driven tests.
2. The benchmark harness (Verification), run against the deterministic core
   with fixture findings.
3. `pr-review` workflow: intake, anatomy, lenses, reviewers, synthesis,
   output, with the execution tree recording each call.
4. Verification layer, coverage loop and consistency verification.
5. Merge gate, polish, budget shares and skipped-phase reporting.
6. Benchmark acceptance run.
7. Triggers: operator, watched repositories, archie's own PRs replacing the
   single-reviewer stage.
8. Precision dial and operator approval.

## Verification

- **Benchmark acceptance.** On the 38 runnable PRs of the Martian
  Code-Review-Bench offline set, one blind run per PR at `deep` with the
  review and classification roles on GLM-5.2, and an independent judge
  (`anthropic/claude-sonnet-4.6`) matching posted comments to golden
  comments: golden micro-recall is at least 0.70. Golden-only precision is
  reported alongside it. The run is reproducible from a script in the
  repository.
- Scoring, thresholds, dedup and line mapping give identical output for
  identical findings.
- A run whose phase share is exhausted posts a review naming the skipped
  phase.
- An update to a PR under review cancels the running review and its child
  steps.
- No pipeline agent can write to the snapshot, and none can reach a forge
  credential.
- Archie's own PR with an unchallenged blocking finding is parked, not
  opened.
- Archie never submits an approving review.
