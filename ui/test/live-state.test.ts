import assert from "node:assert/strict";
import test from "node:test";

const { resourcesForEvent } = await import("../src/stores/live-events.ts");

test("backend events invalidate only their owning projections", () => {
  assert.deepEqual(resourcesForEvent({ kind: "stage_finish", task_id: 42 }), [
    "tasks",
  ]);
  assert.deepEqual(resourcesForEvent({ kind: "task_queued" }), ["tasks"]);
  assert.deepEqual(resourcesForEvent({ kind: "capture" }), ["captures"]);
  assert.deepEqual(resourcesForEvent({ kind: "curator_action" }), [
    "curators",
    "skills",
  ]);
  assert.deepEqual(resourcesForEvent({ kind: "update_report" }), ["updates"]);
  assert.deepEqual(resourcesForEvent({ kind: "log" }), []);
});

test("a closed stream is retried with doubling delay, capped at 30s", async () => {
  const { reconnectDelay } = await import("../src/lib/stream-state.ts");
  assert.deepEqual(
    [0, 1, 2, 3, 4, 5, 6, 10].map(reconnectDelay),
    [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000],
  );
});
