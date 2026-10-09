import assert from "node:assert/strict";
import test from "node:test";

import { delivered, type DeliveredCounts } from "../src/lib/delivered.ts";

test("a run that ended completed is delivered like a merge", () => {
  const tally: Record<string, number> = { merged: 1, completed: 2, pr_open: 5, running: 3 };
  const cases: [DeliveredCounts | undefined, number][] = [
    [undefined, 0],
    [{}, 0],
    [{ merged: 2 }, 2],
    [{ completed: 3 }, 3],
    [{ merged: 2, completed: 0 }, 2],
    [{ merged: 0, completed: 4 }, 4],
    [{ merged: 2, completed: 3 }, 5],
    [tally, 3],
  ];
  for (const [counts, want] of cases) {
    assert.equal(delivered(counts), want, JSON.stringify(counts));
  }
});
