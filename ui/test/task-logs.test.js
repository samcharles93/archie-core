import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup, fireEvent } from "@testing-library/preact";
import { TaskLogPanel, logCacheKey, taskLogDownloadURL } from "../src/tasks/task-logs.jsx";

function renderPanel(state, opts = {}) {
  const { container, unmount } = render(
    <TaskLogPanel
      state={state}
      onRetry={opts.onRetry}
      taskId={opts.taskId}
      attempt={opts.attempt}
      filters={opts.filters}
      onFilterChange={opts.onFilterChange}
      stages={opts.stages}
    />,
  );
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

// W7: the filter controls must sit ABOVE the three read outcomes. Below the
// disabled early return they would replace the deployment-level message with an
// empty filtered pane in exactly the deployment that cannot read logs.
const FILTER_OPTS = { filters: { level: "", stage: "" }, onFilterChange: () => {}, stages: ["prepare"] };

test("the three read outcomes survive with the filter bar rendered above them", () => {
  const disabled = renderPanel({ disabled: true, entries: [] }, FILTER_OPTS);
  assert.ok(disabled.root.querySelector(".task-log-filters"), "the filter bar renders first");
  assert.match(disabled.root.textContent, /No persisted log for this attempt/);
  assert.match(disabled.root.textContent, /cannot read task logs/);
  disabled.unmount();

  const absent = renderPanel({ found: false, entries: [], attempt: 0 }, FILTER_OPTS);
  assert.match(absent.root.textContent, /No log recorded for this attempt/);
  assert.doesNotMatch(absent.root.textContent, /cannot read task logs/);
  absent.unmount();

  const filtered = renderPanel({ disabled: false, found: true, entries: [], attempt: 0 }, FILTER_OPTS);
  assert.equal(filtered.root.querySelector(".empty-title").textContent, "Nothing recorded");
  filtered.unmount();
});

// W6: one cache entry per task could not represent two attempts, so a retry
// served the previous run's log under the new run's id.
test("the log cache key is keyed by task, attempt and filters", () => {
  assert.equal(logCacheKey(7, 1), "7|1||");
  assert.notEqual(logCacheKey(7, 1), logCacheKey(7, 2), "two attempts must not share a cache entry");
  assert.notEqual(logCacheKey(7, 1), logCacheKey(71, 1), "two tasks must not share a cache entry");
  assert.notEqual(
    logCacheKey(7, 1, { level: "ERROR", stage: "" }),
    logCacheKey(7, 1, { level: "", stage: "" }),
    "a filtered read is a different response and needs its own entry",
  );
});

// F2: a stage filter narrows to lines a stage tagged and nothing more. Agent and
// tool output has no stage, so a sparse result must be explained next to the
// control rather than left to look broken.
test("a stage filter states its coverage limit beside the control", () => {
  const { root, unmount } = renderPanel(
    { found: true, entries: [], attempt: 1 },
    { ...FILTER_OPTS, filters: { level: "", stage: "prepare" }, stages: ["prepare", "implement"] },
  );
  assert.match(root.textContent, /A stage filter matches only log lines that record a stage/);
  assert.match(root.textContent, /Agent and tool output carries none/);
  unmount();
});

test("an attempt with no recorded stage names says the stage filter has nothing to match", () => {
  const { root, unmount } = renderPanel(
    { found: true, entries: [], attempt: 1 },
    { ...FILTER_OPTS, stages: [] },
  );
  assert.match(root.textContent, /recorded no stage names/);
  unmount();
});

test("changing a filter reports the whole filter set to the page", () => {
  const seen = [];
  const { root, unmount } = renderPanel(
    { found: true, entries: [], attempt: 1 },
    { ...FILTER_OPTS, onFilterChange: (next) => seen.push(next) },
  );
  const level = root.querySelectorAll("select")[0];
  const stage = root.querySelectorAll("select")[1];
  fireEvent.change(level, { target: { value: "ERROR" } });
  fireEvent.change(stage, { target: { value: "prepare" } });
  assert.deepEqual(seen, [
    { level: "ERROR", stage: "" },
    { level: "", stage: "prepare" },
  ]);
  unmount();
});

// The pane names the attempt it is showing so it can never present attempt 1's
// log as attempt 2's.
test("the pane names the attempt it is showing", () => {
  const explicit = renderPanel({ found: true, attempt: 1, entries: [] }, { attempt: 1 });
  assert.match(explicit.root.textContent, /Attempt 1/);
  explicit.unmount();

  const resolved = renderPanel({ found: true, attempt: 4, entries: [] }, { attempt: null, taskId: 42 });
  assert.match(resolved.root.textContent, /Attempt 4/);
  assert.equal(
    resolved.root.querySelector(".task-log-download").getAttribute("href"),
    "/api/tasks/42/logs/download?attempt=4",
    "a download follows the attempt the pane is showing, not whatever is current",
  );
  resolved.unmount();
});

test("a panel without a page to act on a filter renders no filter controls", () => {
  const { root, unmount } = renderPanel({ found: true, entries: [], attempt: 1 });
  assert.equal(root.querySelector("select"), null, "a control that filters nothing is worse than none");
  assert.match(root.textContent, /Attempt 1/);
  unmount();
});

// R9: the pane carries one live region announcing its STATE, never a line at a
// time. A per-line live region would read the whole log aloud on every load,
// and a pane that loads 500 entries would be unusable with a screen reader.
test("the pane announces its state once, not every log line", () => {
  const { root, unmount } = renderPanel({
    found: true,
    attempt: 2,
    entries: [
      { level: "info", msg: "one" },
      { level: "info", msg: "two" },
    ],
  });
  const live = root.querySelector("[aria-live]");
  assert.ok(live, "the pane should carry a live region");
  assert.equal(live.getAttribute("role"), "status");
  assert.equal(live.getAttribute("aria-atomic"), "true");
  assert.match(live.textContent, /2 log entries/);
  assert.ok(live.className.includes("visually-hidden"), "the live text is for assistive technology only");
  assert.equal(root.querySelector(".log-list").contains(live), false, "the live region is not the list itself");
  unmount();
});

test("the live region says which of the log-unavailable cases applies", () => {
  const disabled = renderPanel({ disabled: true, entries: [] });
  assert.match(disabled.root.querySelector("[aria-live]").textContent, /cannot read task logs/);
  disabled.unmount();

  const absent = renderPanel({ found: false, entries: [], attempt: 0 });
  assert.match(absent.root.querySelector("[aria-live]").textContent, /No log recorded for this attempt/);
  assert.doesNotMatch(absent.root.querySelector("[aria-live]").textContent, /cannot read task logs/);
  absent.unmount();
});

test.after(() => cleanup());
