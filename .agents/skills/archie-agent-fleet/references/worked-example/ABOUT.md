# Worked example: archie-core control plane, September 2026

A real run, frozen partway through, kept verbatim so the shapes can be read
rather than re-derived. It is repository-specific on purpose: the value is in
seeing how much project knowledge a good brief carries.

**Its scripts are the snapshot's, not the current ones.** This directory was
captured mid-run, so the scripts in it predate fixes the skill's own lessons
describe — the typed-gate boolean trap, the post-fix re-gate and re-review, the
P2 handling, the build and lint checks in the gate.
`~/.agents/skills/high-throughput-programming/scripts/` and
`~/.agents/skills/high-throughput-programming/templates/` are normative; if this
directory disagrees with them, they win. Read the briefs, the lane plan and the
merge-train procedure here. Do not copy the scripts.

The method this run followed lives in the user-scope skill
`~/.agents/skills/high-throughput-programming/`; it is normative for method. This
repository's `archie-agent-fleet` skill is normative for facts about archie-core.
A run in another repository uses that skill's `templates/` plus its own filled-in
briefs — none of the archie specifics here transfer.

Seven tracker items, one shared shape, four lanes, a review pass after every
wave, a serial merge train.

| File | Note |
|------|------|
| `00-orchestrator.md` | The filled orchestrator prompt. Section 2 is the project's ground truth, section 5a the review-is-the-constraint doctrine, section 10 the review cadence. |
| `lanes.json` | Lane briefs carrying intent, traps and `file:line` premises. Read one to see the level of detail a brief needs. |
| `prompts/wave1.md` | The shared shape, plus an investigation whose verdict gated another lane. |
| `prompts/wave4-merge-train.md` | The serial merge procedure with the project's own hazards in it. |
| `agents/` | The three agent definitions with project invariants inlined. |

No scripts are copied here. This run's `wave0.js`, `wave2.js`, `review.js` and
`gate-evidence.sh` became `templates/recon.js`, `templates/lanes.js`,
`templates/review.js` and `scripts/gate-evidence.sh` in the user-scope skill;
those are normative, and this directory's stale copies were deleted so there is
exactly one of each to keep current.

What this run's Wave 0 actually caught, which is the argument for recon before
fanout: three of the seven items had premises that no longer matched the tree,
including one whose target file did not exist under the name the item used, one
whose anchor had moved commits earlier, and one whose scope could not reach the
clause it cited. All three would have produced confidently wrong lanes.
