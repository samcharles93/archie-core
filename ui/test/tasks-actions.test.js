import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { render, waitFor, fireEvent } from "@testing-library/preact";

const cssLoad = "data:text/javascript," + encodeURIComponent(`
  export async function load(url, context, nextLoad) {
    if (url.endsWith(".css")) {
      return { format: "module", shortCircuit: true, source: "export default {};" };
    }
    return nextLoad(url, context);
  }
`);
register(cssLoad, import.meta.url);

const { api } = await import("../src/base/api.jsx");
const { tasksPage } = await import("../src/tasks/tasks.jsx");

async function renderTask(task) {
  const original = api.tasks;
  api.tasks = async () => [task];
  try {
    const vnode = tasksPage(new URLSearchParams());
    const { container } = render(vnode);
    await waitFor(() => {
      assert.ok(container.querySelector(".task-row"), "Wait for task to render");
    });
    return container;
  } finally {
    api.tasks = original;
  }
}

test("an action present in the catalog renders a button, not null", async () => {
  const task = {
    id: 7,
    title: "Fix the flaky test",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "tdd",
    actions: ["cancel"],
  };
  const root = await renderTask(task);
  const container = root.querySelector(".task-actions");
  assert.ok(container, "a known action should render a .task-actions container");
  const btn = container.querySelector("button");
  assert.ok(btn, "a button should be rendered for a catalog action");
  assert.ok(btn.className.includes("btn-quiet"), "cancel maps to the quiet variant");
  assert.equal(btn.textContent, "Cancel");
});

test("an id NOT in the catalog renders nothing", async () => {
  const task = {
    id: 8,
    title: "Poltergeist task",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "boot",
    actions: ["teleport"],
  };
  const root = await renderTask(task);
  assert.equal(root.querySelector(".task-actions"), null, "unknown action renders nothing");
});

test("a refused action (403) renders 'you can't do that' framing, not a generic failure", async () => {
  const task = {
    id: 10,
    title: "Refused task",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "tdd",
    actions: ["retry"],
  };
  const root = await renderTask(task);
  const { ApiError } = await import("../src/base/api.jsx");
  const original = api.taskAction;
  api.taskAction = async () => { throw new ApiError("cross-origin mutation refused", 403); };
  try {
    root.querySelector(".task-actions button").click();
    await waitFor(() => {
      assert.ok(root.querySelector(".task-action-error-refused"), "a refused action should render the refused variant");
    });
    const el = root.querySelector(".task-action-error-refused");
    assert.match(el.textContent, /you can't do that/i);
    assert.equal(root.querySelector(".task-action-error-broken"), null);
    assert.equal(root.querySelector(".task-action-error-session"), null);
  } finally {
    api.taskAction = original;
  }
});

test("a broken action (500) renders 'try again or check the daemon' framing", async () => {
  const task = {
    id: 11,
    title: "Broken task",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "tdd",
    actions: ["retry"],
  };
  const root = await renderTask(task);
  const { ApiError } = await import("../src/base/api.jsx");
  const original = api.taskAction;
  api.taskAction = async () => { throw new ApiError("task action failed", 500); };
  try {
    root.querySelector(".task-actions button").click();
    await waitFor(() => {
      assert.ok(root.querySelector(".task-action-error-broken"), "a broken action should render the broken variant");
    });
    const el = root.querySelector(".task-action-error-broken");
    assert.match(el.textContent, /try again, or check the daemon/i);
    assert.equal(root.querySelector(".task-action-error-refused"), null);
    assert.equal(root.querySelector(".task-action-error-session"), null);
  } finally {
    api.taskAction = original;
  }
});

test("a network failure with no status renders the broken variant", async () => {
  const task = {
    id: 12,
    title: "Network failure task",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "tdd",
    actions: ["retry"],
  };
  const root = await renderTask(task);
  const original = api.taskAction;
  api.taskAction = async () => { throw new TypeError("Failed to fetch"); };
  try {
    root.querySelector(".task-actions button").click();
    await waitFor(() => {
      assert.ok(root.querySelector(".task-action-error-broken"), "a network failure should render the broken variant");
    });
  } finally {
    api.taskAction = original;
  }
});

test("a 401 action failure prompts re-authentication instead of a generic failure", async () => {
  const task = {
    id: 13,
    title: "Expired session task",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "tdd",
    actions: ["retry"],
  };
  const root = await renderTask(task);
  const { ApiError } = await import("../src/base/api.jsx");
  const original = api.taskAction;
  api.taskAction = async () => { throw new ApiError("unauthorised", 401); };
  try {
    root.querySelector(".task-actions button").click();
    await waitFor(() => {
      assert.ok(root.querySelector(".task-action-error-session"), "a 401 should render the session-expired variant");
    });
    const el = root.querySelector(".task-action-error-session");
    assert.match(el.textContent, /session has expired/i);
    const reloadBtn = el.querySelector("button");
    assert.ok(reloadBtn, "the session-expired message should offer a reload action");
    assert.match(reloadBtn.textContent, /reload/i);
    assert.equal(root.querySelector(".task-action-error-refused"), null);
    assert.equal(root.querySelector(".task-action-error-broken"), null);
  } finally {
    api.taskAction = original;
  }
});

test("a link-kind action renders an anchor to the forge", async () => {
  const task = {
    id: 9,
    title: "Open the PR",
    status: "pr_open",
    repo: "sam/archie",
    workflow: "tdd",
    issue_number: 12,
    pr_number: 42,
    pr_url: "https://forge.example.internal/sam/archie/pull/42",
    actions: ["open_pr"],
  };
  const root = await renderTask(task);
  const link = root.querySelector(".task-actions")?.querySelector("a");
  assert.ok(link, "open_pr should render an <a> not a <button>");
  assert.equal(link.textContent, "Open PR");
  assert.equal(link.getAttribute("href"), "https://forge.example.internal/sam/archie/pull/42");
});

// The stuck-pane case: an open log pane whose row is collapsed while the task
// is retried. If the pane only ever loaded from the click that opened it, the
// new attempt's pane would sit on "Loading…" with nothing fetching it.
test("a log pane left open across a retry loads the new attempt when the row is reopened", async () => {
  const first = { id: 9, title: "Restart me", status: "parked", repo: "sam/archie", workflow: "tdd", attempt: 1, actions: ["retry"] };
  const second = { ...first, attempt: 2, status: "running" };
  const original = { tasks: api.tasks, task: api.task, taskAction: api.taskAction, taskLogs: api.taskLogs };
  let listReads = 0;
  const logReads = [];
  api.tasks = async () => {
    listReads += 1;
    return [listReads === 1 ? first : second];
  };
  api.task = async () => [
    { kind: "stage_start", attempt: listReads, stage: listReads === 1 ? "prepare" : "bootstrap", at: "2026-09-18T07:00:00Z" },
  ];
  api.taskLogs = async (id, params) => {
    logReads.push(params);
    return { found: true, attempt: params.attempt, disabled: false, entries: [{ level: "info", msg: "line" }] };
  };
  api.taskAction = async () => ({});
  try {
    const { container } = render(tasksPage(new URLSearchParams("task=9")));
    await waitFor(() => assert.match(container.textContent, /Started stage: prepare/));
    fireEvent.click(container.querySelector(".task-logs-section button"));
    await waitFor(() => assert.equal(logReads.length, 1));

    // Collapse the row, retry from the collapsed row, then reopen it.
    fireEvent.click(container.querySelector(".task-expand"));
    fireEvent.click(container.querySelector(".task-actions button"));
    await waitFor(() => assert.equal(listReads, 2));
    fireEvent.click(container.querySelector(".task-expand"));

    await waitFor(() => assert.match(container.textContent, /Started stage: bootstrap/));
    await waitFor(() => assert.equal(logReads.length, 2), { timeout: 2000 });
    assert.deepEqual(logReads[1], { attempt: 2, limit: 500 });
    assert.doesNotMatch(container.textContent, /Loading attempt log/);
  } finally {
    Object.assign(api, original);
  }
});

// R8: the run detail page is a per-task route, and the list is how an operator
// reaches it. The existing #/tasks?task=N deep link is a different affordance
// and stays working (pinned in operator-controls.test.js).
test("a task row links to its run detail page", async () => {
  const root = await renderTask({ id: 7, title: "Fix the flaky test", status: "parked", repo: "sam/archie", actions: [] });
  const link = root.querySelector(".task-detail-link");
  assert.ok(link, "the title should link to the run detail page");
  assert.equal(link.getAttribute("href"), "#/tasks/7");
  assert.equal(link.textContent, "Fix the flaky test");
});

// W8: the per-task caches used to be written and never invalidated, so a retry
// left the previous attempt's timeline (and, because the log cache was keyed by
// task, its log) on screen under the new run.
test("a retry drops the ended attempt's caches and reloads the new run", async () => {
  const first = { id: 8, title: "Flaky", status: "parked", repo: "sam/archie", workflow: "tdd", attempt: 1, actions: ["retry"] };
  const second = { ...first, attempt: 2, status: "running" };
  const original = { tasks: api.tasks, task: api.task, taskAction: api.taskAction, taskLogs: api.taskLogs };
  let listReads = 0;
  const timelineReads = [];
  const logReads = [];
  api.tasks = async () => {
    listReads += 1;
    return [listReads === 1 ? first : second];
  };
  api.task = async () => {
    timelineReads.push(timelineReads.length + 1);
    const attempt = timelineReads.length;
    return [
      {
        kind: "stage_start",
        attempt,
        stage: attempt === 1 ? "prepare" : "bootstrap",
        at: "2026-09-18T07:00:00Z",
      },
    ];
  };
  api.taskLogs = async (id, params) => {
    logReads.push(params);
    return { found: true, attempt: params.attempt, disabled: false, entries: [{ level: "info", msg: "line" }] };
  };
  api.taskAction = async () => ({});
  try {
    const { container } = render(tasksPage(new URLSearchParams("task=8")));
    await waitFor(() => assert.match(container.textContent, /Started stage: prepare/));

    fireEvent.click(container.querySelector(".task-logs-section button"));
    await waitFor(() => assert.equal(logReads.length, 1));
    assert.deepEqual(logReads[0], { attempt: 1, limit: 500 });

    fireEvent.click(container.querySelector(".task-actions button"));
    await waitFor(() => assert.match(container.textContent, /Started stage: bootstrap/));
    assert.equal(timelineReads.length, 2, "the timeline is refetched, not kept from the ended attempt");
    assert.doesNotMatch(container.textContent, /Started stage: prepare/);

    await waitFor(() => assert.equal(logReads.length, 2), { timeout: 2000 });
    assert.deepEqual(logReads[1], { attempt: 2, limit: 500 }, "the log pane follows the new attempt");
  } finally {
    Object.assign(api, original);
  }
});
