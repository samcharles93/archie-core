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
