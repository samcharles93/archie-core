# Control-plane backlog run: how to drive it

This is the committed copy of a real run. Its originals lived in
`/work/apps/archie-core/.pi/control-plane-run/`, which is gitignored, disposable
run state whose live copies may have moved on since. Nothing here is load
bearing at runtime.

The method is not here. The pipeline shape, the concurrency ceilings, the
pi-subagents traps, and the `scripts/` and `templates/` this file names all live
in the user-scope skill `~/.agents/skills/high-throughput-programming/`, and that
skill is normative for method. This copy is normative only for what the run
looked like on this repository; the archie-specific facts themselves live in the
parent `archie-agent-fleet` skill. A run in another repository uses that skill's
`templates/` plus its own filled-in briefs.

## Files

| File                                                                 | What it is                                                                                                                                                                                                                                                                                                                                                                 |
| -------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `00-orchestrator.md`                                                 | The prompt you give pi. It owns the waves, the ceilings and the verification rules.                                                                                                                                                                                                                                                                                        |
| _(the scripts)_                                                      | This run's `wave0.js`, `wave2.js`, `review.js` and `gate-evidence.sh` are **not copied here**: they live on as `templates/recon.js`, `templates/lanes.js`, `templates/review.js` and `scripts/gate-evidence.sh` under `~/.agents/skills/high-throughput-programming/`, which are normative. The snapshot's copies were stale, so they were deleted rather than duplicated. |
| `lanes.json`                                                         | Lane definitions: worktree paths, branches, beads, scoped test commands, briefs. Fill in the model id.                                                                                                                                                                                                                                                                     |
| `prompts/wave1.md`                                                   | fwmp shape-first writer, and the xrux verdict probe.                                                                                                                                                                                                                                                                                                                       |
| `prompts/wave4-merge-train.md`                                       | The serial merge procedure and the landing checks.                                                                                                                                                                                                                                                                                                                         |
| `prompts/wave5-eju6.md`                                              | The end-to-end restart test.                                                                                                                                                                                                                                                                                                                                               |
| `agents/cp-recon.md`, `agents/cp-writer.md`, `agents/cp-reviewer.md` | Project-scope agent definitions, as the run used them from `.pi/agents/`; these committed copies sit beside this file.                                                                                                                                                                                                                                                     |

## Start it

```fish
cd /work/apps/archie-core
pi "$(cat .pi/control-plane-run/00-orchestrator.md)"
```

The `.pi/control-plane-run/` path is the run's own state directory, not this
copy. Interactive is the right mode here: Checkpoint A after Wave 0 is a real
stop and wants you in the loop. `/subagents-fleet` opens the live inspector,
`/subagents-doctor` checks the install.

## Before Wave 2

Create the durable worktrees yourself. Managed `worktree: true` children capture
a patch and then remove the worktree, which a resumed challenge stage cannot
edit, so the lanes use operator-created worktrees plus per-child `cwd`.

```fish
for lane in live recovery steps config
    git -C /work/apps/archie-core worktree add ../cp-$lane -b cp/$lane
end
```

A lane worktree is a **sibling** of the repository, so `.pi/agents/` inside the
repository is not discovered from it. Link it per worktree or the children fail
with `Unknown agent: cp-writer`:

```fish
for lane in live recovery steps config
    mkdir -p /work/apps/cp-$lane/.pi
    ln -sfn /work/apps/archie-core/.pi/agents /work/apps/cp-$lane/.pi/agents
end
```

Then fill `model` in `lanes.json` from what `subagent({ action: "models" })`
resolves, and pass it as `args`.

## Ceilings worth knowing before you tune anything

This install is `pi-subagents` **0.70.0**, matching your clone except for four
doc files. Two 0.69/0.70 features are load bearing here: typed gates
(`gate: { command, output: "json" }` turns a host command's JSON stdout into the
child's `structuredOutput`) and `allowedAgents`, which can stop a reviewer
launching a writer if you want separation of duties enforced rather than
instructed.

- `globalConcurrencyLimit` defaults to **20**, not 64. Set it per top-level call.
- `maxSubagentSpawnsPerRun` defaults to **64** cumulative admissions per run
  tree, never refunded, so retries spend it. Each wave is a separate top-level
  run precisely so each gets its own budget.
- `runs.lanes` caps at 32 lanes, 16 stages per lane, 64 stages total.
- `gate` is rejected on a retained `resume` item, which is why the challenge
  stage runs its tests through `bash` instead.
- A typed gate cannot be combined with `outputSchema` on the same child, so the
  writer stages carry the gate and the reviewers carry the schema.
- Host steps have no per-step `cwd`, which is why `gate-evidence.sh` takes the
  worktree path as its first argument instead of relying on one.

## Why the fleet is not 64 writers

64 concurrent children is comfortable for read-only work and pointless for
writers here. Two limits bite first:

1. **Overlap.** Merge cost scales with shared file overlap, not worker count.
   Seven beads reduce to four genuinely disjoint lanes plus one shared shape
   that has to land alone first. Five workers each adding a field to one struct
   cost eight cleanup commits on this repo in July.
2. **The machine.** 16 cores, 117 Go packages, and `task check` also runs the
   dashboard tests. One full gate saturates the host, so the gate is serialised
   into the merge train and writers run package-scoped tests.

The throughput comes from recon and review: 25 recon children, then a review
pass after every wave at up to 36 children, all read-only, all cheap, all
genuinely parallel. The writers stay at four because that is what the
repository's coupling and the review pass can absorb. The measured literature is
consistent that review, not generation, is where agent-heavy pipelines stall, so
spare concurrency goes into checking rather than into more lanes.
