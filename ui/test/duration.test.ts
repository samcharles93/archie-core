import assert from "node:assert/strict";
import test from "node:test";

import { duration } from "../src/tasks/timeline-event.ts";

test("stage durations read in seconds, never milliseconds", () => {
  for (const [ms, want] of [
    [0, "under 1 s"],
    [999, "under 1 s"],
    [1200, "1.2 s"],
    [41_000, "41 s"],
    [2_501_000, "41 min 41 s"],
    [-1, ""],
  ] as const) {
    assert.equal(duration(ms), want, `duration(${ms})`);
  }
});
