import assert from "node:assert/strict";
import test from "node:test";
import { groupActivity } from "../src/dashboard/activity-group.ts";
import type { ActivityEvent } from "../src/dashboard/state.ts";

const event = (kind: string, task_id: number | undefined, detail: string): ActivityEvent =>
  ({ kind, task_id, detail, at: 1000 });

test("activity groups only consecutive events with the same detail and task", () => {
  const cases: Array<{ name: string; events: ActivityEvent[]; counts: number[] }> = [
    { name: "repeated detail", events: [event("memory", undefined, "cycle"), event("memory", undefined, "cycle")], counts: [2] },
    { name: "different details", events: [event("stage", 7, "build"), event("stage", 7, "plan")], counts: [1, 1] },
    { name: "singleton", events: [event("stage", 7, "build")], counts: [1] },
    { name: "different tasks", events: [event("stage", 7, "build"), event("stage", 9, "build")], counts: [1, 1] },
    { name: "interleaved kinds", events: [event("curator", 7, "cycle"), event("memory", 7, "cycle")], counts: [2] },
    { name: "separated repeats", events: [event("stage", 7, "build"), event("stage", 9, "build"), event("stage", 7, "build")], counts: [1, 1, 1] },
    { name: "missing kind", events: [{ detail: "a", at: 1000 }, { detail: "a", at: 1001 }], counts: [2] },
  ];
  for (const { name, events, counts } of cases) {
    const groups = groupActivity(events);
    assert.deepEqual(groups.map((group) => group.count), counts, name);
    assert.deepEqual(groups.flatMap((group) => group.events), events, `${name}: preserve order and every event`);
    for (const group of groups) {
      assert.equal(group.representative, group.events[0], `${name}: newest event represents its group`);
    }
  }
});
