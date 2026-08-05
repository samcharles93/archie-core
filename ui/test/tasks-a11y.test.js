import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { taskRowA11y } from "../src/tasks/task-row.js";

test("task rows expose an explicit keyboard-accessible timeline control", () => {
  assert.deepEqual(taskRowA11y({ id: 42, title: "Keyboard task" }, false), {
    "aria-expanded": "false",
    "aria-controls": "task-timeline-42",
    "aria-label": "Expand timeline for Keyboard task",
  });
});
