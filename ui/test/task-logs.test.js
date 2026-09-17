import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { TaskLogPanel, taskLogDownloadURL } from "../src/tasks/task-logs.jsx";

function renderPanel(state, opts = {}) {
  const { container, unmount } = render(<TaskLogPanel state={state} onRetry={opts.onRetry} taskId={opts.taskId} />);
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

// The panel used to answer every failed read with "Task logging is optional
// and was not enabled for this run", which was false in a deployment where
// logging is unconditional: the dashboard simply could not read the log. The
// two conditions are now stated differently, and this pins that they stay so.
test("a reader that is absent says the dashboard cannot read logs, not that logging is off", () => {
  const { root, unmount } = renderPanel({ disabled: true, entries: [] });
  assert.match(root.textContent, /No persisted log/);
  assert.doesNotMatch(
    root.textContent,
    /not enabled for this run/,
    "this text blames a configuration setting for a process that simply cannot read logs",
  );
  unmount();
});

test("an attempt with a reader but no log file is not reported as disabled", () => {
  const { root, unmount } = renderPanel({ found: false, entries: [], attempt: 0 });
  assert.match(root.textContent, /No log recorded for this attempt/);
  assert.doesNotMatch(root.textContent, /was not enabled for this run/);
  unmount();
});

test("empty entries render a placeholder distinct from the disabled state", () => {
  const { root, unmount } = renderPanel({ disabled: false, found: true, entries: [], attempt: 0 });
  assert.match(root.textContent, /Nothing recorded/);
  unmount();
});

test("entries render one row per entry with fields visible, surfacing stage/token detail", () => {
  const { root, unmount } = renderPanel({
    found: true,
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

// The download is the feature the panel was missing: an operator who wants the
// log in their own tooling had no way to get it.
test("the download link points at the attempt being shown", () => {
  assert.equal(taskLogDownloadURL(42, 3), "/api/tasks/42/logs/download?attempt=3");
  // No attempt yet resolved: let the server pick the task's current one.
  assert.equal(taskLogDownloadURL(42, null), "/api/tasks/42/logs/download");
});

test("a panel with entries offers the download for that attempt", () => {
  const { root, unmount } = renderPanel({ found: true, attempt: 3, entries: [{ level: "info", msg: "hi" }] }, { taskId: 42 });
  const link = root.querySelector(".task-log-download");
  assert.ok(link, "a download control should render when the attempt has a log");
  assert.equal(link.getAttribute("href"), "/api/tasks/42/logs/download?attempt=3");
  unmount();
});

test("a panel for an attempt with no log offers no download", () => {
  const { root, unmount } = renderPanel({ found: false, attempt: 0, entries: [] }, { taskId: 42 });
  assert.equal(root.querySelector(".task-log-download"), null, "there is nothing to download");
  unmount();
});
