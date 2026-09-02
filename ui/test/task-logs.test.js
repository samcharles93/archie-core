import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { TaskLogPanel } from "../src/tasks/task-logs.jsx";

function renderPanel(state, opts = {}) {
  const { container, unmount } = render(<TaskLogPanel state={state} onRetry={opts.onRetry} />);
  return { root: container, unmount };
}

test("loading state renders a loading placeholder", () => {
  const { root, unmount } = renderPanel(undefined);
  assert.match(root.querySelector(".task-log-loading")?.className || "", /task-log-loading/);
  assert.match(root.textContent, /Loading/);
  unmount();
});

test("fetch failure renders a retry control", () => {
  const { root, unmount } = renderPanel(null, { onRetry: () => {} });
  assert.match(root.querySelector(".task-log-error")?.className || "", /task-log-error/);
  const btn = root.querySelector("button");
  assert.ok(btn, "a retry button should render");
  assert.equal(btn.textContent, "Retry");
  unmount();
});

test("disabled response (task logging not enabled) renders a placeholder, not a crash", () => {
  const { root, unmount } = renderPanel({ disabled: true, entries: [] });
  assert.match(root.textContent, /No persisted log/);
  unmount();
});

test("empty entries render a placeholder distinct from the disabled state", () => {
  const { root, unmount } = renderPanel({ disabled: false, entries: [] });
  assert.match(root.textContent, /Nothing recorded/);
  unmount();
});

test("entries render one row per entry with fields visible, surfacing stage/token detail", () => {
  const { root, unmount } = renderPanel({
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
  const list = root.querySelector(".log-list");
  assert.ok(list, "the log list wrapper should render");
  assert.equal(list.children.length, 2, "one .log-row per entry");
  assert.match(root.textContent, /stage complete/);
  assert.match(root.textContent, /implement/);
  assert.match(root.textContent, /1234/);
  assert.match(root.textContent, /sonnet/);
  assert.match(root.textContent, /agent failed/);
  assert.match(root.textContent, /iteration/);
  unmount();
});
