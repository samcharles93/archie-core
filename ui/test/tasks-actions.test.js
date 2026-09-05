import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { render, waitFor } from "@testing-library/preact";

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
