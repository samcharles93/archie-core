import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { delivered, type DeliveredCounts } from "../src/lib/delivered.ts";

// A workflow now ends a run without a pull request (`completed`) as often as
// it merges one: a no-change build and a triage that needed no code both
// finish with nothing to merge. A surface that counts `merged` alone therefore
// renders zero for those runs -- which is what the two surfaces below did,
// beside three siblings that already counted both.
test("a run that ended completed is delivered like a merge", () => {
  const cases: [DeliveredCounts | undefined, number][] = [
    [undefined, 0],
    [{}, 0],
    [{ merged: 2 }, 2],
    [{ completed: 3 }, 3],
    [{ merged: 2, completed: 0 }, 2],
    [{ merged: 0, completed: 4 }, 4],
    [{ merged: 2, completed: 3 }, 5],
  ];
  for (const [counts, want] of cases) {
    assert.equal(delivered(counts), want, JSON.stringify(counts));
  }
});

// The tiles count a status tally rather than a stats row. `pr_open` is work in
// review, not delivered, so it stays out of this count and each tile adds it
// where it needs it.
test("a status tally is counted the same way", () => {
  const tally: Record<string, number> = { merged: 1, completed: 2, pr_open: 5, running: 3 };
  assert.equal(delivered(tally), 3);
});

// Every surface that reports delivered work reads the shared count. This is
// the guard: a surface that reads `merged` alone renders a finished no-change
// run as zero, and the two that did are asserted by expression below.
const deliveredSurfaces = [
  "../src/dashboard/GatePulseCard.vue",
  "../src/dashboard/ThroughputTiles.vue",
  "../src/tasks/TasksSummary.vue",
  "../src/tasks/NewTaskForm.vue",
  "../src/workflows/WorkflowsPage.vue",
];

test("every surface that reports delivered work counts completed runs", async () => {
  for (const path of deliveredSurfaces) {
    const source = await readFile(new URL(path, import.meta.url), "utf8");
    assert.match(source, /from "@\/lib\/delivered"/, path);
    assert.doesNotMatch(
      source,
      /\.merged\s*(\|\||\?\?)\s*0/,
      `${path} counts merged alone, so a run that ended completed reads as zero`,
    );
  }
});

// The two read sites the defect named. Asserted as the expressions that render
// the number, not the file's contents: the bar and the "N of M" text are what
// an operator reads when a workflow shows no deliverable work.
test("the per-workflow bar and count show delivered runs", async () => {
  const page = flat(await readFile(new URL("../src/workflows/WorkflowsPage.vue", import.meta.url), "utf8"));
  assert.match(page, /rate\(delivered\(row\), row\.runs\)/, "the bar's share is the delivered share");
  assert.match(page, /\{\{ delivered\(row\) \}\} of \{\{ row\.runs \|\| 0 \}\}/, "the count is delivered of runs");
});

test("the workflow picker's percent is delivered, not merged", async () => {
  const form = flat(await readFile(new URL("../src/tasks/NewTaskForm.vue", import.meta.url), "utf8"));
  assert.match(form, /delivered\(statFor\(d\.id\)\) \/ \(statFor\(d\.id\)\?\.runs \|\| 1\)/, "the percent counts delivered runs");
  assert.doesNotMatch(form, /% merged/, "a run that ends completed is not merged");
});

/** flat collapses whitespace so a multi-line template expression still matches. */
function flat(source: string): string {
  return source.replace(/\s+/g, " ");
}
