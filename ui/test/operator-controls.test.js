import { test } from "node:test";
import assert from "node:assert/strict";
import { dashboardTaskTargets } from "../src/dashboard/task-targets.js";
import {
  SETUP_DISMISSAL_KEY,
  dismissSetupComplete,
  setupPanelState,
} from "../src/dashboard/setup-preference.js";
import { initialTaskFilter, taskMatchesStatus } from "../src/tasks/task-filters.js";

function memoryStorage() {
  const values = new Map();
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: (key) => values.delete(key),
  };
}

test("dashboard links one attention task directly and groups multiple", () => {
  assert.equal(dashboardTaskTargets([{ id: 7, status: "parked" }]).attention.href, "#/tasks?task=7");
  assert.equal(dashboardTaskTargets([
    { id: 7, status: "parked" },
    { id: 8, status: "waiting_human" },
  ]).attention.href, "#/tasks?status=needs_you");
  assert.equal(dashboardTaskTargets([{ id: 9, status: "running" }]).running.href, "#/tasks?status=running");
});

test("setup completion dismissal is versioned and resets on regression", () => {
  const storage = memoryStorage();
  const complete = { steps: [{ done: true }] };
  const incomplete = { steps: [{ done: false }] };
  assert.equal(setupPanelState(complete, storage).kind, "complete");
  dismissSetupComplete(storage);
  assert.equal(storage.getItem(SETUP_DISMISSAL_KEY), "1");
  assert.equal(setupPanelState(complete, storage).kind, "dismissed");
  assert.equal(setupPanelState(incomplete, storage).kind, "incomplete");
  assert.equal(storage.getItem(SETUP_DISMISSAL_KEY), null);
});

test("needs-you filtering includes only parked and waiting tasks", () => {
  const params = new URLSearchParams("status=needs_you");
  assert.equal(initialTaskFilter(params), "needs_you");
  assert.equal(taskMatchesStatus({ status: "parked" }, "needs_you"), true);
  assert.equal(taskMatchesStatus({ status: "waiting_human" }, "needs_you"), true);
  assert.equal(taskMatchesStatus({ status: "running" }, "needs_you"), false);
  assert.equal(initialTaskFilter(new URLSearchParams("status=made_up")), "");
});
