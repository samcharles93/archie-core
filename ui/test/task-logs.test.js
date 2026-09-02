import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { taskLogPanel } from "../src/tasks/task-logs.js";

test("loading state renders a loading placeholder", () => {
  const node = taskLogPanel(undefined, {});
  assert.match(node.className, /task-log-loading/);
  assert.match(node.textContent, /Loading/);
});

test("fetch failure renders a retry control", () => {
  const node = taskLogPanel(null, { onRetry: () => {} });
  assert.match(node.className, /task-log-error/);
  const btn = node.querySelector("button");
  assert.ok(btn, "a retry button should render");
  assert.equal(btn.textContent, "Retry");
});

test("disabled response (task logging not enabled) renders a placeholder, not a crash", () => {
  const node = taskLogPanel({ disabled: true, entries: [] });
  assert.match(node.textContent, /No persisted log/);
});

test("empty entries render a placeholder distinct from the disabled state", () => {
  const node = taskLogPanel({ disabled: false, entries: [] });
  assert.match(node.textContent, /Nothing recorded/);
});

test("entries render one row per entry with fields visible, surfacing stage/token detail", () => {
  const node = taskLogPanel({
    entries: [
      {
        time: "2026-01-01T00:00:00Z",
        level: "info",
        msg: "stage complete",
        fields: { stage: "implement", tokens: 1234, model: "sonnet" },
      },
      {
        time: "2026-01-01T00:01:00Z",
        level: "error",
        msg: "agent failed",
        fields: { iteration: 3 },
      },
    ],
  });
  assert.equal(node.children.length, 2, "one .log-row per entry");
  assert.match(node.textContent, /stage complete/);
  assert.match(node.textContent, /implement/);
  assert.match(node.textContent, /1234/);
  assert.match(node.textContent, /sonnet/);
  assert.match(node.textContent, /agent failed/);
  assert.match(node.textContent, /iteration/);
});
