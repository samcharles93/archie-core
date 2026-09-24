import assert from "node:assert/strict";
import test from "node:test";

import { taskTimeLimitMs } from "../src/tasks/attempt-config.ts";

const captured = (limit: unknown) => ({
  kind: "config_captured",
  attempt: 1,
  data: { document: { budgets: { task_wall_clock: limit } } },
});

test("the attempt's task time limit is read from its captured config", () => {
  for (const [limit, want] of [
    ["4h0m0s", 4 * 3600_000],
    ["1h30m0s", 5400_000],
    ["90s", 90_000],
    ["0s", 0],
    [undefined, 0],
    ["nonsense", 0],
  ] as const) {
    assert.equal(taskTimeLimitMs(captured(limit)), want, String(limit));
  }
  assert.equal(taskTimeLimitMs(null), 0);
});
