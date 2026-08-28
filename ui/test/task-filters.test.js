import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { initialTaskFilter, taskMatchesStatus } from "../src/tasks/task-filters.js";
import { attentionStatusIds, statusIds } from "../src/base/task-meta.js";

test("the needs_you filter group matches exactly the catalog's attention statuses", () => {
  const attention = attentionStatusIds();
  // Every status the catalog marks as needing a human matches "needs_you".
  for (const id of attention) {
    assert.equal(taskMatchesStatus({ status: id }, "needs_you"), true, `${id} should match needs_you`);
  }
  // No non-attention catalog status does.
  for (const id of statusIds()) {
    if (!attention.has(id)) {
      assert.equal(taskMatchesStatus({ status: id }, "needs_you"), false, `${id} should not match needs_you`);
    }
  }
});

test("initialTaskFilter accepts needs_you and every catalog status, and drops unknowns", () => {
  assert.equal(initialTaskFilter(new URLSearchParams("status=needs_you")), "needs_you");
  // TASK_STATUSES is derived from the catalog, so every statusId is accepted.
  for (const id of statusIds()) {
    assert.equal(initialTaskFilter(new URLSearchParams(`status=${id}`)), id, `${id} should be accepted`);
  }
  assert.equal(initialTaskFilter(new URLSearchParams("status=quantum_superposition")), "");
  assert.equal(initialTaskFilter(new URLSearchParams("status=")), "");
});

test("taskMatchesStatus matches a specific catalog status exactly", () => {
  assert.equal(taskMatchesStatus({ status: "running" }, "running"), true);
  assert.equal(taskMatchesStatus({ status: "running" }, "queued"), false);
  assert.equal(taskMatchesStatus({ status: "running" }, ""), true);
});
