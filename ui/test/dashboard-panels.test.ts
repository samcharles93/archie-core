import assert from "node:assert/strict";
import test from "node:test";
import { filterActivity, matchesActivityFilter } from "../src/dashboard/activity-filter.ts";
import { passBar, type PassBar } from "../src/dashboard/pass-bar.ts";

const feed = [
  { kind: "task_started", task_id: 41 },
  { kind: "curator_run" },
  { kind: "session-memory", task_id: 0 },
  { kind: "agent_finish", task_id: "42" },
];

test("activity filters partition task and system events without reordering", () => {
  for (const [filter, expected] of [
    ["all", feed],
    ["tasks", [feed[0], feed[3]]],
    ["system", [feed[1], feed[2]]],
  ] as const) {
    assert.deepEqual(filterActivity(feed, filter), expected, filter);
  }
  for (const task_id of [0, "", "abc", -1, null, undefined]) {
    assert.equal(matchesActivityFilter({ task_id }, "tasks"), false, String(task_id));
    assert.equal(matchesActivityFilter({ task_id }, "system"), true, String(task_id));
  }
  assert.equal(matchesActivityFilter({ task_id: "7" }, "tasks"), true);
});

test("gate bars show the delivered share and keep zero-pass failures visible", () => {
  const cases: Array<[number, number, PassBar]> = [
    [3, 3, { pct: 100, kind: "ok", track: "idle" }],
    [1, 4, { pct: 25, kind: "danger", track: "danger" }],
    [0, 3, { pct: 0, kind: "danger", track: "danger" }],
    [0, 0, { pct: 0, kind: "idle", track: "idle" }],
    [4, 3, { pct: 100, kind: "ok", track: "idle" }],
  ];
  for (const [delivered, runs, expected] of cases) {
    assert.deepEqual(passBar(delivered, runs), expected, `${delivered}/${runs}`);
  }
});
