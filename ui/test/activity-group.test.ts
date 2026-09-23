import assert from "node:assert/strict";
import test from "node:test";

const { groupActivity, groupKey } = await import("../src/dashboard/activity-group.ts");

import type { ActivityEvent } from "../src/dashboard/state.ts";

function ev(kind: string, taskID: number | undefined, detail: string, at = 1000): ActivityEvent {
  return { kind, task_id: taskID, detail, at };
}

test("consecutive same-kind background events collapse into one group", () => {
  const events = [
    ev("session-memory", undefined, "cycle 3"),
    ev("session-memory", undefined, "cycle 2"),
    ev("session-memory", undefined, "cycle 1"),
  ];
  const groups = groupActivity(events);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].count, 3);
  // The newest event (first in the feed) represents the group.
  assert.equal(groups[0].representative.detail, "cycle 3");
  assert.equal(groups[0].events.length, 3);
});

test("a singleton stays a plain one-count group", () => {
  const groups = groupActivity([ev("stage_finished", 7, "build")]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].count, 1);
  assert.equal(groups[0].events.length, 1);
});

test("a different task breaks the run", () => {
  const events = [
    ev("runner_run", 7, "a"),
    ev("runner_run", 9, "b"),
    ev("runner_run", 7, "c"),
  ];
  const groups = groupActivity(events);
  assert.equal(groups.length, 3);
  for (const group of groups) assert.equal(group.count, 1);
});

test("a different kind breaks the run", () => {
  const events = [
    ev("session-memory", undefined, "a"),
    ev("runner_run", undefined, "b"),
    ev("session-memory", undefined, "c"),
  ];
  const groups = groupActivity(events);
  assert.equal(groups.length, 3);
});

test("a missing kind resolves to the same label an unset row shows", () => {
  const events: ActivityEvent[] = [
    { detail: "a", at: 1000 },
    { detail: "b", at: 1001 },
  ];
  const groups = groupActivity(events);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].label, "event");
});

test("groupKey separates label and task", () => {
  assert.notEqual(groupKey("runner_run", 7), groupKey("runner_run", 9));
  assert.equal(groupKey("runner_run", 7), groupKey("runner_run", 7));
  assert.equal(groupKey("runner_run", 0), groupKey("runner_run", undefined));
});