import assert from "node:assert/strict";
import test from "node:test";

const {
  ACTIVITY_FILTERS,
  activityEmptyTitle,
  filterActivity,
  matchesActivityFilter,
} = await import("../src/dashboard/activity-filter.ts");
const { passBar } = await import("../src/dashboard/pass-bar.ts");

/** A feed with both kinds in it: task events and the daemon's own telemetry. */
const feed = [
  { kind: "task_started", task_id: 41 },
  { kind: "curator_run" },
  { kind: "session-memory", task_id: 0 },
  { kind: "agent_finish", task_id: "42" },
];

test("All keeps the whole feed, in the order it arrived", () => {
  assert.deepEqual(filterActivity(feed, "all"), feed);
});

test("Tasks keeps the events a task owns, in arrival order", () => {
  assert.deepEqual(
    filterActivity(feed, "tasks").map((event) => event.kind),
    ["task_started", "agent_finish"],
  );
});

test("the two views account for every event exactly once", () => {
  const tasks = filterActivity(feed, "tasks");
  const system = filterActivity(feed, "system");
  assert.equal(
    tasks.length + system.length,
    feed.length,
    "an event showed in neither view, or in both",
  );
});

test("an event with no task id is system, whatever the id looks like", () => {
  for (const task_id of [0, "", "abc", -1, null, undefined]) {
    assert.equal(
      matchesActivityFilter({ task_id }, "tasks"),
      false,
      `task_id=${String(task_id)} read as a task event`,
    );
    assert.equal(matchesActivityFilter({ task_id }, "system"), true);
  }
  assert.equal(matchesActivityFilter({ task_id: "7" }, "tasks"), true);
});

test("the filter offers the three views the header renders", () => {
  assert.deepEqual(
    ACTIVITY_FILTERS.map((option) => [option.value, option.label]),
    [
      ["all", "All"],
      ["tasks", "Tasks"],
      ["system", "System"],
    ],
  );
});

test("an emptied feed says which view emptied it", () => {
  assert.equal(activityEmptyTitle(0, "all"), "Waiting for activity");
  assert.equal(activityEmptyTitle(0, "tasks"), "No task events yet");
  assert.equal(activityEmptyTitle(0, "system"), "No system events yet");
  assert.equal(activityEmptyTitle(2, "tasks"), "", "a feed with rows said it was empty");
});

test("a gate that passed every run draws full, in the passing colour", () => {
  assert.deepEqual(passBar(3, 3), { pct: 100, kind: "ok", track: "idle" });
});

test("a failing gate draws red at the share that passed", () => {
  assert.deepEqual(passBar(1, 4), { pct: 25, kind: "danger", track: "danger" });
});

test("a gate that delivered nothing still reads as failing", () => {
  const bar = passBar(0, 3);
  assert.equal(bar.pct, 0);
  assert.equal(bar.kind, "danger");
  assert.equal(
    bar.track,
    "danger",
    "a zero-width red bar on a plain track reads as a blank row",
  );
});

test("a gate with no runs has nothing to draw", () => {
  assert.deepEqual(passBar(0, 0), { pct: 0, kind: "idle", track: "idle" });
});

test("a count larger than the runs it came from does not overflow the bar", () => {
  assert.deepEqual(passBar(4, 3), { pct: 100, kind: "ok", track: "idle" });
});
